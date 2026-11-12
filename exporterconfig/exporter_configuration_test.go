package exporterconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const mockConfiguration = `version: "1"
probers:
  certificate:
    interval: 5m
    timeout: 5s
    ports: [443/tcp]
  dns:
    interval: 1m
    timeout: 3s
    ports: [53/udp]
    record_types: [A]
  domain_registration:
    interval: 6h
    timeout: 10s
    ports: [443/rdap]
    info_labels: [registrar, name_servers]
  http:
    interval: 1m
    timeout: 5s
    ports: [443/https]
    path: /healthz
    status_codes: [200]
  ping:
    interval: 1m
    timeout: 3s
    ports: [8/icmp]
  tcp:
    interval: 1m
    timeout: 3s
probe_targets:
  mock-api:
    addresses: [mock-api.example]
    probers:
      certificate: {}
  mock-cdn:
    addresses: [192.0.2.10, 192.0.2.11]
    probers:
      certificate:
        ports: [443/tcp, 8443/tcp]
      ping: {}
    serves: [mock-a.example, mock-b.example]
  mock-database:
    addresses: [192.0.2.20]
    probers:
      tcp:
        ports: [5432/tcp]
  mock-domain.example:
    probers:
      domain_registration: {}
  mock-resolver:
    addresses: [192.0.2.53]
    probers:
      dns:
        record_types: [A, AAAA]
        timeout: 4s
    serves: [mock-a.example]
  mock-web:
    addresses: [192.0.2.30]
    probers:
      http:
        ports: [80/http]
        path: /status
    serves: [mock-a.example]
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

// locationOf is where a piece of text first starts in another, as a problem names it: the source, the line and the column.
func locationOf(t *testing.T, text, piece string) string {
	t.Helper()
	index := strings.Index(text, piece)
	if index < 0 {
		t.Fatalf("the text holds no %q", piece)
	}
	before := text[:index]
	return fmt.Sprintf("%s:%d:%d", mockSource, strings.Count(before, "\n")+1, index-strings.LastIndex(before, "\n"))
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

func TestAConfigurationParsesIntoItsProbersAndTargets(t *testing.T) {
	configuration := parsed(t, mockConfiguration)
	if configuration.Version != "1.0" || len(configuration.Probers) != 6 || len(configuration.ProbeTargets) != 6 {
		t.Fatalf("configuration = %+v", configuration)
	}
	cdn := configuration.ProbeTargets["mock-cdn"]
	if !reflect.DeepEqual(cdn.Addresses, []string{"192.0.2.10", "192.0.2.11"}) || !reflect.DeepEqual(cdn.Serves, []string{"mock-a.example", "mock-b.example"}) ||
		!reflect.DeepEqual(cdn.Probers["certificate"].Ports, []Port{{443, "tcp"}, {8443, "tcp"}}) {
		t.Errorf("mock-cdn = %+v", cdn)
	}
}

// A target checked by a prober takes the prober's settings, each overridden where the target states its own.
func TestATargetOverridesTheProbersSettingsItStates(t *testing.T) {
	configuration := parsed(t, mockConfiguration)
	for name, testCase := range map[string]struct {
		target, prober string
		want           CheckSettings
	}{
		"the prober's own": {"mock-domain.example", "domain_registration", CheckSettings{
			Interval: 6 * time.Hour, Timeout: 10 * time.Second, Ports: []Port{{443, "rdap"}}, InfoLabels: []string{"registrar", "name_servers"},
		}},
		"ports overridden": {"mock-cdn", "certificate", CheckSettings{
			Interval: 5 * time.Minute, Timeout: 5 * time.Second, Ports: []Port{{443, "tcp"}, {8443, "tcp"}},
		}},
		"timeout and record types overridden": {"mock-resolver", "dns", CheckSettings{
			Interval: time.Minute, Timeout: 4 * time.Second, Ports: []Port{{53, "udp"}}, RecordTypes: []string{"A", "AAAA"},
		}},
		"ports and path overridden, status codes the prober's": {"mock-web", "http", CheckSettings{
			Interval: time.Minute, Timeout: 5 * time.Second, Ports: []Port{{80, "http"}}, Path: "/status", StatusCodes: []int{200},
		}},
	} {
		if got := configuration.CheckSettings(testCase.target, testCase.prober); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: settings = %+v, want %+v", name, got, testCase.want)
		}
	}
}

// A target chooses its own info labels in place of the prober's, and an empty list chooses none.
func TestATargetChoosesItsOwnInfoLabels(t *testing.T) {
	for written, want := range map[string][]string{"[registrar]": {"registrar"}, "[]": {}} {
		text := changed(t, "      domain_registration: {}\n", "      domain_registration:\n        info_labels: "+written+"\n")
		got := parsed(t, text).CheckSettings("mock-domain.example", "domain_registration").InfoLabels
		if got == nil || !reflect.DeepEqual(got, want) {
			t.Errorf("info_labels %s: the check publishes %v, want %v", written, got, want)
		}
	}
}

func TestAMinorStatedReadsAsItselfAndAMajorAsItsNewestMinor(t *testing.T) {
	for stated, want := range map[string]string{`"1"`: "1.0", `"1.0"`: "1.0"} {
		if got := parsed(t, changed(t, `version: "1"`, "version: "+stated)).Version; got != want {
			t.Errorf("version %s reads as %s, want %s", stated, got, want)
		}
	}
}

// A target is checked by each of its probers: at every address, on every port, for every host it serves and every record type where the prober takes them; a target that serves no other hosts is checked at each address as its own host, and a domain is checked as itself.
func TestEachProberChecksEachAddressPortHostAndRecordTypeItTakes(t *testing.T) {
	var got []string
	for _, run := range parsed(t, mockConfiguration).Runs() {
		got = append(got, strings.Join([]string{run.ProbeTarget, run.Prober, run.Address, run.Port.String(), run.Host, run.RecordType}, "|"))
	}
	want := []string{
		"mock-api|certificate|mock-api.example|443/tcp|mock-api.example|",
		"mock-cdn|certificate|192.0.2.10|443/tcp|mock-a.example|",
		"mock-cdn|certificate|192.0.2.10|443/tcp|mock-b.example|",
		"mock-cdn|certificate|192.0.2.10|8443/tcp|mock-a.example|",
		"mock-cdn|certificate|192.0.2.10|8443/tcp|mock-b.example|",
		"mock-cdn|certificate|192.0.2.11|443/tcp|mock-a.example|",
		"mock-cdn|certificate|192.0.2.11|443/tcp|mock-b.example|",
		"mock-cdn|certificate|192.0.2.11|8443/tcp|mock-a.example|",
		"mock-cdn|certificate|192.0.2.11|8443/tcp|mock-b.example|",
		"mock-cdn|ping|192.0.2.10|8/icmp||",
		"mock-cdn|ping|192.0.2.11|8/icmp||",
		"mock-database|tcp|192.0.2.20|5432/tcp||",
		"mock-domain.example|domain_registration|mock-domain.example|443/rdap||",
		"mock-resolver|dns|192.0.2.53|53/udp|mock-a.example|A",
		"mock-resolver|dns|192.0.2.53|53/udp|mock-a.example|AAAA",
		"mock-web|http|192.0.2.30|80/http|mock-a.example|",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
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
		old, replacement, message, at string
	}{
		"unknown field":                            {"probe_targets:\n", "mock_field: 1\nprobe_targets:\n", "`mock_field` is not defined in the schema", "mock_field"},
		"unknown prober":                           {"  tcp:\n    interval", "  mock:\n    interval", "`mock` is not defined in the schema", "mock:"},
		"certificate on udp":                       {"ports: [443/tcp]", "ports: [443/udp]", "`ports[0]` must match pattern", "443/udp"},
		"http on tcp":                              {"ports: [80/http]", "ports: [80/tcp]", "`ports[0]` must match pattern", "80/tcp"},
		"a ping that is no echo":                   {"ports: [8/icmp]", "ports: [0/icmp]", "`ports[0]`", "0/icmp"},
		"registration over whois":                  {"ports: [443/rdap]", "ports: [43/whois]", "`ports[0]`", "43/whois"},
		"unknown record type":                      {"record_types: [A, AAAA]", "record_types: [A, MOCK]", "`record_types[1]` must be one of", "MOCK"},
		"unknown info label":                       {"info_labels: [registrar, name_servers]", "info_labels: [registrar, mock_label]", "`info_labels[1]` must be one of `\"name_servers\"`, `\"registrar\"`", "mock_label"},
		"info labels on a prober with none":        {"    ports: [8/icmp]\n", "    ports: [8/icmp]\n    info_labels: [registrar]\n", "`info_labels` is not defined in the schema", "info_labels: [registrar]\n"},
		"a dns check without serves":               {"    serves: [mock-a.example]\n  mock-web", "  mock-web", "`serves` is required", "mock-resolver:"},
		"addresses on a domain":                    {"      domain_registration: {}\n", "      domain_registration: {}\n    addresses: [192.0.2.10]\n", "`addresses` matches a form that is not allowed", "[192.0.2.10]\n"},
		"no settings under probers":                {"  tcp:\n    interval: 1m\n    timeout: 3s\n", "", "`tcp` has no settings under `probers`", "tcp:\n        ports"},
		"no ports here or under probers":           {"      tcp:\n        ports: [5432/tcp]\n", "      tcp: {}\n", "`tcp` checks `mock-database` without `ports`; state it here or under `probers`", "tcp: {}"},
		"no status codes anywhere":                 {"    status_codes: [200]\n", "", "`http` checks `mock-web` without `status_codes`", "http:\n        ports"},
		"a timeout longer than the interval":       {"        timeout: 4s\n", "        timeout: 2m\n", "`dns` checks `mock-resolver` with a `timeout` of 2m0s, longer than its `interval` of 1m0s", "dns:\n        record"},
		"a zero override":                          {"        timeout: 4s\n", "        timeout: 0s\n", "`timeout` must be above zero", "0s\n    serves"},
		"a zero interval":                          {"interval: 6h", "interval: 0s", "`interval` must be above zero", "0s\n    timeout: 10s"},
		"registration of a name that is no domain": {"  mock-domain.example:", "  mock-domain:", "`mock-domain` is checked by `domain_registration`, so it is named by its domain", "mock-domain:"},
	} {
		text := changed(t, testCase.old, testCase.replacement)
		location := locationOf(t, text, testCase.at)
		_, err := Parse(mockSource, []byte(text))
		if !reportsProblem(err, testCase.message, location) {
			t.Errorf("%s: err = %v\nwant %q at %s", name, err, testCase.message, location)
		}
	}
}

