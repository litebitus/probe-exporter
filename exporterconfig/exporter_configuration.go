// Package exporterconfig reads the exporter's configuration file: its modules, the targets each probes, and the bound on concurrent probes, refusing every value it cannot use.
package exporterconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	"go.yaml.in/yaml/v3"
)

// RDAPProber is the prober the exporter adds to blackbox_exporter's.
const RDAPProber = "rdap"

// Configuration is a loaded configuration file.
type Configuration struct {
	Modules             map[string]Module
	ProbeConfigs        []ProbeConfig
	MaxConcurrentProbes int
}

// Module is a prober with its settings, a blackbox prober's in blackbox_exporter's module format, and the module as written.
type Module struct {
	Prober         string
	Timeout        time.Duration
	BlackboxModule blackboxconfig.Module
	WrittenText    string
}

// ProbeConfig is a module, how often it probes, and its targets.
type ProbeConfig struct {
	Module       string
	Interval     time.Duration
	TargetGroups map[string]TargetGroup
}

// TargetGroup is targets and the settings they share.
type TargetGroup struct {
	Targets    []string
	Hostnames  []string
	QueryNames []string
	Labels     map[string]string
}

type configurationFile struct {
	Modules             map[string]yaml.Node `yaml:"modules"`
	ProbeConfigs        []probeConfigEntry   `yaml:"probe_configs"`
	MaxConcurrentProbes int                  `yaml:"max_concurrent_probes"`
}

type probeConfigEntry struct {
	Module       string                      `yaml:"module"`
	Interval     time.Duration               `yaml:"interval"`
	TargetGroups map[string]targetGroupEntry `yaml:"target_groups"`
}

type targetGroupEntry struct {
	Targets    []string          `yaml:"targets"`
	Hostnames  []string          `yaml:"hostnames"`
	QueryNames []string          `yaml:"query_names"`
	Labels     map[string]string `yaml:"labels"`
}

type rdapModuleEntry struct {
	Prober  string        `yaml:"prober"`
	Timeout time.Duration `yaml:"timeout"`
}

// LoadFile reads and checks the configuration file at a path.
func LoadFile(path string) (Configuration, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return Configuration{}, fmt.Errorf("failed to read the configuration file: %w", err)
	}
	return Parse(text)
}

// Parse reads and checks a configuration's text, answering every problem it holds.
func Parse(text []byte) (Configuration, error) {
	var file configurationFile
	if err := strictDecode(text, &file); errors.Is(err, io.EOF) {
		return Configuration{}, errors.New("the configuration is empty")
	} else if err != nil {
		return Configuration{}, fmt.Errorf("failed to parse the configuration: %w", err)
	}
	configuration := Configuration{
		Modules:             map[string]Module{},
		MaxConcurrentProbes: file.MaxConcurrentProbes,
	}
	var problems []error
	if len(file.Modules) == 0 {
		problems = append(problems, errors.New("`modules` is required, with at least one module"))
	}
	for name, node := range file.Modules {
		module, err := decodeModule(&node)
		if err != nil {
			problems = append(problems, fmt.Errorf("module `%s`: %w", name, err))
			continue
		}
		configuration.Modules[name] = module
	}
	for _, entry := range file.ProbeConfigs {
		probeConfig := ProbeConfig{Module: entry.Module, Interval: entry.Interval, TargetGroups: map[string]TargetGroup{}}
		for name, group := range entry.TargetGroups {
			probeConfig.TargetGroups[name] = TargetGroup(group)
		}
		configuration.ProbeConfigs = append(configuration.ProbeConfigs, probeConfig)
	}
	writtenModules := map[string]bool{}
	for name := range file.Modules {
		writtenModules[name] = true
	}
	problems = append(problems, checkConfiguration(configuration, writtenModules)...)
	if len(problems) > 0 {
		return Configuration{}, errors.Join(problems...)
	}
	return configuration, nil
}

func decodeModule(node *yaml.Node) (Module, error) {
	text, err := yaml.Marshal(node)
	if err != nil {
		return Module{}, err
	}
	var written struct {
		Prober string `yaml:"prober"`
	}
	if err := node.Decode(&written); err != nil {
		return Module{}, err
	}
	module := Module{Prober: written.Prober, WrittenText: string(text)}
	if written.Prober == RDAPProber {
		var rdapModule rdapModuleEntry
		if err := strictDecode(text, &rdapModule); err != nil {
			return Module{}, err
		}
		module.Timeout = rdapModule.Timeout
	} else {
		decodedText, standsIn := text, false
		if written.Prober == "dns" {
			decodedText, standsIn, err = withStandInQueryName(node)
			if err != nil {
				return Module{}, err
			}
		}
		if err := strictDecode(decodedText, &module.BlackboxModule); err != nil {
			return Module{}, err
		}
		if standsIn {
			module.BlackboxModule.DNS.QueryName = ""
		}
		module.Timeout = module.BlackboxModule.Timeout
	}
	if module.Timeout <= 0 {
		return Module{}, errors.New("`timeout` is required, above zero")
	}
	return module, nil
}

// standInQueryName passes blackbox's required `query_name` check for a dns module whose target groups state the names asked.
const standInQueryName = "query-name.invalid"

func withStandInQueryName(node *yaml.Node) ([]byte, bool, error) {
	var module map[string]any
	if err := node.Decode(&module); err != nil {
		return nil, false, err
	}
	dnsSettings, _ := module["dns"].(map[string]any)
	if dnsSettings == nil {
		dnsSettings = map[string]any{}
	}
	if _, stated := dnsSettings["query_name"]; stated {
		text, err := yaml.Marshal(node)
		return text, false, err
	}
	dnsSettings["query_name"] = standInQueryName
	module["dns"] = dnsSettings
	text, err := yaml.Marshal(module)
	return text, true, err
}

func strictDecode(text []byte, into any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	return decoder.Decode(into)
}
