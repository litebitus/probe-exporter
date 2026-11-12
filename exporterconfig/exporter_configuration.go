// Package exporterconfig reads the exporter's configuration file: it holds the file to the schema of the version it states before reading anything else, then to the rules a schema cannot state, and reports every problem at its line and column.
package exporterconfig

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
	"go.yaml.in/yaml/v3"

	"github.com/litebitus/probe-exporter/schemas"
)

// The probers a configuration names.
const (
	CertificateProber        = "certificate"
	DNSProber                = "dns"
	DomainRegistrationProber = "domain_registration"
	HTTPProber               = "http"
	PingProber               = "ping"
	TCPProber                = "tcp"
)

// Configuration is a loaded configuration file, and the minor version of the format it is read as.
type Configuration struct {
	Version      string
	Probers      map[string]ProberSettings
	ProbeTargets map[string]ProbeTarget
}

// ProberSettings are a prober's settings as written, once for every target the prober checks and again by a target to override them, each unset where not written.
type ProberSettings struct {
	Interval    *time.Duration `yaml:"interval"`
	Timeout     *time.Duration `yaml:"timeout"`
	Ports       []Port         `yaml:"ports"`
	Path        string         `yaml:"path"`
	StatusCodes []int          `yaml:"status_codes"`
	RecordTypes []string       `yaml:"record_types"`
}

// ProbeTarget is what is probed: where it is reached, the hosts it serves, and the probers that check it with the settings it overrides.
type ProbeTarget struct {
	Addresses []string                  `yaml:"addresses"`
	Probers   map[string]ProberSettings `yaml:"probers"`
	Serves    []string                  `yaml:"serves"`
}

// Port is a port and the protocol spoken on it, written `443/tcp`; for ICMP, the message type takes the port's place.
type Port struct {
	Number   int
	Protocol string
}

// UnmarshalYAML reads a port written `<number>/<protocol>`.
func (port *Port) UnmarshalYAML(node *yaml.Node) error {
	number, protocol, isPaired := strings.Cut(node.Value, "/")
	parsed, err := strconv.Atoi(number)
	if !isPaired || err != nil || protocol == "" {
		return fmt.Errorf("`%s` is not a port and its protocol, such as `443/tcp`", node.Value)
	}
	*port = Port{Number: parsed, Protocol: protocol}
	return nil
}

func (port Port) String() string {
	return strconv.Itoa(port.Number) + "/" + port.Protocol
}

// CheckSettings are the settings one prober checks one target with: the prober's own, each overridden where the target states it.
type CheckSettings struct {
	Interval    time.Duration
	Timeout     time.Duration
	Ports       []Port
	Path        string
	StatusCodes []int
	RecordTypes []string
}

// CheckSettings are the settings a prober checks a target with.
func (configuration Configuration) CheckSettings(targetName, proberName string) CheckSettings {
	return resolved(configuration.Probers[proberName], configuration.ProbeTargets[targetName].Probers[proberName])
}

func resolved(prober, target ProberSettings) CheckSettings {
	return CheckSettings{
		Interval:    durationOf(cmp.Or(target.Interval, prober.Interval)),
		Timeout:     durationOf(cmp.Or(target.Timeout, prober.Timeout)),
		Ports:       writtenOr(target.Ports, prober.Ports),
		Path:        cmp.Or(target.Path, prober.Path),
		StatusCodes: writtenOr(target.StatusCodes, prober.StatusCodes),
		RecordTypes: writtenOr(target.RecordTypes, prober.RecordTypes),
	}
}

func durationOf(written *time.Duration) time.Duration {
	if written == nil {
		return 0
	}
	return *written
}

// writtenOr is the stated list where it is written, else the other.
func writtenOr[Value any](stated, otherwise []Value) []Value {
	if stated != nil {
		return stated
	}
	return otherwise
}

type configurationFile struct {
	Version      string                    `yaml:"version"`
	Probers      map[string]ProberSettings `yaml:"probers"`
	ProbeTargets map[string]ProbeTarget    `yaml:"probe_targets"`
}

// configurationSchemas are the schemas the exporter carries, each loaded once.
var configurationSchemas = yamldslspec.NewSchemaCache()

// configurationSizeLimit is the largest configuration file the exporter reads.
const configurationSizeLimit = 1 << 20

// LoadFile reads and checks the configuration file at a path, refusing one larger than 1 MiB.
func LoadFile(path string) (Configuration, error) {
	file, err := os.Open(path)
	if err != nil {
		return Configuration{}, fmt.Errorf("failed to read the configuration file: %w", err)
	}
	defer file.Close()
	text, err := io.ReadAll(io.LimitReader(file, configurationSizeLimit+1))
	if err != nil {
		return Configuration{}, fmt.Errorf("failed to read the configuration file: %w", err)
	}
	if len(text) > configurationSizeLimit {
		return Configuration{}, fmt.Errorf("the configuration file %s is larger than 1 MiB", path)
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
	configuration := Configuration{Version: minorVersion, Probers: file.Probers, ProbeTargets: file.ProbeTargets}
	if problems := checkConfiguration(configuration, root); len(problems) > 0 {
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

func strictDecode(text []byte, into any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	err := decoder.Decode(into)
	if err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
	}
	return nil
}
