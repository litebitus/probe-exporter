package rdapprober

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

const answerSizeLimit = 1 << 20

// RDAPProber reads a domain's registration from its registry.
type RDAPProber struct {
	bootstrap *Bootstrap
	client    *http.Client
}

// NewRDAPProber is the prober finding registries through a bootstrap and asking them with an HTTP client.
func NewRDAPProber(bootstrap *Bootstrap, client *http.Client) RDAPProber {
	return RDAPProber{bootstrap: bootstrap, client: client}
}

type domainAnswer struct {
	Events []struct {
		EventAction string `json:"eventAction"`
		EventDate   string `json:"eventDate"`
	} `json:"events"`
}

// Probe asks the target domain's registry for its registration, and succeeds when the answer states when the registration expires.
func (rdapProber RDAPProber) Probe(ctx context.Context, target probeschedule.Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	domain := strings.TrimSuffix(strings.ToLower(target.Address), ".")
	baseURLs, err := rdapProber.bootstrap.BaseURLs(ctx, domain)
	if err != nil {
		logger.Error("failed to find the domain's registry", "domain", domain, "err", err)
		return false
	}
	domainURL := strings.TrimSuffix(baseURLs[0], "/") + "/domain/" + url.PathEscape(domain)
	logger.Debug("asking the registry", "url", domainURL)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, domainURL, nil)
	if err != nil {
		logger.Error("failed to build the request", "url", domainURL, "err", err)
		return false
	}
	request.Header.Set("Accept", "application/rdap+json")
	response, err := rdapProber.client.Do(request)
	if err != nil {
		logger.Error("failed to ask the registry", "url", domainURL, "err", err)
		return false
	}
	defer response.Body.Close()
	statusCode := prometheus.NewGauge(prometheus.GaugeOpts{Name: "probe_http_status_code", Help: "Response HTTP status code"})
	statusCode.Set(float64(response.StatusCode))
	registry.MustRegister(statusCode)
	if response.StatusCode != http.StatusOK {
		logger.Error("the registry answered without the domain's registration", "url", domainURL, "status_code", response.StatusCode)
		return false
	}
	var answer domainAnswer
	if err := json.NewDecoder(io.LimitReader(response.Body, answerSizeLimit)).Decode(&answer); err != nil {
		logger.Error("failed to read the registry's answer", "url", domainURL, "err", err)
		return false
	}
	for _, event := range answer.Events {
		if event.EventAction != "expiration" {
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339, event.EventDate)
		if err != nil {
			logger.Error("failed to read the expiration event's date", "event_date", event.EventDate, "err", err)
			return false
		}
		expiry := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "probe_domain_registration_expiry_timestamp_seconds",
			Help: "When the domain's registration expires, from its registry's expiration event",
		})
		expiry.Set(float64(expiresAt.Unix()))
		registry.MustRegister(expiry)
		logger.Debug("read the registration's expiry", "expires_at", expiresAt)
		return true
	}
	logger.Error("the registry's answer holds no expiration event", "url", domainURL)
	return false
}
