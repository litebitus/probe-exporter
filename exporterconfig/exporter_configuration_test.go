package exporterconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const mockConfiguration = `version: "1"
probers:
  certificate:
    interval: 5m
    timeout: 5s
    ports: [443, 8443]
  domain_registration:
    interval: 6h
    timeout: 10s
  ping:
    interval: 1m
    timeout: 3s
probe_targets:
  mock-cdn:
    addresses: [192.0.2.10, 192.0.2.11]
    probers: [certificate, ping]
    serves: [mock-a.example, mock-b.example]
  mock-domain.example:
    probers: [domain_registration]
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

func TestAConfigurationParsesIntoItsProbersAndTargets(t *testing.T) {
	configuration := parsed(t, mockConfiguration)
	if configuration.Version != "1.0" || len(configuration.Probers) != 3 || len(configuration.ProbeTargets) != 2 {
		t.Fatalf("configuration = %+v", configuration)
	}
	wantSettings := ProberSettings{Interval: 5 * time.Minute, Timeout: 5 * time.Second, Ports: []int{443, 8443}}
	if got := configuration.Probers[CertificateProber]; !reflect.DeepEqual(got, wantSettings) {
		t.Errorf("certificate = %+v", got)
	}
	wantTarget := ProbeTarget{
		Probers:   []string{CertificateProber, PingProber},
		Addresses: []string{"192.0.2.10", "192.0.2.11"},
		Serves:    []string{"mock-a.example", "mock-b.example"},
	}
	if got := configuration.ProbeTargets["mock-cdn"]; !reflect.DeepEqual(got, wantTarget) {
		t.Errorf("mock-cdn = %+v", got)
	}
}

func TestAMinorStatedReadsAsItselfAndAMajorAsItsNewestMinor(t *testing.T) {
	for stated, want := range map[string]string{`"1"`: "1.0", `"1.0"`: "1.0"} {
		if got := parsed(t, changed(t, `version: "1"`, "version: "+stated)).Version; got != want {
			t.Errorf("version %s reads as %s, want %s", stated, got, want)
		}
	}
}

func runPlaces(configuration Configuration) []string {
	var places []string
	for _, run := range configuration.Runs() {
		places = append(places, strings.Join([]string{run.ProbeTarget, run.Prober, run.Address, strconv.Itoa(run.Port), run.Host}, "|"))
	}
	return places
}

// A target is checked by each of its probers: at every address, on every port the prober takes, for every host it serves where the prober asks for one; a domain is checked as itself.
func TestEachProberChecksEachAddressPortAndHostItTakes(t *testing.T) {
	want := []string{
		"mock-cdn|certificate|192.0.2.10|443|mock-a.example",
		"mock-cdn|certificate|192.0.2.10|443|mock-b.example",
		"mock-cdn|certificate|192.0.2.10|8443|mock-a.example",
		"mock-cdn|certificate|192.0.2.10|8443|mock-b.example",
		"mock-cdn|certificate|192.0.2.11|443|mock-a.example",
		"mock-cdn|certificate|192.0.2.11|443|mock-b.example",
		"mock-cdn|certificate|192.0.2.11|8443|mock-a.example",
		"mock-cdn|certificate|192.0.2.11|8443|mock-b.example",
		"mock-cdn|ping|192.0.2.10|0|",
		"mock-cdn|ping|192.0.2.11|0|",
		"mock-domain.example|domain_registration|mock-domain.example|0|",
	}
	if got := runPlaces(parsed(t, mockConfiguration)); !reflect.DeepEqual(got, want) {
		t.Errorf("runs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEveryProberTakesTheAddressesPortsAndHostsItNeeds(t *testing.T) {
	text := `version: "1"
probers:
  dns:
    interval: 1m
    timeout: 3s
  http:
    interval: 1m
    timeout: 5s
    ports: [443]
    path: /healthz
    status_codes: [200, 204]
  tcp:
    interval: 1m
    timeout: 3s
    ports: [22, 5432]
probe_targets:
  mock-resolver:
    addresses: [192.0.2.53]
    probers: [dns]
    serves: [mock-a.example]
  mock-web:
    addresses: [192.0.2.10]
    probers: [http, tcp]
    serves: [mock-a.example]
