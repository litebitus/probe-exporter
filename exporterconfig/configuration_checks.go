package exporterconfig

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// ReservedLabels are the labels the exporter puts on every series of a result, which a group's labels cannot name.
var ReservedLabels = []string{"hostname", "module", "query_name", "target", "target_group"}

var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type configurationCheck struct {
	configuration  Configuration
	writtenModules map[string]bool
	probeIdentity  map[string]string
	problems       []error
}

func (check *configurationCheck) refuse(format string, arguments ...any) {
	check.problems = append(check.problems, fmt.Errorf(format, arguments...))
}

func checkConfiguration(configuration Configuration, writtenModules map[string]bool) []error {
	check := &configurationCheck{configuration: configuration, writtenModules: writtenModules, probeIdentity: map[string]string{}}
	if configuration.MaxConcurrentProbes < 1 {
		check.refuse("`max_concurrent_probes` is required, at least 1")
	}
	if len(configuration.ProbeConfigs) == 0 {
		check.refuse("`probe_configs` is required, with at least one entry")
	}
	for position, probeConfig := range configuration.ProbeConfigs {
		check.probeConfig(fmt.Sprintf("`probe_configs[%d]`", position), probeConfig)
	}
	return check.problems
}

func (check *configurationCheck) probeConfig(at string, probeConfig ProbeConfig) {
	module, known := check.configuration.Modules[probeConfig.Module]
	switch {
	case probeConfig.Module == "":
		check.refuse("%s: `module` is required", at)
	case !known && !check.writtenModules[probeConfig.Module]:
		check.refuse("%s: `module` names `%s`, which `modules` does not define", at, probeConfig.Module)
	}
	if probeConfig.Interval <= 0 {
		check.refuse("%s: `interval` is required, above zero", at)
	}
	if len(probeConfig.TargetGroups) == 0 {
		check.refuse("%s: `target_groups` is required, with at least one group", at)
	}
	for _, name := range slices.Sorted(maps.Keys(probeConfig.TargetGroups)) {
		groupAt := fmt.Sprintf("%s: target group `%s`", at, name)
		group := probeConfig.TargetGroups[name]
		check.targetGroup(groupAt, group)
		if known {
			check.groupSettings(groupAt, module, group)
			check.probeIdentities(groupAt, groupProbeTargets(probeConfig, name))
		}
	}
}

func (check *configurationCheck) targetGroup(at string, group TargetGroup) {
	if len(group.Targets) == 0 {
		check.refuse("%s: `targets` is required, with at least one target", at)
	}
	check.distinctValues(at, "targets", group.Targets)
	check.distinctValues(at, "hostnames", group.Hostnames)
	check.distinctValues(at, "query_names", group.QueryNames)
	for _, name := range slices.Sorted(maps.Keys(group.Labels)) {
		switch {
		case !labelNamePattern.MatchString(name) || strings.HasPrefix(name, "__"):
			check.refuse("%s: label `%s` is not a label name: letters, digits and underscores, "+
				"beginning with a letter or an underscore, and not with two underscores", at, name)
		case slices.Contains(ReservedLabels, name):
			check.refuse("%s: label `%s` is one the exporter sets on every series", at, name)
		}
	}
}

func (check *configurationCheck) distinctValues(at, field string, values []string) {
	seen := map[string]bool{}
	for _, value := range values {
		switch {
		case value == "":
			check.refuse("%s: `%s` holds an empty value", at, field)
		case seen[value]:
			check.refuse("%s: `%s` holds `%s` twice", at, field, value)
		}
		seen[value] = true
	}
}

func (check *configurationCheck) groupSettings(at string, module Module, group TargetGroup) {
	if len(group.Hostnames) > 0 {
		switch module.Prober {
		case "http":
			for name := range module.BlackboxModule.HTTP.Headers {
				if strings.EqualFold(name, "Host") {
					check.refuse("%s: `hostnames` is set, and its module states a `Host` header", at)
				}
			}
		case "tcp":
			if module.BlackboxModule.TCP.TLSConfig.ServerName != "" {
				check.refuse("%s: `hostnames` is set, and its module states a TLS `server_name`", at)
			}
		default:
			check.refuse("%s: `hostnames` is for a module of the `http` or `tcp` prober, not `%s`", at, module.Prober)
		}
	}
	queryNameInModule := module.BlackboxModule.DNS.QueryName != ""
	switch {
	case len(group.QueryNames) > 0 && module.Prober != "dns":
		check.refuse("%s: `query_names` is for a module of the `dns` prober, not `%s`", at, module.Prober)
	case len(group.QueryNames) > 0 && queryNameInModule:
		check.refuse("%s: `query_names` is set, and its module states a `query_name`", at)
	case len(group.QueryNames) == 0 && module.Prober == "dns" && !queryNameInModule:
		check.refuse("%s: `query_names` is required, since its module states no `query_name`", at)
	}
}

func (check *configurationCheck) probeIdentities(at string, probeTargets []ProbeTarget) {
	for _, probeTarget := range probeTargets {
		identity := strings.Join([]string{
			probeTarget.Module, probeTarget.TargetGroup, probeTarget.Target, probeTarget.Hostname, probeTarget.QueryName,
		}, "\x00")
		if earlier, taken := check.probeIdentity[identity]; taken {
			check.refuse("%s: target `%s` is probed the same way in %s", at, probeTarget.Target, earlier)
			continue
		}
		check.probeIdentity[identity] = at
	}
}
