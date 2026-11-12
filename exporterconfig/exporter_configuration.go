// Package exporterconfig reads the exporter's configuration file: it holds the file to the schema of the version it states before reading anything else, then to the rules a schema cannot state, and reports every problem at its line and column.
package exporterconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	"go.yaml.in/yaml/v3"

	"github.com/litebitus/probe-exporter/schemas"
)

// RDAPProber is the prober the exporter adds to blackbox_exporter's.
const RDAPProber = "rdap"

// Configuration is a loaded configuration file, and the minor version of the format it is read as.
type Configuration struct {
	Version             string
	Probes              map[string]Probe
	MaxConcurrentProbes int
}

// Probe is a prober with its settings, how often it runs and its targets; a blackbox prober's settings in blackbox_exporter's module format, and the prober and its settings as written.
type Probe struct {
	Prober          string
	Timeout         time.Duration
	Interval        time.Duration
	TargetGroups    map[string]TargetGroup
	BlackboxModule  blackboxconfig.Module
	WrittenSettings string
}

// TargetGroup is targets and the labels they share.
type TargetGroup struct {
	Targets []Target
	Labels  map[string]string
}

// Target is a name and the addresses it is checked at, one run per address.
type Target struct {
	Name      string   `yaml:"name"`
	Addresses []string `yaml:"addresses"`
}

type configurationFile struct {
	Version             string               `yaml:"version"`
	Probes              map[string]yaml.Node `yaml:"probes"`
	MaxConcurrentProbes int                  `yaml:"max_concurrent_probes"`
}

// probeFieldsEntry is what a probe states beside its prober's settings: its prober, how often it runs and its targets.
type probeFieldsEntry struct {
	Prober       string                      `yaml:"prober"`
	Interval     time.Duration               `yaml:"interval"`
	TargetGroups map[string]targetGroupEntry `yaml:"target_groups"`
}

type targetGroupEntry struct {
	Targets []Target          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

type rdapSettingsEntry struct {
	Prober  string        `yaml:"prober"`
	Timeout time.Duration `yaml:"timeout"`
}

// configurationSchemas are the schemas the exporter carries, each loaded once.
var configurationSchemas = yamldslspec.NewSchemaCache()

// LoadFile reads and checks the configuration file at a path.
func LoadFile(path string) (Configuration, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return Configuration{}, fmt.Errorf("failed to read the configuration file: %w", err)
	}
	return Parse(path, text)
}

// Parse reads and checks a configuration's text, named in its problems as sourceName: first against the schema of the version it states, then against the rules a schema cannot state, answering every problem found.
func Parse(sourceName string, text []byte) (Configuration, error) {
	report := problemReport{sourceName: sourceName, text: string(text)}
	root, readProblem := yamldslspec.ParseYAML(sourceName, string(text), nil)
	if readProblem != nil {
		return Configuration{}, report.of([]yamldslspec.Diagnostic{*readProblem})
	}
	if root.Kind == yamldslspec.NodeNull {
		return Configuration{}, report.of([]yamldslspec.Diagnostic{yamldslspec.DiagnosticAt(root.Position, "the configuration is empty")})
	}
	minorVersion, shapeProblems, err := validateShape(root)
	if err != nil {
		return Configuration{}, err
	}
	if len(shapeProblems) > 0 {
		return Configuration{}, report.of(shapeProblems)
	}
	var file configurationFile
	if err := strictDecode(text, &file); err != nil {
		return Configuration{}, report.of([]yamldslspec.Diagnostic{yamldslspec.UnplacedDiagnostic(err.Error())})
	}
	configuration := Configuration{Version: minorVersion, Probes: map[string]Probe{}, MaxConcurrentProbes: file.MaxConcurrentProbes}
	var problems []yamldslspec.Diagnostic
	for name, node := range file.Probes {
		probe, err := decodeProbe(&node)
		if err != nil {
			problems = append(problems, placedAtKey(root, fmt.Sprintf("probe `%s`: %v", name, err), "probes", name))
			continue
		}
		configuration.Probes[name] = probe
	}
	problems = append(problems, checkConfiguration(configuration, root)...)
	if len(problems) > 0 {
		return Configuration{}, report.of(problems)
	}
	return configuration, nil
}

