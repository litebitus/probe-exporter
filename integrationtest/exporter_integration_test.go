// Package integrationtest runs the exporter as its users run it: from a configuration file, over HTTP, probing real listeners and reloading on a signal.
package integrationtest

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/litebitus/probe-exporter/exporterapp"
)

// trustedHost is a name the mock listener's certificate is issued for.
const trustedHost = "example.com"

// runningExporter is the exporter running with a configuration file, until the test ends.
type runningExporter struct {
	baseURL           string
	configurationFile string
	reloadSignals     chan os.Signal
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func startExporter(t *testing.T, edge mockEdge, configuration string) *runningExporter {
	t.Helper()
	exporter := &runningExporter{
		configurationFile: filepath.Join(t.TempDir(), "mock-config.yml"),
		reloadSignals:     make(chan os.Signal, 1),
	}
	exporter.write(t, configuration)
	address := freeAddress(t)
	exporter.baseURL = "http://" + address
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() {
		ended <- exporterapp.Run(ctx, []string{
			"--config.file=" + exporter.configurationFile, "--web.listen-address=" + address, "--log.level=debug",
		}, edge.programSettings(exporter.reloadSignals))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ended:
			if err != nil {
				t.Errorf("the exporter ended with %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("the exporter did not stop within 3s")
		}
	})
	waitFor(t, func() bool { return exporter.get(t, "/-/ready") == "ready\n" })
	return exporter
}

func (exporter *runningExporter) write(t *testing.T, configuration string) {
	t.Helper()
	if err := os.WriteFile(exporter.configurationFile, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (exporter *runningExporter) get(t *testing.T, path string) string {
	t.Helper()
	response, err := http.Get(exporter.baseURL + path)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return string(body)
}

func (exporter *runningExporter) reload(t *testing.T, configuration string) {
	t.Helper()
	exporter.write(t, configuration)
	exporter.reloadSignals <- syscall.SIGHUP
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not hold within 4s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mockEdge is what the exporter probes: an HTTPS listener with the certificate authority that issued its certificate, and a registry's RDAP service with its bootstrap.
type mockEdge struct {
	port                   string
	trustedAuthoritiesFile string
	bootstrapURL           string
	client                 *http.Client
}

func startMockEdge(t *testing.T) mockEdge {
	t.Helper()
	listener := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/mock-health" {
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(listener.Close)
	_, port, _ := net.SplitHostPort(listener.Listener.Addr().String())
	authorities := filepath.Join(t.TempDir(), "mock-authorities.pem")
	if err := os.WriteFile(authorities, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: listener.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rdap/domain/mock-domain.example" {
			http.NotFound(writer, request)
			return
		}
		io.WriteString(writer, `{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"2027-08-20T04:00:00Z"}],`+
			`"entities":[{"objectClassName":"entity","roles":["registrar"],"vcardArray":["vcard",[["fn",{},"text","Mock Registrar"]]]}],`+
			`"nameservers":[{"objectClassName":"nameserver","ldhName":"ns-1.mock-dns.example"}]}`)
	}))
	t.Cleanup(registry.Close)
	bootstrap := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprintf(writer, `{"services":[[["example"],["%s/rdap/"]]]}`, registry.URL)
	}))
	t.Cleanup(bootstrap.Close)
	return mockEdge{port: port, trustedAuthoritiesFile: authorities, bootstrapURL: bootstrap.URL, client: registry.Client()}
}

func (edge mockEdge) programSettings(reloadSignals chan os.Signal) exporterapp.ProgramSettings {
	return exporterapp.ProgramSettings{
		Version: "mock-version", Output: io.Discard, ReloadSignals: reloadSignals,
		RDAPBootstrapURL: edge.bootstrapURL, HTTPClient: edge.client, TrustedAuthoritiesFile: edge.trustedAuthoritiesFile,
	}
}

// configuration checks the listener, as the target named, with the certificate, http and tcp probers, the target overriding the http path, and two domains' registrations.
func (edge mockEdge) configuration(targetName string) string {
	return `version: "1"
probers:
  certificate:
    interval: 500ms
    timeout: 500ms
    ports: [` + edge.port + `/tcp]
  domain_registration:
    interval: 500ms
    timeout: 500ms
    ports: [443/rdap]
    info_labels: [registrar, name_servers]
  http:
    interval: 500ms
    timeout: 500ms
    ports: [` + edge.port + `/https]
    path: /mock-missing
    status_codes: [200]
  tcp:
    interval: 500ms
    timeout: 500ms
probe_targets:
  ` + targetName + `:
    addresses: [127.0.0.1]
    probers:
      certificate: {}
      http:
        path: /mock-health
      tcp:
        ports: [` + edge.port + `/tcp]
    serves: [` + trustedHost + `]
  mock-domain.example:
    probers:
      domain_registration: {}
  mock-missing.example:
    probers:
      domain_registration: {}
`
}

