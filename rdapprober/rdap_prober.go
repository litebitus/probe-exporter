package rdapprober

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

const answerSizeLimit = 1 << 20

// infoSeriesProber names the prober in its info series, `probe_domain_registration_info`.
const infoSeriesProber = "domain_registration"

// InfoLabels are the facts of a registration the prober can publish, each a label of its info series.
var InfoLabels = []string{"name_servers", "registrar"}

// RDAPProber reads a domain's registration from its registry, publishing the info labels chosen for it.
type RDAPProber struct {
	bootstrap  *Bootstrap
	client     *http.Client
	infoLabels []string
}

// NewRDAPProber is the prober finding registries through a bootstrap and asking them with an HTTP client, publishing the info labels given; a label that is not one of InfoLabels is refused.
func NewRDAPProber(bootstrap *Bootstrap, client *http.Client, infoLabels []string) (RDAPProber, error) {
	for _, label := range infoLabels {
		if !slices.Contains(InfoLabels, label) {
			return RDAPProber{}, fmt.Errorf("the `%s` prober has no info label `%s`", infoSeriesProber, label)
		}
	}
	return RDAPProber{bootstrap: bootstrap, client: client, infoLabels: infoLabels}, nil
}

type domainAnswer struct {
	Events []struct {
		EventAction string `json:"eventAction"`
		EventDate   string `json:"eventDate"`
	} `json:"events"`
	Entities []struct {
		Roles      []string          `json:"roles"`
		VCardArray []json.RawMessage `json:"vcardArray"`
	} `json:"entities"`
	Nameservers []struct {
		LDHName string `json:"ldhName"`
	} `json:"nameservers"`
}

// registrarName is the formatted name of the entity whose role is `registrar`, from its jCard (RFC 7095); empty where the answer names none.
func (answer domainAnswer) registrarName() string {
	for _, entity := range answer.Entities {
		if !slices.Contains(entity.Roles, "registrar") || len(entity.VCardArray) < 2 {
			continue
		}
		var properties [][]any
		if json.Unmarshal(entity.VCardArray[1], &properties) != nil {
			continue
		}
		for _, property := range properties {
			if len(property) >= 4 && property[0] == "fn" {
				if name, isText := property[3].(string); isText {
					return name
				}
			}
		}
	}
	return ""
}

// nameServers are the domain's name servers, lower case and in name order, joined by `, `; empty where the answer names none.
func (answer domainAnswer) nameServers() string {
	var names []string
	for _, nameServer := range answer.Nameservers {
		names = append(names, strings.TrimSuffix(strings.ToLower(nameServer.LDHName), "."))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
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
	probeschedule.PublishInfo(registry, infoSeriesProber, map[string]string{
		"name_servers": answer.nameServers(),
		"registrar":    answer.registrarName(),
	}, rdapProber.infoLabels)
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
