// Package rdapprober reads a domain's registration from its registry over RDAP, finding the registry through IANA's RDAP bootstrap for DNS.
package rdapprober

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IANABootstrapURL is IANA's RDAP bootstrap for DNS (RFC 9224), which maps a top-level domain to its registry's RDAP base URLs.
const IANABootstrapURL = "https://data.iana.org/rdap/dns.json"

// BootstrapRefreshInterval is how long a read of the bootstrap is used before it is read again.
const BootstrapRefreshInterval = 24 * time.Hour

const bootstrapSizeLimit = 8 << 20

// Bootstrap is the RDAP bootstrap for DNS, read when first needed and again at most once per refresh interval; a failed read keeps the last one.
type Bootstrap struct {
	url    string
	client *http.Client
	now    func() time.Time

	mutex       sync.Mutex
	baseURLs    map[string][]string
	attemptedAt time.Time
}

// NewBootstrap is the bootstrap read from a URL with an HTTP client.
func NewBootstrap(url string, client *http.Client) *Bootstrap {
	return &Bootstrap{url: url, client: client, now: time.Now}
}

type bootstrapFile struct {
	Services [][][]string `json:"services"`
}

// BaseURLs are the RDAP base URLs of the registry of a domain, found by its longest suffix the bootstrap names.
func (bootstrap *Bootstrap) BaseURLs(ctx context.Context, domain string) ([]string, error) {
	baseURLs, err := bootstrap.current(ctx)
	if err != nil {
		return nil, err
	}
	labels := strings.Split(domain, ".")
	for start := range labels {
		if urls, named := baseURLs[strings.Join(labels[start:], ".")]; named {
			return urls, nil
		}
	}
	return nil, fmt.Errorf("the RDAP bootstrap names no registry for %s", domain)
}

func (bootstrap *Bootstrap) current(ctx context.Context) (map[string][]string, error) {
	bootstrap.mutex.Lock()
	defer bootstrap.mutex.Unlock()
	if bootstrap.baseURLs != nil && bootstrap.now().Sub(bootstrap.attemptedAt) < BootstrapRefreshInterval {
		return bootstrap.baseURLs, nil
	}
	bootstrap.attemptedAt = bootstrap.now()
	baseURLs, err := bootstrap.read(ctx)
	if err != nil {
		if bootstrap.baseURLs != nil {
			return bootstrap.baseURLs, nil
		}
		return nil, err
	}
	bootstrap.baseURLs = baseURLs
	return baseURLs, nil
}

func (bootstrap *Bootstrap) read(ctx context.Context) (map[string][]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, bootstrap.url, nil)
	if err != nil {
		return nil, err
	}
	response, err := bootstrap.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to read the RDAP bootstrap: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to read the RDAP bootstrap: %s answered %d", bootstrap.url, response.StatusCode)
	}
	var file bootstrapFile
	if err := json.NewDecoder(io.LimitReader(response.Body, bootstrapSizeLimit)).Decode(&file); err != nil {
		return nil, fmt.Errorf("failed to read the RDAP bootstrap: %w", err)
	}
	baseURLs := map[string][]string{}
	for _, service := range file.Services {
		if len(service) != 2 {
			continue
		}
		var secure []string
		for _, url := range service[1] {
			if strings.HasPrefix(url, "https://") {
				secure = append(secure, url)
			}
		}
		if len(secure) == 0 {
			continue
		}
		for _, entry := range service[0] {
			baseURLs[strings.ToLower(entry)] = secure
		}
	}
	if len(baseURLs) == 0 {
		return nil, errors.New("failed to read the RDAP bootstrap: it names no registry over HTTPS")
	}
	return baseURLs, nil
}