func TestTheEmptyAndTheUnmappedAreRefused(t *testing.T) {
	for text, message := range map[string]string{"": "the configuration is empty", "- mock\n": "a configuration is a mapping of its fields"} {
		if _, err := Parse(mockSource, []byte(text)); !reportsProblem(err, message, mockSource+":1:1") {
			t.Errorf("%q: err = %v", text, err)
		}
	}
}

func TestAFieldNoProberOfTheTargetUsesCarriesWhyAsItsSchemaNote(t *testing.T) {
	_, err := Parse(mockSource, []byte(changed(t, "      domain_registration: {}\n", "      domain_registration: {}\n    serves: [mock-a.example]\n")))
	want := "[schema] A target is given `serves` only when a prober checking it asks for its hosts."
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

func yamlNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

func TestAPortIsANumberAndItsProtocol(t *testing.T) {
	var port Port
	for _, written := range []string{"443", "mock/tcp", "443/"} {
		if err := yamlNode(written).Decode(&port); err == nil {
			t.Errorf("%q was read as a port", written)
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

func TestAFileLoadsAsItsTextParsesAndIsNamedByItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	if err := os.WriteFile(path, []byte(mockConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	if configuration, err := LoadFile(path); err != nil || len(configuration.ProbeTargets) != 6 {
		t.Errorf("configuration = %+v (%v)", configuration, err)
	}
	text := changed(t, "interval: 6h", "interval: 0s")
	os.WriteFile(path, []byte(text), 0o600)
	if _, err := LoadFile(path); !reportsProblem(err, "`interval` must be above zero", path+strings.TrimPrefix(locationOf(t, text, "0s\n    timeout: 10s"), mockSource)) {
		t.Errorf("err = %v", err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "mock-missing.yml")); err == nil ||
		!strings.Contains(err.Error(), "failed to read the configuration file") {
		t.Errorf("a missing file: err = %v", err)
	}
	if _, err := LoadFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "failed to read the configuration file") {
		t.Errorf("a folder: err = %v", err)
	}
}

func TestAConfigurationFileLargerThanItsLimitIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock-config.yml")
	if err := os.WriteFile(path, []byte(mockConfiguration+strings.Repeat("#", configurationSizeLimit)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || err.Error() != "the configuration file "+path+" is larger than 1 MiB" {
		t.Errorf("err = %v", err)
	}
}
