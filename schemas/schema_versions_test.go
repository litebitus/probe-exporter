package schemas

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func TestAConfigurationStatesAMajorOrAMinor(t *testing.T) {
	if got := StatedVersions(); !slices.Equal(got, []string{"1", "1.0"}) {
		t.Errorf("stated versions = %v", got)
	}
}

func TestAMajorFloatsToItsNewestMinorAndAMinorNamesItself(t *testing.T) {
	newest := minorSchemas[len(minorSchemas)-1]
	for stated, want := range map[string]string{strconv.Itoa(newest.major): newest.version(), "1.0": "1.0"} {
		text, got, err := SchemaFor(stated)
		if err != nil || got != want || !strings.Contains(string(text), `"$id": "https://github.com/litebitus/probe-exporter/schemas/probe-exporter-v`+want) {
			t.Errorf("%s reads as %s (%v), want %s", stated, got, err, want)
		}
	}
	if _, _, err := SchemaFor("0.9"); err == nil || err.Error() != `"0.9" is not a version this exporter reads` {
		t.Errorf("an unknown version: err = %v", err)
	}
}

// Every minor's file states its own version and its major's as what a configuration may write, and is identified by its own name.
func TestEveryMinorSchemaIsIdentifiedByItsVersion(t *testing.T) {
	for _, schema := range minorSchemas {
		text, err := schemaFiles.ReadFile(schema.fileName)
		if err != nil {
			t.Fatal(err)
		}
		var read struct {
			ID         string `json:"$id"`
			Properties struct {
				Version struct {
					Enum []string `json:"enum"`
				} `json:"version"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(text, &read); err != nil {
			t.Fatalf("%s: %v", schema.fileName, err)
		}
		want := []string{strconv.Itoa(schema.major), schema.version()}
		if read.ID != "https://github.com/litebitus/probe-exporter/schemas/"+schema.fileName || !slices.Equal(read.Properties.Version.Enum, want) {
			t.Errorf("%s: $id %s, version %v, want %v", schema.fileName, read.ID, read.Properties.Version.Enum, want)
		}
	}
}

func TestAReleaseAttachesEveryMinorAndEachMajorAsItsNewestMinor(t *testing.T) {
	assets, err := ReleaseAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range minorSchemas {
		if _, attached := assets[schema.fileName]; !attached {
			t.Errorf("%s is not attached; assets %v", schema.fileName, slices.Sorted(maps.Keys(assets)))
		}
	}
	newest := minorSchemas[len(minorSchemas)-1]
	major := "probe-exporter-v" + strconv.Itoa(newest.major) + ".schema.json"
	if string(assets[major]) != string(assets[newest.fileName]) {
		t.Errorf("%s is not %s", major, newest.fileName)
	}
}

func TestMinorsAreOrderedOldestFirstAndAMisnamedFileIsRefused(t *testing.T) {
	read, err := readMinorSchemas(fstest.MapFS{
		"probe-exporter-v2.0.schema.json":  {},
		"probe-exporter-v1.10.schema.json": {},
		"probe-exporter-v1.2.schema.json":  {},
	})
	var versions []string
	for _, schema := range read {
		versions = append(versions, schema.version())
	}
	if err != nil || strings.Join(versions, ",") != "1.2,1.10,2.0" {
		t.Errorf("versions = %v (%v)", versions, err)
	}
	if _, err := readMinorSchemas(fstest.MapFS{"mock.json": {}}); err == nil {
		t.Error("a misnamed schema file was read")
	}
	if _, err := readMinorSchemas(fstest.MapFS{"mock": {Mode: 0o755 | os.ModeDir}, "mock/probe-exporter-v1.0.schema.json": {}}); err == nil {
		t.Error("a folder was read as a schema")
	}
}

func newestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	newest := minorSchemas[len(minorSchemas)-1]
	text, _ := schemaFiles.ReadFile(newest.fileName)
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(text)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(newest.fileName, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(newest.fileName)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func yamlInstance(t *testing.T, text string) any {
	t.Helper()
	var instance any
	if err := yaml.Unmarshal([]byte(text), &instance); err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestEveryExampleConfigurationIsValidAgainstTheNewestSchema(t *testing.T) {
	schema := newestSchema(t)
	examples, _ := filepath.Glob(filepath.Join("..", "examples", "*.yml"))
	if len(examples) == 0 {
		t.Fatal("no examples")
	}
	for _, example := range examples {
		text, err := os.ReadFile(example)
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(yamlInstance(t, string(text))); err != nil {
			t.Errorf("%s: %v", example, err)
		}
	}
}

const mockConfiguration = `version: "1"
probers:
  certificate:
    interval: 5m
    timeout: 5s
    ports: [443]
  dns:
    interval: 1m
    timeout: 3s
  domain_registration:
    interval: 6h
    timeout: 10s
  http:
    interval: 1m
    timeout: 5s
    ports: [443]
    path: /healthz
    status_codes: [200]
  ping:
    interval: 1m
    timeout: 3s
  tcp:
    interval: 1m
    timeout: 3s
    ports: [5432]
probe_targets:
  mock-cdn:
    addresses: [192.0.2.10, mock-cdn.example.net]
    probers: [certificate, http]
    serves: [mock-a.example]
  mock-database:
    addresses: [192.0.2.20]
    probers: [ping, tcp]
  mock-resolver:
    addresses: ["2001:db8::53"]
    probers: [dns]
    serves: [mock-a.example]
  mock-domain.example:
    probers: [domain_registration]
`

// The schema refuses what the exporter refuses that a schema can state.
func TestTheSchemaRefusesWhatTheExporterRefuses(t *testing.T) {
	schema := newestSchema(t)
	if err := schema.Validate(yamlInstance(t, mockConfiguration)); err != nil {
		t.Fatalf("the mock configuration is invalid: %v", err)
	}
	for name, change := range map[string][2]string{
		"no version":                  {`version: "1"` + "\n", ""},
		"unknown version":             {`version: "1"`, `version: "2"`},
		"unknown field":               {"probe_targets:\n", "mock_field: 1\nprobe_targets:\n"},
		"unknown prober settings":     {"  ping:\n", "  mock:\n"},
		"unknown prober setting":      {"    path: /healthz\n", "    path: /healthz\n    mock_setting: 1\n"},
		"no timeout":                  {"    timeout: 10s\n", ""},
		"duration":                    {"interval: 6h", "interval: 1d"},
		"no ports":                    {"    ports: [5432]\n", ""},
		"no port":                     {"ports: [5432]", "ports: []"},
		"port zero":                   {"ports: [5432]", "ports: [0]"},
		"port too high":               {"ports: [5432]", "ports: [70000]"},
		"repeated port":               {"ports: [5432]", "ports: [5432, 5432]"},
		"path without a slash":        {"path: /healthz", "path: healthz"},
		"no status codes":             {"    status_codes: [200]\n", ""},
		"status code too high":        {"status_codes: [200]", "status_codes: [600]"},
		"no targets":                  {"probe_targets:\n", "probe_targets: {}\nmock_targets:\n"},
		"target name with a space":    {"  mock-cdn:", "  mock cdn:"},
		"no probers":                  {"    probers: [ping, tcp]\n", ""},
		"empty probers":               {"probers: [ping, tcp]", "probers: []"},
		"unknown prober":              {"probers: [ping, tcp]", "probers: [ping, mock]"},
		"repeated prober":             {"probers: [ping, tcp]", "probers: [ping, ping]"},
		"certificate without serves":  {"    serves: [mock-a.example]\n  mock-database:", "  mock-database:"},
		"ping without addresses":      {"    addresses: [192.0.2.20]\n", ""},
		"addresses on a domain":       {"    probers: [domain_registration]\n", "    probers: [domain_registration]\n    addresses: [192.0.2.10]\n"},
		"serves on a domain":          {"    probers: [domain_registration]\n", "    probers: [domain_registration]\n    serves: [mock-a.example]\n"},
		"serves on a ping":            {"    probers: [ping, tcp]\n", "    probers: [ping, tcp]\n    serves: [mock-a.example]\n"},
		"address with a port":         {"addresses: [192.0.2.20]", "addresses: [192.0.2.20:5432]"},
		"address as a URL":            {"addresses: [192.0.2.20]", "addresses: [https://192.0.2.20]"},
		"repeated address":            {"addresses: [192.0.2.20]", "addresses: [192.0.2.20, 192.0.2.20]"},
		"wildcard host":               {"serves: [mock-a.example]\n  mock-database:", "serves: [\"*.mock.example\"]\n  mock-database:"},
		"unknown target field":        {"    probers: [domain_registration]\n", "    probers: [domain_registration]\n    mock_field: 1\n"},
		"a field of the earlier form": {"  certificate:\n", "  certificate:\n    prober: tcp\n"},
	} {
		if !strings.Contains(mockConfiguration, change[0]) {
			t.Fatalf("%s: the mock configuration holds no %q", name, change[0])
		}
		changed := strings.Replace(mockConfiguration, change[0], change[1], 1)
		if err := schema.Validate(yamlInstance(t, changed)); err == nil {
			t.Errorf("%s: the schema admits it", name)
		}
	}
}
