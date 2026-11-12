package rdapprober

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

// mockRegistry is a registry's RDAP service, answering each domain it holds as RFC 9083 shapes a domain object.
type mockRegistry struct {
	server  *httptest.Server
	accept  atomic.Value
	answers map[string]mockAnswer
}

type mockAnswer struct {
	status int
	body   string
}

func newMockRegistry(t *testing.T, answers map[string]mockAnswer) *mockRegistry {
	registry := &mockRegistry{answers: answers}
	registry.server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		registry.accept.Store(request.Header.Get("Accept"))
		answer, held := registry.answers[strings.TrimPrefix(request.URL.Path, "/rdap/domain/")]
		if !held {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/rdap+json")
		writer.WriteHeader(answer.status)
		io.WriteString(writer, answer.body)
	}))
	t.Cleanup(registry.server.Close)
	return registry
}

func domainObject(name, expiration string) string {
	return fmt.Sprintf(`{"objectClassName":"domain","ldhName":%q,"events":[`+
		`{"eventAction":"registration","eventDate":"2020-01-02T03:04:05Z"},`+
		`{"eventAction":"expiration","eventDate":%q},`+
		`{"eventAction":"last update of RDAP database","eventDate":"2026-11-11T00:00:00Z"}]}`, name, expiration)
}

// mockBootstrap serves an RDAP bootstrap for DNS naming the registry for the `example` top-level domain.
func mockBootstrap(t *testing.T, body string) (*httptest.Server, *atomic.Int64) {
	reads := &atomic.Int64{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reads.Add(1)
		io.WriteString(writer, body)
	}))
	t.Cleanup(server.Close)
	return server, reads
}

func bootstrapNaming(registryURL string) string {
	return fmt.Sprintf(`{"version":"1.0","services":[`+
		`[["example"],["http://mock-insecure.example/rdap/","%s/rdap/"]],`+
		`[["mock-only-http"],["http://mock-insecure.example/rdap/"]],`+
		`[["mock-short"],["%s/rdap/"]]]}`, registryURL, registryURL)
}

type probeOutcome struct {
	success bool
	series  map[string]float64
	log     string
}

