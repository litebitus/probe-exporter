// Package integrationtest runs the exporter as its users run it: from a configuration file, over HTTP, probing real listeners and reloading on a signal.
package integrationtest

import (
	"context"
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

// runningExporter is the exporter running with a configuration file, until the test ends.
type runningExporter struct {
	baseURL           string
	configurationFile string
	reloadSignals     chan os.Signal
	output            *strings.Builder
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

func startExporter(t *testing.T, configuration string, client *http.Client, bootstrapURL string) *runningExporter {
	t.Helper()
	exporter := &runningExporter{
		configurationFile: filepath.Join(t.TempDir(), "mock-config.yml"),
		reloadSignals:     make(chan os.Signal, 1),
		output:            &strings.Builder{},
	}
	exporter.write(t, configuration)
	address := freeAddress(t)
	exporter.baseURL = "http://" + address
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() {
		ended <- exporterapp.Run(ctx, []string{
			"--config.file=" + exporter.configurationFile, "--web.listen-address=" + address, "--log.level=debug",
		}, exporterapp.ProgramSettings{
			Version: "mock-version", Output: io.Discard, ReloadSignals: exporter.reloadSignals,
			RDAPBootstrapURL: bootstrapURL, HTTPClient: client,
		})
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

// mockEdge is what the exporter probes: a TLS listener, and a registry's RDAP service with its bootstrap.
type mockEdge struct {
	tlsAddress   string
	bootstrapURL string
	client       *http.Client
}

func startMockEdge(t *testing.T) mockEdge {
	t.Helper()
	tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(tlsServer.Close)
	registry := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rdap/domain/mock-domain.example" {
			http.NotFound(writer, request)
			return
		}
		io.WriteString(writer, `{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"2027-08-20T04:00:00Z"}]}`)
	}))
	t.Cleanup(registry.Close)
	bootstrap := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprintf(writer, `{"services":[[["example"],["%s/rdap/"]]]}`, registry.URL)
	}))
	t.Cleanup(bootstrap.Close)
	return mockEdge{tlsAddress: strings.TrimPrefix(tlsServer.URL, "https://"), bootstrapURL: bootstrap.URL, client: registry.Client()}
}

func (edge mockEdge) configuration(tlsGroups string) string {
	return `modules:
  mock_tls:
    prober: tcp
    timeout: 2s
    tcp:
      tls: true
      tls_config:
        insecure_skip_verify: true
  mock_registration:
    prober: rdap
    timeout: 2s
probe_configs:
  - module: mock_tls
    interval: 100ms
    target_groups:
` + tlsGroups + `  - module: mock_registration
    interval: 100ms
    target_groups:
      mock-domains:
        targets: [mock-domain.example, mock-missing.example]
max_concurrent_probes: 4
`
}

func (edge mockEdge) tlsGroup(name string, hostnames ...string) string {
	group := "      " + name + ":\n        targets: [" + edge.tlsAddress + "]\n        labels:\n          mock_site: " + name + "\n"
	if len(hostnames) > 0 {
		group += "        hostnames: [" + strings.Join(hostnames, ", ") + "]\n"
	}
	return group
}

// The exporter probes every target its configuration lists, on its own schedule, and serves each result labeled with its probe.
func TestTheExporterServesEachConfiguredTargetsResult(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge.configuration(edge.tlsGroup("mock-cdn", "mock-a.example", "mock-b.example")), edge.client, edge.bootstrapURL)
	want := []string{
		`probe_success{hostname="mock-a.example",mock_site="mock-cdn",module="mock_tls",target="` + edge.tlsAddress + `",target_group="mock-cdn"} 1`,
		`probe_success{hostname="mock-b.example",mock_site="mock-cdn",module="mock_tls",target="` + edge.tlsAddress + `",target_group="mock-cdn"} 1`,
		`probe_domain_registration_expiry_timestamp_seconds{module="mock_registration",target="mock-domain.example",target_group="mock-domains"} 1.8187344e+09`,
		`probe_success{module="mock_registration",target="mock-missing.example",target_group="mock-domains"} 0`,
		`probe_http_status_code{module="mock_registration",target="mock-missing.example",target_group="mock-domains"} 404`,
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
	exporter := startExporter(t, edge.configuration(edge.tlsGroup("mock-cdn")), edge.client, edge.bootstrapURL)
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
	exporter := startExporter(t, edge.configuration(edge.tlsGroup("mock-old")), edge.client, edge.bootstrapURL)
	waitFor(t, func() bool { return strings.Contains(exporter.get(t, "/metrics"), `target_group="mock-old"`) })
	exporter.reload(t, edge.configuration(edge.tlsGroup("mock-new")))
	waitFor(t, func() bool {
		metrics := exporter.get(t, "/metrics")
		return strings.Contains(metrics, `target_group="mock-new"`) && !strings.Contains(metrics, `target_group="mock-old"`)
	})
}

// A configuration that fails to load on a reload is refused and the running one kept.
func TestAFailedReloadKeepsTheRunningConfiguration(t *testing.T) {
	edge := startMockEdge(t)
	exporter := startExporter(t, edge.configuration(edge.tlsGroup("mock-cdn")), edge.client, edge.bootstrapURL)
	exporter.reload(t, "modules: [")
	waitFor(t, func() bool {
		return strings.Contains(exporter.get(t, "/metrics"), "probe_exporter_config_last_reload_successful 0")
	})
	time.Sleep(250 * time.Millisecond)
	if !strings.Contains(exporter.get(t, "/metrics"), `target_group="mock-cdn"`) {
		t.Error("the running configuration's targets were dropped")
	}
}

func TestTheExporterRefusesToStartWithoutAUsableConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	os.WriteFile(path, []byte("modules: ["), 0o600)
	for name, arguments := range map[string][]string{
		"no flags":            {},
		"unusable file":       {"--config.file=" + path, "--web.listen-address=" + freeAddress(t)},
		"missing file":        {"--config.file=" + path + ".missing", "--web.listen-address=" + freeAddress(t)},
		"unusable web config": {"--config.file=" + path, "--web.listen-address=" + freeAddress(t), "--web.config.file=" + path},
	} {
		err := exporterapp.Run(context.Background(), arguments, exporterapp.ProgramSettings{Output: io.Discard, HTTPClient: http.DefaultClient})
		if err == nil {
			t.Errorf("%s: the exporter started", name)
		}
	}
}

func TestTheExporterAnswersItsVersion(t *testing.T) {
	output := &strings.Builder{}
	err := exporterapp.Run(context.Background(), []string{"--version"}, exporterapp.ProgramSettings{Version: "mock-version", Output: output})
	if err != nil || !strings.Contains(output.String(), "mock-version") {
		t.Errorf("output = %q (%v)", output.String(), err)
	}
}
