// Package exporterconfig reads the exporter's configuration file: it holds the file to the schema of the version it states before reading anything else, then to the rules a schema cannot state, and reports every problem at its line and column.
package exporterconfig

import (
	"bytes"
	"errors"
	"fmt"
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

// ProberSettings are a prober's settings: how often it checks each target and how long a check may take, and what the prober itself takes.
type ProberSettings struct {
	Interval    time.Duration `yaml:"interval"`
	Timeout     time.Duration `yaml:"timeout"`
	Ports       []int         `yaml:"ports"`
	Path        string        `yaml:"path"`
	StatusCodes []int         `yaml:"status_codes"`
}

// ProbeTarget is what is probed: the probers that check it, where it is reached, and the hosts it serves.
type ProbeTarget struct {
	Probers   []string `yaml:"probers"`
	Addresses []string `yaml:"addresses"`
	Serves    []string `yaml:"serves"`
}

type configurationFile struct {
	Version      string                    `yaml:"version"`
	Probers      map[string]ProberSettings `yaml:"probers"`
	ProbeTargets map[string]ProbeTarget    `yaml:"probe_targets"`
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