func probeDomain(t *testing.T, prober RDAPProber, domain string) probeOutcome {
	t.Helper()
	registry := prometheus.NewRegistry()
	log := &strings.Builder{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	success := prober.Probe(ctx, probeschedule.Target{Address: domain}, registry, slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[string]float64{}
	for _, family := range families {
		series[family.GetName()] = family.Metric[0].GetGauge().GetValue()
	}
	return probeOutcome{success: success, series: series, log: log.String()}
}

func mockProber(t *testing.T, answers map[string]mockAnswer) (RDAPProber, *mockRegistry) {
	registry := newMockRegistry(t, answers)
	bootstrapServer, _ := mockBootstrap(t, bootstrapNaming(registry.server.URL))
	client := registry.server.Client()
	return NewRDAPProber(NewBootstrap(bootstrapServer.URL, client), client), registry
}

func TestARegisteredDomainAnswersWhenItsRegistrationExpires(t *testing.T) {
	prober, registry := mockProber(t, map[string]mockAnswer{
		"mock-domain.example": {http.StatusOK, domainObject("mock-domain.example", "2027-08-20T04:00:00Z")},
	})
	outcome := probeDomain(t, prober, "Mock-Domain.Example.")
	want := float64(time.Date(2027, 8, 20, 4, 0, 0, 0, time.UTC).Unix())
	if !outcome.success || outcome.series["probe_domain_registration_expiry_timestamp_seconds"] != want ||
		outcome.series["probe_http_status_code"] != http.StatusOK {
		t.Errorf("outcome = %+v", outcome)
	}
	if accept := registry.accept.Load(); accept != "application/rdap+json" {
		t.Errorf("Accept = %v", accept)
	}
}

// Each failure is a failed probe whose log says why.
func TestEachFailureFailsTheProbeAndItsLogSaysWhy(t *testing.T) {
	prober, _ := mockProber(t, map[string]mockAnswer{
		"mock-no-expiry.example":   {http.StatusOK, `{"objectClassName":"domain","events":[{"eventAction":"registration","eventDate":"2020-01-02T03:04:05Z"}]}`},
		"mock-bad-date.example":    {http.StatusOK, domainObject("mock-bad-date.example", "mock-date")},
		"mock-not-json.example":    {http.StatusOK, "mock"},
		"mock-unavailable.example": {http.StatusServiceUnavailable, ""},
	})
	for domain, want := range map[string]string{
		"mock-no-expiry.example":     "the registry's answer holds no expiration event",
		"mock-bad-date.example":      "failed to read the expiration event's date",
		"mock-not-json.example":      "failed to read the registry's answer",
		"mock-unavailable.example":   "the registry answered without the domain's registration",
		"mock-missing.example":       "the registry answered without the domain's registration",
		"mock-domain.mock-nowhere":   "failed to find the domain's registry",
		"mock-domain.mock-only-http": "failed to find the domain's registry",
	} {
		outcome := probeDomain(t, prober, domain)
		if outcome.success || !strings.Contains(outcome.log, want) {
			t.Errorf("%s: outcome = %+v, want a log saying %q", domain, outcome, want)
		}
	}
}

func TestARegistryAnswerCarriesItsStatusCodeEvenWhenItFails(t *testing.T) {
	prober, _ := mockProber(t, map[string]mockAnswer{"mock-unavailable.example": {http.StatusServiceUnavailable, ""}})
	if outcome := probeDomain(t, prober, "mock-unavailable.example"); outcome.series["probe_http_status_code"] != http.StatusServiceUnavailable {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestAnUnreachableRegistryFailsTheProbe(t *testing.T) {
	registry := newMockRegistry(t, nil)
	bootstrapServer, _ := mockBootstrap(t, bootstrapNaming("https://127.0.0.1:1"))
	prober := NewRDAPProber(NewBootstrap(bootstrapServer.URL, registry.server.Client()), registry.server.Client())
	if outcome := probeDomain(t, prober, "mock-domain.example"); outcome.success || !strings.Contains(outcome.log, "failed to ask the registry") {
		t.Errorf("outcome = %+v", outcome)
	}
}

// A domain's registry is the one named for the domain's longest suffix the bootstrap names.
func TestADomainsRegistryIsNamedByItsLongestSuffix(t *testing.T) {
	bootstrapServer, _ := mockBootstrap(t, `{"services":[[["example"],["https://mock-tld.example/"]],[["co.example"],["https://mock-second-level.example/"]]]}`)
	bootstrap := NewBootstrap(bootstrapServer.URL, bootstrapServer.Client())
	for domain, want := range map[string]string{
		"mock.co.example": "https://mock-second-level.example/",
		"mock.example":    "https://mock-tld.example/",
	} {
		baseURLs, err := bootstrap.BaseURLs(context.Background(), domain)
		if err != nil || len(baseURLs) != 1 || baseURLs[0] != want {
			t.Errorf("%s: base URLs = %v (%v), want %s", domain, baseURLs, err, want)
		}
	}
}

func TestTheBootstrapIsReadAgainOnlyAfterItsRefreshInterval(t *testing.T) {
	bootstrapServer, reads := mockBootstrap(t, `{"services":[[["example"],["https://mock-tld.example/"]]]}`)
	bootstrap := NewBootstrap(bootstrapServer.URL, bootstrapServer.Client())
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	bootstrap.now = func() time.Time { return now }
	bootstrap.BaseURLs(context.Background(), "mock.example")
	now = now.Add(BootstrapRefreshInterval - time.Second)
	bootstrap.BaseURLs(context.Background(), "mock.example")
	if reads.Load() != 1 {
		t.Errorf("read %d times within the interval", reads.Load())
	}
	now = now.Add(time.Second)
	bootstrap.BaseURLs(context.Background(), "mock.example")
	if reads.Load() != 2 {
		t.Errorf("read %d times after the interval", reads.Load())
	}
}

// A failed read of the bootstrap keeps the last one, and with none read yet, fails the lookup.
func TestAFailedBootstrapReadKeepsTheLastOne(t *testing.T) {
	healthy := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !healthy {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(writer, `{"services":[[["example"],["https://mock-tld.example/"]]]}`)
	}))
	defer server.Close()
	bootstrap := NewBootstrap(server.URL, server.Client())
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	bootstrap.now = func() time.Time { return now }
	bootstrap.BaseURLs(context.Background(), "mock.example")
	healthy = false
	now = now.Add(BootstrapRefreshInterval)
	if baseURLs, err := bootstrap.BaseURLs(context.Background(), "mock.example"); err != nil || len(baseURLs) != 1 {
		t.Errorf("base URLs = %v (%v)", baseURLs, err)
	}
	fresh := NewBootstrap(server.URL, server.Client())
	if _, err := fresh.BaseURLs(context.Background(), "mock.example"); err == nil || !strings.Contains(err.Error(), "answered 502") {
		t.Errorf("err = %v", err)
	}
}

func TestABootstrapThatCannotBeUsedFailsTheLookup(t *testing.T) {
	for body, want := range map[string]string{
		"mock":                         "failed to read the RDAP bootstrap: invalid character",
		`{"services":[[["example"]]]}`: "it names no registry over HTTPS",
		`{"services":[[["example"],["http://mock.example/"]]]}`: "it names no registry over HTTPS",
	} {
		server, _ := mockBootstrap(t, body)
		if _, err := NewBootstrap(server.URL, server.Client()).BaseURLs(context.Background(), "mock.example"); err == nil ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
	unreachable := NewBootstrap("https://127.0.0.1:1/mock.json", http.DefaultClient)
	if _, err := unreachable.BaseURLs(context.Background(), "mock.example"); err == nil {
		t.Error("an unreachable bootstrap was read")
	}
	malformed := NewBootstrap("mock://\x00", http.DefaultClient)
	if _, err := malformed.BaseURLs(context.Background(), "mock.example"); err == nil {
		t.Error("a malformed bootstrap URL was read")
	}
}
