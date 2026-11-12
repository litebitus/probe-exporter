package exporterconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const mockConfiguration = `modules:
  mock_tls:
    prober: tcp
    timeout: 5s
    tcp:
      preferred_ip_protocol: ip4
      tls: true
  mock_registration:
    prober: rdap
    timeout: 10s
  mock_lookup:
    prober: dns
    timeout: 3s
    dns:
      query_type: A
probe_configs:
  - module: mock_tls
    interval: 5m
    target_groups:
      mock-cdn:
        targets: [192.0.2.10:443, 192.0.2.11:443]
        hostnames: [mock-a.example, mock-b.example]
        labels:
          mock_site: mock-cdn
      mock-direct:
        targets: [mock-direct.example:443]
  - module: mock_registration
    interval: 6h
    target_groups:
      mock-domains:
        targets: [mock-domain.example]
  - module: mock_lookup
    interval: 1m
    target_groups:
      mock-resolvers:
        targets: [192.0.2.53]
        query_names: [mock-name.example]
max_concurrent_probes: 4
`

func parsed(t *testing.T, text string) Configuration {
	t.Helper()
	configuration, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("the configuration does not parse: %v", err)
	}
	return configuration
}

// changed is the mock configuration with one piece of its text replaced.
func changed(t *testing.T, old, replacement string) string {
	t.Helper()
	if !strings.Contains(mockConfiguration, old) {
		t.Fatalf("the mock configuration holds no %q", old)
	}
	return strings.Replace(mockConfiguration, old, replacement, 1)
}

func TestAConfigurationParsesIntoItsModulesAndProbeConfigs(t *testing.T) {
	configuration := parsed(t, mockConfiguration)
	if configuration.MaxConcurrentProbes != 4 || len(configuration.Modules) != 3 || len(configuration.ProbeConfigs) != 3 {
		t.Fatalf("configuration = %+v", configuration)
	}
	tls := configuration.Modules["mock_tls"]
	if tls.Prober != "tcp" || tls.Timeout != 5*time.Second || !tls.BlackboxModule.TCP.TLS || !strings.Contains(tls.WrittenText, "tls: true") {
		t.Errorf("mock_tls = %+v", tls)
	}
	registration := configuration.Modules["mock_registration"]
	if registration.Prober != RDAPProber || registration.Timeout != 10*time.Second {
		t.Errorf("mock_registration = %+v", registration)
	}
	group := configuration.ProbeConfigs[0].TargetGroups["mock-cdn"]
	if configuration.ProbeConfigs[0].Interval != 5*time.Minute || group.Labels["mock_site"] != "mock-cdn" || len(group.Hostnames) != 2 {
		t.Errorf("probe config = %+v", configuration.ProbeConfigs[0])
	}
}

