package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/litebitus/probe-exporter/schemas"
)

func committedSchema(t *testing.T) []byte {
	t.Helper()
	_, fileName := schemas.NewestMinor()
	committed, err := os.ReadFile(filepath.Join("..", "..", "schemas", fileName))
	if err != nil {
		t.Fatal(err)
	}
	return committed
}

// The committed schema of the newest minor already holds the blackbox definitions the generator fills, so a change to blackbox's settings shows as a diff.
func TestTheCommittedSchemaHoldsTheBlackboxDefinitionsTheGeneratorFills(t *testing.T) {
	committed := committedSchema(t)
	filled, err := filledSchema(committed)
	if err != nil {
		t.Fatal(err)
	}
	if string(filled) != string(committed) {
		_, fileName := schemas.NewestMinor()
		t.Errorf("schemas/%s differs from the generator's output; run `make schema`", fileName)
	}
}

// Every definition written by hand is kept where it stands; only the blackbox definitions are replaced, a stale one dropped.
func TestTheGeneratorKeepsEveryDefinitionWrittenByHand(t *testing.T) {
	written := `{"title":"mock","$defs":{"mock_first":{"type":"string","maximum":1.50},"blackbox_mock_stale":{},"mock_last":{"enum":["b","a"]}}}`
	filled, err := filledSchema([]byte(written))
	if err != nil {
		t.Fatal(err)
	}
	text := string(filled)
	first, last, stale := strings.Index(text, `"mock_first"`), strings.Index(text, `"mock_last"`), strings.Index(text, "blackbox_mock_stale")
	tcp := strings.Index(text, `"blackbox_tcp_probe"`)
	if first < 0 || last < first || stale >= 0 || tcp < last || !strings.Contains(text, `"maximum": 1.50`) ||
		!strings.Contains(text, "\"enum\": [\n        \"b\",\n        \"a\"\n      ]") {
		t.Errorf("filled =\n%s", text)
	}
}

func TestASchemaTheGeneratorCannotFillIsRefused(t *testing.T) {
	for written, want := range map[string]string{
		`{"title":"mock"}`:       "the schema has no `$defs` object",
		`["mock"]`:               "the schema is not a JSON object",
		`{"$defs":{}} {}`:        "text follows the JSON value",
		`{"$defs":{"mock":[1,}}`: "invalid character",
		`{"$defs":`:              "EOF",
	} {
		if _, err := filledSchema([]byte(written)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", written, err, want)
		}
	}
}

func TestTheGeneratorFillsTheNewestMinorInTheFolderGiven(t *testing.T) {
	folder := t.TempDir()
	_, fileName := schemas.NewestMinor()
	if err := os.WriteFile(filepath.Join(folder, fileName), committedSchema(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{folder}); err != nil {
		t.Fatal(err)
	}
	if filled, _ := os.ReadFile(filepath.Join(folder, fileName)); string(filled) != string(committedSchema(t)) {
		t.Error("the filled schema differs from the committed one")
	}
	if err := run(nil); err == nil || err.Error() != "usage: schemagen <schemas folder>" {
		t.Errorf("err = %v", err)
	}
	if err := run([]string{t.TempDir()}); err == nil {
		t.Error("a folder without the newest minor's schema was filled")
	}
	os.WriteFile(filepath.Join(folder, fileName), []byte("mock"), 0o600)
	if err := run([]string{folder}); err == nil || !strings.Contains(err.Error(), fileName) {
		t.Errorf("an unreadable schema: err = %v", err)
	}
}

const mockReference = "### `<mock_probe>`\n" +
	"```yml\n" +
	"# The mock protocol (ip4, ip6).\n" +
	"[ mock_protocol: <string> | default = \"ip6\" ]\n" +
	"[ mock_fallback: <boolean | default = true> ]\n" +
	"\n" +
	"# A mock list,\n" +
	"#\n" +
	"# over two lines.\n" +
	"mock_list:\n" +
	"  [ - [ mock_entry: <string> ],\n" +
	"\n" +
	"[ mock_transport: <string> | default: \"udp\" ] # udp, tcp\n" +
	"# A second description of the mock protocol.\n" +
	"[ mock_protocol: <string> ]\n" +
	"```\n" +
	"Prose outside the block: <ignored>\n" +
	"[ mock_outside: <string> ]\n"

func TestTheReferenceGivesEachFieldItsCommentAndDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CONFIGURATION.md")
	os.WriteFile(path, []byte(mockReference), 0o600)
	reference, err := readConfigurationReference(path)
	if err != nil {
		t.Fatal(err)
	}
	section := reference["mock_probe"]
	for field, want := range map[string]fieldReference{
		"mock_protocol":  {description: "The mock protocol (ip4, ip6).", defaultValue: "ip6"},
		"mock_fallback":  {defaultValue: "true"},
		"mock_list":      {description: "A mock list, over two lines."},
		"mock_entry":     {},
		"mock_transport": {description: "udp, tcp", defaultValue: "udp"},
	} {
		if section[field] != want {
			t.Errorf("%s = %+v, want %+v", field, section[field], want)
		}
	}
	if _, read := section["mock_outside"]; read {
		t.Error("a field outside the code block was read")
	}
	if _, err := readConfigurationReference(filepath.Join(t.TempDir(), "mock-missing.md")); err == nil {
		t.Error("a missing reference was read")
	}
}

