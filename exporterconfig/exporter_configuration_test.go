package exporterconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const mockConfiguration = `version: "1"
probes:
  mock_tls:
    prober: tcp
    timeout: 5s
    interval: 5m
    tcp:
      preferred_ip_protocol: ip4
      tls: true
    target_groups:
      mock-cdn:
        targets:
          - name: mock-a.example
            addresses: [192.0.2.10:443, 192.0.2.11:443]
          - name: mock-b.example
            addresses: [192.0.2.10:443]
        labels:
          mock_site: mock-cdn
  mock_registration:
    prober: rdap
    timeout: 10s
    interval: 6h
    target_groups:
      mock-domains:
        targets:
          - name: mock-domain.example
            addresses: [mock-domain.example]
  mock_lookup:
    prober: dns
    timeout: 3s
    interval: 1m
    dns:
      query_type: A
    target_groups:
      mock-resolvers:
        targets:
          - name: mock-name.example
            addresses: [192.0.2.53]
max_concurrent_probes: 4
`

const mockSource = "mock-config.yml"

func parsed(t *testing.T, text string) Configuration {
	t.Helper()
	configuration, err := Parse(mockSource, []byte(text))
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

// reportsProblem is whether a report names a problem whose message starts with a text, at a location, as a compiler reports one.
func reportsProblem(err error, messageStart, location string) bool {
	if err == nil {
		return false
	}
	for _, renderedProblem := range strings.Split(err.Error(), "\n\n") {
		headline, otherLines, _ := strings.Cut(renderedProblem, "\n")
		locationLine, _, _ := strings.Cut(otherLines, "\n")
		if strings.HasPrefix(headline, "error: "+messageStart) && strings.TrimSpace(locationLine) == "--> "+location {
			return true
		}
	}
	return false
}

func TestAConfigurationParsesIntoItsProbes(t *testing.T) {
	configuration := parsed(t, mockConfiguration)
	if configuration.Version != "1.0" || configuration.MaxConcurrentProbes != 4 || len(configuration.Probes) != 3 {
		t.Fatalf("configuration = %+v", configuration)
	}
	tls := configuration.Probes["mock_tls"]
	if tls.Prober != "tcp" || tls.Timeout != 5*time.Second || tls.Interval != 5*time.Minute || !tls.BlackboxModule.TCP.TLS {
		t.Errorf("mock_tls = %+v", tls)
	}
	if strings.Contains(tls.WrittenSettings, "target_groups") || strings.Contains(tls.WrittenSettings, "interval") ||
		!strings.Contains(tls.WrittenSettings, "tls: true") {
		t.Errorf("mock_tls's written settings = %q", tls.WrittenSettings)
	}
	group := tls.TargetGroups["mock-cdn"]
	if group.Labels["mock_site"] != "mock-cdn" || len(group.Targets) != 2 ||
		!reflect.DeepEqual(group.Targets[0], Target{Name: "mock-a.example", Addresses: []string{"192.0.2.10:443", "192.0.2.11:443"}}) {
		t.Errorf("mock-cdn = %+v", group)
	}
	registration := configuration.Probes["mock_registration"]
	if registration.Prober != RDAPProber || registration.Timeout != 10*time.Second || registration.Interval != 6*time.Hour {
		t.Errorf("mock_registration = %+v", registration)
	}
}

func TestAMinorStatedReadsAsItselfAndAMajorAsItsNewestMinor(t *testing.T) {
	for stated, want := range map[string]string{`"1"`: "1.0", `"1.0"`: "1.0"} {
		if got := parsed(t, changed(t, `version: "1"`, "version: "+stated)).Version; got != want {
			t.Errorf("version %s reads as %s, want %s", stated, got, want)
		}
	}
}

// Each target is run against once per address, its name with each.
func TestEachTargetRunsOncePerAddress(t *testing.T) {
	var got []string
	for _, probeTarget := range parsed(t, mockConfiguration).ProbeTargets() {
		got = append(got, strings.Join([]string{probeTarget.Probe, probeTarget.TargetGroup, probeTarget.Name, probeTarget.Address}, "|"))
	}
	want := []string{
		"mock_lookup|mock-resolvers|mock-name.example|192.0.2.53",
		"mock_registration|mock-domains|mock-domain.example|mock-domain.example",
		"mock_tls|mock-cdn|mock-a.example|192.0.2.10:443",
		"mock_tls|mock-cdn|mock-a.example|192.0.2.11:443",
		"mock_tls|mock-cdn|mock-b.example|192.0.2.10:443",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestARunCarriesItsGroupsLabelsAndItsProbesInterval(t *testing.T) {
	for _, probeTarget := range parsed(t, mockConfiguration).ProbeTargets() {
		if probeTarget.TargetGroup == "mock-cdn" && (probeTarget.Labels["mock_site"] != "mock-cdn" || probeTarget.Interval != 5*time.Minute) {
			t.Errorf("run = %+v", probeTarget)
		}
	}
}

// The version comes first: a file without one, or with one the exporter does not read, is checked no further.
func TestTheVersionIsCheckedBeforeAnythingElse(t *testing.T) {
	for name, testCase := range map[string]struct {
		text, message, location string
	}{
		"missing":     {changed(t, `version: "1"`+"\n", ""), "`version` is required, quoted, one of `\"1\"` or `\"1.0\"`", mockSource + ":1:1"},
		"unquoted":    {changed(t, `version: "1"`, "version: 1"), "`version` is required, quoted, one of `\"1\"` or `\"1.0\"`", mockSource + ":1:10"},
		"not carried": {changed(t, `version: "1"`, `version: "2"`), "`version` `\"2\"` is not a version this exporter reads; it reads `\"1\"` or `\"1.0\"`", mockSource + ":1:10"},
	} {
		_, err := Parse(mockSource, []byte(testCase.text))
		if !reportsProblem(err, testCase.message, testCase.location) || strings.Count(err.Error(), "error: ") != 1 {
			t.Errorf("%s: err = %v, want only %q at %s", name, err, testCase.message, testCase.location)
		}
	}
}

// Every value the exporter cannot use is refused, never corrected, each problem reported at its line and column.
func TestEveryUnusableValueIsReportedWhereItIs(t *testing.T) {
	for name, testCase := range map[string]struct {
		text, message, location string
	}{
		"empty":         {"", "the configuration is empty", mockSource + ":1:1"},
		"not a mapping": {"- mock\n", "a configuration is a mapping of its fields", mockSource + ":1:1"},
		"unknown field": {changed(t, "max_concurrent_probes: 4", "max_concurrent_probes: 4\nmock_field: 1"), "`mock_field` is not defined in the schema", mockSource + ":40:1"},
		"unknown prober": {changed(t, "prober: tcp", "prober: mock"),
			"`prober` must be one of `\"dns\"`, `\"grpc\"`, `\"http\"`, `\"icmp\"`, `\"rdap\"`, `\"tcp\"`, `\"unix\"`, `\"websocket\"`", mockSource + ":4:13"},
		"misspelled blackbox setting": {changed(t, "      tls: true", "      tlss: true"), "`tlss` is not defined in the schema", mockSource + ":9:7"},
		"settings of another prober":  {changed(t, "    prober: rdap\n", "    prober: rdap\n    tcp:\n      tls: true\n"), "`tcp` is not a valid key of `mock_registration`", mockSource + ":21:5"},
		"no interval":                 {changed(t, "    interval: 6h\n", ""), "`interval` is required", mockSource + ":19:3"},
		"no concurrency":              {changed(t, "max_concurrent_probes: 4", "max_concurrent_probes: 0"), "`max_concurrent_probes` must be >= `1`", mockSource + ":39:24"},
		"reserved label":              {changed(t, "mock_site: mock-cdn", "probe: mock-cdn"), "`probe` is not a valid key of `labels`", mockSource + ":18:11"},
		"tcp address without a port": {changed(t, "addresses: [192.0.2.10:443, 192.0.2.11:443]", "addresses: [192.0.2.10, 192.0.2.11:443]"),
			"`addresses[0]` must match pattern", mockSource + ":14:25"},
		"rdap address as a URL": {changed(t, "addresses: [mock-domain.example]", "addresses: [https://mock-domain.example]"),
			"`addresses[0]` must match pattern", mockSource + ":27:25"},
		"wildcard name": {changed(t, "name: mock-a.example", `name: "*.mock.example"`), "`name` must match pattern", mockSource + ":13:19"},
		"no name": {changed(t, "          - name: mock-b.example\n            addresses: [192.0.2.10:443]\n", "          - addresses: [192.0.2.10:443]\n"),
			"`name` is required", mockSource + ":15:13"},
		"no addresses": {changed(t, "            addresses: [192.0.2.53]\n", ""), "`addresses` is required", mockSource + ":37:13"},
		"unknown target field": {changed(t, "            addresses: [192.0.2.53]\n", "            addresses: [192.0.2.53]\n            mock_field: 1\n"),
			"`mock_field` is not defined in the schema", mockSource + ":39:13"},
		"a server name in the settings": {changed(t, "      tls: true", "      tls: true\n      tls_config:\n        server_name: mock-a.example"),
			"`tls_config` matches a form that is not allowed", mockSource + ":11:9"},
		"a query name in the settings": {changed(t, "query_type: A", "query_type: A\n      query_name: mock-name.example"),
			"`dns` matches a form that is not allowed", mockSource + ":33:7"},
		"a name checked at an address twice": {changed(t, "          - name: mock-b.example\n", "          - name: mock-a.example\n"),
			"`mock-a.example` is checked at `192.0.2.10:443` twice in this group", mockSource + ":16:25"},
		"zero timeout":  {changed(t, "timeout: 10s", "timeout: 0s"), "`timeout` must be above zero", mockSource + ":21:14"},
		"zero interval": {changed(t, "interval: 6h", "interval: 0s"), "`interval` must be above zero", mockSource + ":22:15"},
		"blackbox refuses the settings": {changed(t, "      tls: true", "      tls: true\n      query_response:\n        - expect: mock\n          expect_bytes: mock"),
			"probe `mock_tls`: expect and expect_bytes are mutually exclusive", mockSource + ":3:3"},
	} {
		_, err := Parse(mockSource, []byte(testCase.text))
		if !reportsProblem(err, testCase.message, testCase.location) {
			t.Errorf("%s: err = %v\nwant %q at %s", name, err, testCase.message, testCase.location)
		}
	}
}

func TestASettingANameStandsInForCarriesWhyAsItsSchemaNote(t *testing.T) {
	_, err := Parse(mockSource, []byte(changed(t, "      tls: true", "      tls: true\n      tls_config:\n        server_name: mock-a.example")))
	want := "[schema] A `tcp` probe's targets each state the `name` sent as the TLS server name, so its `tls_config` states no `server_name`."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want the note %q", err, want)
	}
}

func TestEveryProblemIsReportedAtOnceAndCounted(t *testing.T) {
	_, err := Parse(mockSource, []byte(changed(t, "interval: 6h", "interval: 6h\n    mock_field: 1")+"mock_other_field: 1\n"))
	if err == nil || !strings.HasSuffix(err.Error(), "\n\n2 errors") {
		t.Errorf("err = %v", err)
	}
}

func TestAnHTTPProbesSettingsStateNoHostHeader(t *testing.T) {
	text := `version: "1"
probes:
  mock_web:
    prober: http
    timeout: 5s
    interval: 1m
    http:
      headers:
        host: mock-a.example
    target_groups:
      mock-sites:
        targets:
          - name: mock-b.example
            addresses: [https://192.0.2.10]
max_concurrent_probes: 1
`
	_, err := Parse(mockSource, []byte(text))
	if !reportsProblem(err, "`host` is not a valid key of `headers`", mockSource+":9:9") {
		t.Errorf("err = %v", err)
	}
}

// Every prober takes its addresses in its own form.
func TestEveryProberTakesItsAddressesInItsOwnForm(t *testing.T) {
	for prober, testCase := range map[string]struct {
		valid, invalid string
	}{
		"dns":       {"[192.0.2.53, 192.0.2.53:5353, mock-resolver.example, 2001:db8::53]", "[https://192.0.2.53]"},
		"grpc":      {"[mock.example:9090, \"[2001:db8::1]:9090\"]", "[mock.example]"},
		"http":      {"[https://mock.example/healthz, http://192.0.2.10:8080]", "[mock.example]"},
		"icmp":      {"[mock.example, 192.0.2.10, 2001:db8::1]", "[mock.example:443]"},
		"rdap":      {"[mock.example]", "[mock]"},
		"tcp":       {"[mock.example:443, \"[2001:db8::1]:443\"]", "[mock.example:99999]"},
		"unix":      {"[/run/mock.sock]", "[run/mock.sock]"},
		"websocket": {"[wss://mock.example/socket, ws://192.0.2.10:8080]", "[https://mock.example]"},
	} {
		for _, addresses := range []string{testCase.valid, testCase.invalid} {
			text := "version: \"1\"\nprobes:\n  mock_probe:\n    prober: " + prober + "\n    timeout: 5s\n    interval: 1m\n" +
				"    target_groups:\n      mock-group:\n        targets:\n          - name: mock.example\n            addresses: " +
				addresses + "\nmax_concurrent_probes: 1\n"
			_, err := Parse(mockSource, []byte(text))
			if refused := err != nil; refused != (addresses == testCase.invalid) {
				t.Errorf("%s addresses %s: err = %v", prober, addresses, err)
			}
		}
	}
}

func TestEveryExampleConfigurationLoads(t *testing.T) {
	examples, err := filepath.Glob(filepath.Join("..", "examples", "*.yml"))
	if err != nil || len(examples) == 0 {
		t.Fatalf("examples = %v (%v)", examples, err)
	}
	for _, example := range examples {
		if _, err := LoadFile(example); err != nil {
			t.Errorf("%s: %v", example, err)
		}
	}
}

func TestADNSProbeTakesNoQueryNameOfItsOwn(t *testing.T) {
	if got := parsed(t, mockConfiguration).Probes["mock_lookup"].BlackboxModule.DNS.QueryName; got != "" {
		t.Errorf("query name = %q", got)
	}
	configuration := parsed(t, changed(t, "    dns:\n      query_type: A\n", ""))
	if probe := configuration.Probes["mock_lookup"]; probe.Prober != "dns" || probe.BlackboxModule.DNS.QueryName != "" {
		t.Errorf("mock_lookup = %+v", probe)
	}
}

func TestAFileLoadsAsItsTextParsesAndIsNamedByItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	if err := os.WriteFile(path, []byte(mockConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	if configuration, err := LoadFile(path); err != nil || len(configuration.Probes) != 3 {
		t.Errorf("configuration = %+v (%v)", configuration, err)
	}
	os.WriteFile(path, []byte(changed(t, "interval: 6h", "interval: 0s")), 0o600)
	if _, err := LoadFile(path); !reportsProblem(err, "`interval` must be above zero", path+":22:15") {
		t.Errorf("err = %v", err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "mock-missing.yml")); err == nil ||
		!strings.Contains(err.Error(), "failed to read the configuration file") {
		t.Errorf("a missing file: err = %v", err)
	}
}