`
	want := []string{
		"mock-resolver|dns|192.0.2.53|0|mock-a.example",
		"mock-web|http|192.0.2.10|443|mock-a.example",
		"mock-web|tcp|192.0.2.10|22|",
		"mock-web|tcp|192.0.2.10|5432|",
	}
	configuration := parsed(t, text)
	if got := runPlaces(configuration); !reflect.DeepEqual(got, want) {
		t.Errorf("runs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if http := configuration.Probers[HTTPProber]; http.Path != "/healthz" || !reflect.DeepEqual(http.StatusCodes, []int{200, 204}) {
		t.Errorf("http = %+v", http)
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
		"empty":           {"", "the configuration is empty", mockSource + ":1:1"},
		"not a mapping":   {"- mock\n", "a configuration is a mapping of its fields", mockSource + ":1:1"},
		"unknown field":   {mockConfiguration + "mock_field: 1\n", "`mock_field` is not defined in the schema", mockSource + ":20:1"},
		"unknown prober":  {changed(t, "  ping:", "  mock:"), "`mock` is not defined in the schema", mockSource + ":10:3"},
		"no ports":        {changed(t, "    ports: [443, 8443]\n", ""), "`ports` is required", mockSource + ":3:3"},
		"port too high":   {changed(t, "[443, 8443]", "[443, 70000]"), "`ports[1]` must be <= `65535`", mockSource + ":6:18"},
		"unknown checker": {changed(t, "[certificate, ping]", "[certificate, mock]"), "`probers[1]` must be one of", mockSource + ":16:28"},
		"no serves":       {changed(t, "    serves: [mock-a.example, mock-b.example]\n", ""), "`serves` is required", mockSource + ":14:3"},
		"no addresses":    {changed(t, "    addresses: [192.0.2.10, 192.0.2.11]\n", ""), "`addresses` is required", mockSource + ":14:3"},
		"addresses on a domain": {changed(t, "    probers: [domain_registration]", "    probers: [domain_registration]\n    addresses: [192.0.2.10]"),
			"`addresses` matches a form that is not allowed", mockSource + ":20:16"},
		"address with a port": {changed(t, "[192.0.2.10, 192.0.2.11]", "[192.0.2.10:443, 192.0.2.11]"),
			"`addresses[0]` must match pattern", mockSource + ":15:17"},
		"wildcard host": {changed(t, "[mock-a.example, mock-b.example]", `["*.mock.example", mock-b.example]`),
			"`serves[0]` must match pattern", mockSource + ":17:14"},
		"prober without settings": {changed(t, "  ping:\n    interval: 1m\n    timeout: 3s\n", ""),
			"`ping` has no settings under `probers`", mockSource + ":13:28"},
		"domain registration of a name that is no domain": {changed(t, "  mock-domain.example:", "  mock-domain:"),
			"`mock-domain` is checked by `domain_registration`, so it is named by its domain", mockSource + ":18:3"},
		"zero timeout":  {changed(t, "timeout: 10s", "timeout: 0s"), "`timeout` must be above zero", mockSource + ":9:14"},
		"zero interval": {changed(t, "interval: 6h", "interval: 0s"), "`interval` must be above zero", mockSource + ":8:15"},
	} {
		_, err := Parse(mockSource, []byte(testCase.text))
		if !reportsProblem(err, testCase.message, testCase.location) {
			t.Errorf("%s: err = %v\nwant %q at %s", name, err, testCase.message, testCase.location)
		}
	}
}

func TestAFieldNoProberOfTheTargetUsesCarriesWhyAsItsSchemaNote(t *testing.T) {
	_, err := Parse(mockSource, []byte(changed(t, "    probers: [domain_registration]", "    probers: [domain_registration]\n    serves: [mock-a.example]")))
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
	if configuration, err := LoadFile(path); err != nil || len(configuration.ProbeTargets) != 2 {
		t.Errorf("configuration = %+v (%v)", configuration, err)
	}
	os.WriteFile(path, []byte(changed(t, "interval: 6h", "interval: 0s")), 0o600)
	if _, err := LoadFile(path); !reportsProblem(err, "`interval` must be above zero", path+":8:15") {
		t.Errorf("err = %v", err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "mock-missing.yml")); err == nil ||
		!strings.Contains(err.Error(), "failed to read the configuration file") {
		t.Errorf("a missing file: err = %v", err)
	}
}