// validateShape holds a configuration to the schema its version names, answering the minor version it is read as and every problem; an error where that schema does not load.
func validateShape(root yamldslspec.Node) (string, []yamldslspec.Diagnostic, error) {
	if root.Kind != yamldslspec.NodeMap {
		return "", []yamldslspec.Diagnostic{yamldslspec.DiagnosticAt(root.Position, "a configuration is a mapping of its fields")}, nil
	}
	versionNode, isWritten := root.Get("version")
	stated, isText := versionNode.AsString()
	if !isText {
		versionPosition := root.Position
		if isWritten {
			versionPosition = versionNode.Position
		}
		return "", []yamldslspec.Diagnostic{
			yamldslspec.DiagnosticAt(versionPosition, "`version` is required, quoted, one of "+statedVersionsList()),
		}, nil
	}
	schemaText, minorVersion, err := schemas.SchemaFor(stated)
	if err != nil {
		return "", []yamldslspec.Diagnostic{
			yamldslspec.DiagnosticAt(versionNode.Position, fmt.Sprintf("`version` `%s` is not a version this exporter reads; it reads %s",
				strconv.Quote(stated), statedVersionsList())),
		}, nil
	}
	loadedSchema, err := configurationSchemas.Load("probe-exporter-v"+minorVersion+".schema.json", string(schemaText))
	if err != nil {
		return "", nil, fmt.Errorf("the schema of version %s cannot be read: %w", minorVersion, err)
	}
	return minorVersion, loadedSchema.Validate(yamldslspec.ResolvedValueFromNode(root), yamldslspec.ValidationRules{}), nil
}

func statedVersionsList() string {
	return quotedAlternatives(schemas.StatedVersions())
}

// quotedAlternatives are values written as alternatives, each quoted and fenced: `"a"`, `"b"` or `"c"`.
func quotedAlternatives(values []string) string {
	var quoted []string
	for _, value := range values {
		quoted = append(quoted, "`"+strconv.Quote(value)+"`")
	}
	if len(quoted) <= 1 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// decodeProbe reads a probe: its interval and targets, then its prober and settings, a blackbox prober's as blackbox_exporter reads a module.
func decodeProbe(probeNode *yaml.Node) (Probe, error) {
	var written probeFieldsEntry
	if err := probeNode.Decode(&written); err != nil {
		return Probe{}, err
	}
	node := withoutKeys(probeNode, "interval", "target_groups")
	text, err := yaml.Marshal(node)
	if err != nil {
		return Probe{}, err
	}
	probe := Probe{
		Prober:          written.Prober,
		Interval:        written.Interval,
		TargetGroups:    map[string]TargetGroup{},
		WrittenSettings: string(text),
	}
	for name, group := range written.TargetGroups {
		probe.TargetGroups[name] = TargetGroup(group)
	}
	if written.Prober == RDAPProber {
		var rdapSettings rdapSettingsEntry
		if err := strictDecode(text, &rdapSettings); err != nil {
			return Probe{}, err
		}
		probe.Timeout = rdapSettings.Timeout
		return probe, nil
	}
	decodedText := text
	if written.Prober == "dns" {
		decodedText, err = withStandInQueryName(node)
		if err != nil {
			return Probe{}, err
		}
	}
	if err := strictDecode(decodedText, &probe.BlackboxModule); err != nil {
		return Probe{}, err
	}
	if written.Prober == "dns" {
		probe.BlackboxModule.DNS.QueryName = ""
	}
	probe.Timeout = probe.BlackboxModule.Timeout
	return probe, nil
}

// withoutKeys is a mapping node without the entries of some keys, the node itself left as it is.
func withoutKeys(mapping *yaml.Node, keys ...string) *yaml.Node {
	kept := *mapping
	kept.Content = nil
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if !slices.Contains(keys, mapping.Content[index].Value) {
			kept.Content = append(kept.Content, mapping.Content[index], mapping.Content[index+1])
		}
	}
	return &kept
}

// standInQueryName passes blackbox's required `query_name` check for a dns probe, whose targets each state the name asked.
const standInQueryName = "query-name.invalid"

// withStandInQueryName is a dns probe's settings with the stand-in query name, the schema having refused one stated.
func withStandInQueryName(node *yaml.Node) ([]byte, error) {
	var settings map[string]any
	if err := node.Decode(&settings); err != nil {
		return nil, err
	}
	dnsSettings, _ := settings["dns"].(map[string]any)
	if dnsSettings == nil {
		dnsSettings = map[string]any{}
	}
	dnsSettings["query_name"] = standInQueryName
	settings["dns"] = dnsSettings
	return yaml.Marshal(settings)
}

func strictDecode(text []byte, into any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	err := decoder.Decode(into)
	if err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
	}
	return nil
}