// Each target is probed once per hostname and query name its group states, and once when it states none.
func TestEachTargetIsProbedOncePerHostnameAndQueryName(t *testing.T) {
	var got []string
	for _, probeTarget := range parsed(t, mockConfiguration).ProbeTargets() {
		got = append(got, strings.Join([]string{
			probeTarget.Module, probeTarget.TargetGroup, probeTarget.Target, probeTarget.Hostname, probeTarget.QueryName,
		}, "|"))
	}
	want := []string{
		"mock_tls|mock-cdn|192.0.2.10:443|mock-a.example|",
		"mock_tls|mock-cdn|192.0.2.10:443|mock-b.example|",
		"mock_tls|mock-cdn|192.0.2.11:443|mock-a.example|",
		"mock_tls|mock-cdn|192.0.2.11:443|mock-b.example|",
		"mock_tls|mock-direct|mock-direct.example:443||",
		"mock_registration|mock-domains|mock-domain.example||",
		"mock_lookup|mock-resolvers|192.0.2.53||mock-name.example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("probe targets =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAProbeTargetCarriesItsGroupsLabelsAndItsInterval(t *testing.T) {
	probeTarget := parsed(t, mockConfiguration).ProbeTargets()[0]
	if probeTarget.Labels["mock_site"] != "mock-cdn" || probeTarget.Interval != 5*time.Minute {
		t.Errorf("probe target = %+v", probeTarget)
	}
}

// Every value the exporter cannot use is refused, never corrected, and every problem is named at once.
func TestEveryUnusableValueIsRefused(t *testing.T) {
	for name, testCase := range map[string]struct {
		text string
		want string
	}{
		"empty":    {"", "the configuration is empty"},
		"not yaml": {"modules: [", "failed to parse the configuration"},
		"unknown field": {changed(t, "max_concurrent_probes: 4", "max_concurrent_probes: 4\nmock_field: 1"),
			"field mock_field not found"},
		"no modules":             {"probe_configs: []\nmax_concurrent_probes: 1\n", "`modules` is required, with at least one module"},
		"unknown prober":         {changed(t, "prober: tcp", "prober: mock"), "module `mock_tls`: prober 'mock' is not valid"},
		"unknown blackbox field": {changed(t, "query_type: A", "mock_setting: A"), "module `mock_lookup`:"},
		"unknown rdap field": {changed(t, "prober: rdap\n    timeout: 10s", "prober: rdap\n    timeout: 10s\n    mock_setting: 1"),
			"module `mock_registration`:"},
		"no timeout": {changed(t, "prober: rdap\n    timeout: 10s", "prober: rdap"),
			"module `mock_registration`: `timeout` is required, above zero"},
		"no concurrency": {changed(t, "max_concurrent_probes: 4", "max_concurrent_probes: 0"),
			"`max_concurrent_probes` is required, at least 1"},
		"no probe configs": {mockConfiguration[:strings.Index(mockConfiguration, "probe_configs:")] + "max_concurrent_probes: 1\n",
			"`probe_configs` is required, with at least one entry"},
		"no module named": {changed(t, "  - module: mock_registration\n", "  - module: \"\"\n"),
			"`probe_configs[1]`: `module` is required"},
		"unknown module": {changed(t, "  - module: mock_registration\n", "  - module: mock_missing\n"),
			"`probe_configs[1]`: `module` names `mock_missing`, which `modules` does not define"},
		"no interval": {changed(t, "interval: 6h", "interval: 0s"), "`probe_configs[1]`: `interval` is required, above zero"},
		"no groups": {changed(t, "interval: 6h\n    target_groups:\n      mock-domains:\n        targets: [mock-domain.example]",
			"interval: 6h\n    target_groups: {}"), "`probe_configs[1]`: `target_groups` is required, with at least one group"},
		"no targets": {changed(t, "targets: [mock-domain.example]", "targets: []"),
			"target group `mock-domains`: `targets` is required, with at least one target"},
		"empty target": {changed(t, "targets: [mock-domain.example]", `targets: ["", mock-domain.example]`),
			"target group `mock-domains`: `targets` holds an empty value"},
		"repeated target": {changed(t, "targets: [mock-domain.example]", "targets: [mock-domain.example, mock-domain.example]"),
			"target group `mock-domains`: `targets` holds `mock-domain.example` twice"},
		"repeated hostname": {changed(t, "hostnames: [mock-a.example, mock-b.example]", "hostnames: [mock-a.example, mock-a.example]"),
			"`hostnames` holds `mock-a.example` twice"},
		"label name": {changed(t, "mock_site: mock-cdn", "mock-site: mock-cdn"), "label `mock-site` is not a label name"},
		"reserved label": {changed(t, "mock_site: mock-cdn", "target: mock-cdn"),
			"label `target` is one the exporter sets on every series"},
		"double underscore label": {changed(t, "mock_site: mock-cdn", "__mock: mock-cdn"), "label `__mock` is not a label name"},
		"hostnames for rdap": {changed(t, "targets: [mock-domain.example]", "targets: [mock-domain.example]\n        hostnames: [mock-a.example]"),
			"`hostnames` is for a module of the `http` or `tcp` prober, not `rdap`"},
		"hostnames and a server name": {changed(t, "tls: true", "tls: true\n      tls_config:\n        server_name: mock-a.example"),
			"`hostnames` is set, and its module states a TLS `server_name`"},
		"query names for tcp": {changed(t, "targets: [mock-direct.example:443]", "targets: [mock-direct.example:443]\n        query_names: [mock-name.example]"),
			"`query_names` is for a module of the `dns` prober, not `tcp`"},
		"query names and a module query name": {changed(t, "query_type: A", "query_type: A\n      query_name: mock-name.example"),
			"`query_names` is set, and its module states a `query_name`"},
		"no query name": {changed(t, "        query_names: [mock-name.example]\n", ""),
			"`query_names` is required, since its module states no `query_name`"},
	} {
		_, err := Parse([]byte(testCase.text))
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s: err = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestAnHTTPModuleWithAHostHeaderTakesNoHostnames(t *testing.T) {
	text := `modules:
  mock_web:
    prober: http
    timeout: 5s
    http:
      headers:
        host: mock-a.example
probe_configs:
  - module: mock_web
    interval: 1m
    target_groups:
      mock-sites:
        targets: [https://192.0.2.10]
        hostnames: [mock-b.example]
max_concurrent_probes: 1
`
	_, err := Parse([]byte(text))
	if want := "`hostnames` is set, and its module states a `Host` header"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// A target probed the same way twice, by two probe configs naming the same module and group, is refused.
func TestATargetProbedTheSameWayTwiceIsRefused(t *testing.T) {
	text := mockConfiguration + `  - module: mock_registration
    interval: 1h
    target_groups:
      mock-domains:
        targets: [mock-domain.example]
`
	text = strings.Replace(text, "max_concurrent_probes: 4\n", "", 1) + "max_concurrent_probes: 4\n"
	_, err := Parse([]byte(text))
	want := "`probe_configs[3]`: target group `mock-domains`: target `mock-domain.example` is probed the same way in `probe_configs[1]`"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestAProbeConfigNamingAModuleThatFailedIsNotAlsoRefusedForIt(t *testing.T) {
	_, err := Parse([]byte(changed(t, "prober: rdap\n    timeout: 10s", "prober: rdap")))
	if err == nil || strings.Contains(err.Error(), "which `modules` does not define") {
		t.Errorf("err = %v", err)
	}
}

func TestTheDesignDocsExampleConfigurationLoads(t *testing.T) {
	design, err := os.ReadFile(filepath.Join("..", "docs", "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, afterOpening, _ := strings.Cut(string(design), "```yaml\n")
	example, _, _ := strings.Cut(afterOpening, "```")
	configuration := parsed(t, example)
	if len(configuration.ProbeTargets()) != 8 {
		t.Errorf("probe targets = %+v", configuration.ProbeTargets())
	}
}

func TestADNSModuleStatingItsQueryNameNeedsNoQueryNames(t *testing.T) {
	configuration := parsed(t, strings.Replace(changed(t, "query_type: A", "query_type: A\n      query_name: mock-name.example"),
		"        query_names: [mock-name.example]\n", "", 1))
	if got := configuration.Modules["mock_lookup"].BlackboxModule.DNS.QueryName; got != "mock-name.example" {
		t.Errorf("query name = %q", got)
	}
}

func TestAFileLoadsAsItsTextParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	if err := os.WriteFile(path, []byte(mockConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := LoadFile(path)
	if err != nil || len(configuration.Modules) != 3 {
		t.Errorf("configuration = %+v (%v)", configuration, err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "mock-missing.yml")); err == nil ||
		!strings.Contains(err.Error(), "failed to read the configuration file") {
		t.Errorf("a missing file: err = %v", err)
	}
}