func TestAFieldIsDescribedByTheFirstSectionThatDocumentsIt(t *testing.T) {
	reference := configurationReference{
		"mock_outer": {"mock_field": {description: "outer"}},
		"mock_inner": {"mock_field": {description: "inner"}},
	}
	if read, _ := reference.lookUp([]string{"mock_inner", "mock_outer"}, "mock_field"); read.description != "inner" {
		t.Errorf("read = %+v", read)
	}
	if _, documented := reference.lookUp([]string{"mock_outer"}, "mock_missing"); documented {
		t.Error("an undocumented field was documented")
	}
}

func TestADescriptionCarriesTheDefaultAndKeepsTheSchemasOwnKeys(t *testing.T) {
	described := describe(newObject("type", "boolean"), fieldReference{description: "Mock flag", defaultValue: "true"})
	encoded, _ := json.Marshal(described)
	if string(encoded) != `{"description":"Mock flag. `+"`true`"+` by default.","type":"boolean"}` {
		t.Errorf("described = %s", encoded)
	}
	reference := describe(newObject("$ref", "#/$defs/mock"), fieldReference{description: "Mock."})
	if encoded, _ := json.Marshal(reference); string(encoded) != `{"description":"Mock.","$ref":"#/$defs/mock"}` {
		t.Errorf("described reference = %s", encoded)
	}
	if undescribed := describe(newObject("type", "string"), fieldReference{}); len(undescribed.keys) != 1 {
		t.Errorf("an undocumented field gained keys: %v", undescribed.keys)
	}
}

func TestAnObjectKeepsItsKeysInOrderAndItsTextUnescaped(t *testing.T) {
	object := newObject("zebra", 1, "alpha", "<a & b>")
	object.set("zebra", 2)
	encoded, err := indentedJSON(object)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"zebra\": 2,\n  \"alpha\": \"<a & b>\"\n}\n"; string(encoded) != want {
		t.Errorf("encoded = %q, want %q", encoded, want)
	}
	if _, err := indentedJSON(newObject("mock", make(chan int))); err == nil {
		t.Error("an unencodable value was encoded")
	}
}

func TestDefinitionsAreNamedByTheirTypesWords(t *testing.T) {
	schema := committedSchema(t)
	for _, name := range []string{"blackbox_dns_probe", "blackbox_http_client_config", "blackbox_tls_config", "blackbox_oauth2", "blackbox_query_response"} {
		if !strings.Contains(string(schema), `"`+name+`": {`) {
			t.Errorf("no definition %s", name)
		}
	}
}
