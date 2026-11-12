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

	"github.com/litebitus/probe-exporter/proberesults"
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
probes:
  mock_tls:
    prober: tcp
    timeout: 5s
    interval: 1m
    tcp:
      tls: true
    target_groups:
      mock-cdn:
        targets:
          - name: mock-a.example
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
max_concurrent_probes: 2
`

func TestEveryBlackboxSettingTheExporterReadsIsValid(t *testing.T) {
	schema := newestSchema(t)
	text := strings.Replace(mockConfiguration, "      tls: true\n", `      tls: true
      preferred_ip_protocol: ip4
      tls_config:
        insecure_skip_verify: false
        min_version: TLS12
      query_response:
        - expect: "^220"
          labels:
            - name: mock_label
              value: mock
        - send: "QUIT"
`, 1)
	text = strings.Replace(text, "max_concurrent_probes: 2\n", `  mock_web:
    prober: http
    timeout: 5s
    interval: 1m
    http:
      method: GET
      valid_status_codes: [200, 204]
      headers:
        X-Mock: mock
      body_size_limit: 10MB
      follow_redirects: false
      basic_auth:
        username: mock-user
        password: mock-password
      fail_if_body_not_matches_regexp: ["mock"]
    target_groups:
      mock-sites:
        targets:
          - name: mock-a.example
            addresses: [https://mock-a.example/healthz]
  mock_lookup:
    prober: dns
    timeout: 3s
    interval: 1m
    dns:
      transport_protocol: udp
      query_type: A
      valid_rcodes: [NOERROR]
      validate_answer_rrs:
        fail_if_not_matches_regexp: [".*"]
    target_groups:
      mock-resolvers:
        targets:
          - name: mock-name.example
            addresses: [192.0.2.53]
max_concurrent_probes: 2
`, 1)
	if err := schema.Validate(yamlInstance(t, text)); err != nil {
		t.Errorf("valid blackbox settings are refused: %v", err)
	}
}

// A group's labels cannot name a label the exporter sets on every series.
func TestTheSchemaReservesTheLabelsTheExporterSets(t *testing.T) {
	schema := newestSchema(t)
	for _, label := range proberesults.IdentityLabels {
		text := strings.Replace(mockConfiguration, "mock_site: mock-cdn", label+": mock-cdn", 1)
		if err := schema.Validate(yamlInstance(t, text)); err == nil {
			t.Errorf("the schema admits label %s", label)
		}
	}
}

// The schema refuses what the exporter refuses that a schema can state.
func TestTheSchemaRefusesWhatTheExporterRefuses(t *testing.T) {
	schema := newestSchema(t)
	if err := schema.Validate(yamlInstance(t, mockConfiguration)); err != nil {
		t.Fatalf("the mock configuration is invalid: %v", err)
	}
	for name, change := range map[string][2]string{
		"no version":                {`version: "1"` + "\n", ""},
		"unknown version":           {`version: "1"`, `version: "2"`},
		"unknown prober":            {"prober: tcp", "prober: mock"},
		"no timeout":                {"    timeout: 10s\n", ""},
		"rdap settings":             {"prober: rdap", "prober: rdap\n    tcp: {}"},
		"another prober's settings": {"    tcp:\n", "    http: {}\n    tcp:\n"},
		"duration":                  {"interval: 1m", "interval: 1d"},
		"no interval":               {"    interval: 6h\n", ""},
		"no target groups": {"    target_groups:\n      mock-domains:\n        targets:\n          - name: mock-domain.example\n            addresses: [mock-domain.example]\n",
			"    target_groups: {}\n"},
		"tcp address without a port":     {"addresses: [192.0.2.10:443]", "addresses: [192.0.2.10]"},
		"rdap address as a URL":          {"addresses: [mock-domain.example]", "addresses: [https://mock-domain.example]"},
		"wildcard name":                  {"name: mock-a.example", `name: "*.mock.example"`},
		"no addresses":                   {"addresses: [192.0.2.10:443]", "addresses: []"},
		"repeated address":               {"addresses: [192.0.2.10:443]", "addresses: [192.0.2.10:443, 192.0.2.10:443]"},
		"a server name in the settings":  {"      tls: true", "      tls: true\n      tls_config:\n        server_name: mock-a.example"},
		"a name field from the old form": {"        labels:\n", "        hostnames: [mock-a.example]\n        labels:\n"},
		"unknown group field":            {"        labels:\n", "        mock_field: 1\n        labels:\n"},
		"no targets": {"        targets:\n          - name: mock-a.example\n            addresses: [192.0.2.10:443]\n",
			"        targets: []\n"},
		"reserved label":              {"mock_site: mock-cdn", "address: mock-cdn"},
		"label name":                  {"mock_site: mock-cdn", "mock-site: mock-cdn"},
		"double underscore":           {"mock_site: mock-cdn", "__mock: mock-cdn"},
		"no concurrency":              {"max_concurrent_probes: 2", "max_concurrent_probes: 0"},
		"unknown field":               {"max_concurrent_probes: 2", "max_concurrent_probes: 2\nmock_field: 1"},
		"misspelled blackbox setting": {"      tls: true", "      tlss: true"},
		"mistyped blackbox setting":   {"      tls: true", "      tls: mock"},
		"unknown ip protocol":         {"      tls: true", "      preferred_ip_protocol: ip5"},
		"nested blackbox setting":     {"      tls: true", "      tls_config:\n        mock_setting: 1"},
		"unknown TLS version":         {"      tls: true", "      tls_config:\n        min_version: TLS09"},
		"mistyped header match":       {"prober: tcp", "prober: http\n    http:\n      method: GET\n      fail_if_header_matches:\n        - header: 1"},
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