// The exporter checks every target its configuration lists with each of the target's probers, on its own schedule, and serves each result labeled with its place.
func TestTheExporterServesEachConfiguredTargetsResult(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge, edge.configuration("mock-cdn"))
	place := func(prober, protocol string) string {
		return `address="127.0.0.1",host="` + trustedHost + `",port="` + edge.port + "/" + protocol + `",probe_target="mock-cdn",prober="` + prober + `"`
	}
	want := []string{
		`probe_success{` + place("certificate", "tcp") + `} 1`,
		`probe_ssl_earliest_cert_expiry{` + place("certificate", "tcp") + `}`,
		`probe_success{` + place("http", "https") + `} 1`,
		`probe_http_status_code{` + place("http", "https") + `} 200`,
		`probe_success{address="127.0.0.1",port="` + edge.port + `/tcp",probe_target="mock-cdn",prober="tcp"} 1`,
		`probe_domain_registration_expiry_timestamp_seconds{address="mock-domain.example",port="443/rdap",probe_target="mock-domain.example",prober="domain_registration"} 1.8187344e+09`,
		`probe_domain_registration_info{address="mock-domain.example",name_servers="ns-1.mock-dns.example",port="443/rdap",probe_target="mock-domain.example",prober="domain_registration",registrar="Mock Registrar"} 1`,
		`probe_success{address="mock-missing.example",port="443/rdap",probe_target="mock-missing.example",prober="domain_registration"} 0`,
		`probe_exporter_config_last_reload_successful 1`,
	}
	waitFor(t, func() bool {
		metrics := exporter.get(t, "/metrics")
		for _, series := range want {
			if !strings.Contains(metrics, series) {
				return false
			}
		}
		return true
	})
	results := exporter.get(t, "/results")
	for _, part := range []string{"<td>mock-missing.example</td>", "the registry answered without the domain&#39;s registration"} {
		if !strings.Contains(results, part) {
			t.Errorf("the results page holds no %q", part)
		}
	}
	if exporter.get(t, "/-/healthy") != "healthy\n" {
		t.Error("the exporter is not healthy")
	}
}

func TestTheExporterServesOpenMetricsWhenTheScraperAsksForIt(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge, edge.configuration("mock-cdn"))
	request, _ := http.NewRequest(http.MethodGet, exporter.baseURL+"/metrics", nil)
	request.Header.Set("Accept", "application/openmetrics-text;version=1.0.0")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/openmetrics-text") || !strings.HasSuffix(string(body), "# EOF\n") {
		t.Errorf("content type = %s", response.Header.Get("Content-Type"))
	}
}

// On a reload, a target no longer configured loses its result, and a new one is probed.
func TestAReloadDropsTargetsNoLongerConfiguredAndProbesNewOnes(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge, edge.configuration("mock-old"))
	waitFor(t, func() bool { return strings.Contains(exporter.get(t, "/metrics"), `probe_target="mock-old"`) })
	exporter.reload(t, edge.configuration("mock-new"))
	waitFor(t, func() bool {
		metrics := exporter.get(t, "/metrics")
		return strings.Contains(metrics, `probe_target="mock-new"`) && !strings.Contains(metrics, `probe_target="mock-old"`)
	})
}

// A configuration that fails to load on a reload is refused and the running one kept.
func TestAFailedReloadKeepsTheRunningConfiguration(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge, edge.configuration("mock-cdn"))
	exporter.reload(t, "probers: [")
	waitFor(t, func() bool {
		return strings.Contains(exporter.get(t, "/metrics"), "probe_exporter_config_last_reload_successful 0")
	})
	time.Sleep(250 * time.Millisecond)
	if !strings.Contains(exporter.get(t, "/metrics"), `probe_target="mock-cdn"`) {
		t.Error("the running configuration's targets were dropped")
	}
}

func TestTheExporterRefusesToStartWithoutAUsableConfiguration(t *testing.T) {
	edge := startMockEdge(t)
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	os.WriteFile(path, []byte("probers: ["), 0o600)
	usable := filepath.Join(t.TempDir(), "mock-usable-config.yml")
	os.WriteFile(usable, []byte(edge.configuration("mock-cdn")), 0o600)
	tooLarge := filepath.Join(t.TempDir(), "mock-large-config.yml")
	os.WriteFile(tooLarge, []byte(edge.configuration("mock-cdn")+strings.Repeat("#", 1<<20)), 0o600)
	for name, testCase := range map[string]struct {
		arguments []string
		message   string
	}{
		"no flags":            {[]string{}, "required flag(s)"},
		"unusable file":       {[]string{"--config.file=" + path, "--web.listen-address=" + freeAddress(t)}, "error: "},
		"missing file":        {[]string{"--config.file=" + path + ".missing", "--web.listen-address=" + freeAddress(t)}, "failed to read the configuration file"},
		"unusable web config": {[]string{"--config.file=" + usable, "--web.listen-address=" + freeAddress(t), "--web.config.file=" + path}, ""},
		"a file too large":    {[]string{"--config.file=" + tooLarge, "--web.listen-address=" + freeAddress(t)}, "is larger than 1 MiB"},
	} {
		err := exporterapp.Run(context.Background(), testCase.arguments, edge.programSettings(nil))
		if err == nil || !strings.Contains(err.Error(), testCase.message) {
			t.Errorf("%s: err = %v, want %q", name, err, testCase.message)
		}
	}
}

func TestTheExporterStopsWhenItCannotListen(t *testing.T) {
	edge := startMockEdge(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	os.WriteFile(path, []byte(edge.configuration("mock-cdn")), 0o600)
	err = exporterapp.Run(context.Background(), []string{"--config.file=" + path, "--web.listen-address=" + taken.Addr().String()},
		edge.programSettings(nil))
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("err = %v", err)
	}
}

func TestTheExporterAnswersItsVersion(t *testing.T) {
	output := &strings.Builder{}
	err := exporterapp.Run(context.Background(), []string{"--version"}, exporterapp.ProgramSettings{Version: "mock-version", Output: output})
	if err != nil || !strings.Contains(output.String(), "mock-version") {
		t.Errorf("output = %q (%v)", output.String(), err)
	}
}
