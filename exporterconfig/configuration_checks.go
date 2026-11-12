package exporterconfig

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
)

var domainNamePattern = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.?$`)

// checkConfiguration holds a configuration the schema admits to the rules a schema cannot state, placing each problem in the file: a prober's `interval` and `timeout` above zero, every prober a target names given its settings, and a target checked by `domain_registration` named by its domain.
func checkConfiguration(configuration Configuration, root yamldslspec.Node) []yamldslspec.Diagnostic {
	var problems []yamldslspec.Diagnostic
	for _, proberName := range slices.Sorted(maps.Keys(configuration.Probers)) {
		settings := configuration.Probers[proberName]
		if settings.Interval <= 0 {
			problems = append(problems, placedAtValue(root, "`interval` must be above zero", "probers", proberName, "interval"))
		}
		if settings.Timeout <= 0 {
			problems = append(problems, placedAtValue(root, "`timeout` must be above zero", "probers", proberName, "timeout"))
		}
	}
	for _, targetName := range slices.Sorted(maps.Keys(configuration.ProbeTargets)) {
		for proberIndex, proberName := range configuration.ProbeTargets[targetName].Probers {
			if _, isSet := configuration.Probers[proberName]; !isSet {
				problems = append(problems, placedAtValue(root, fmt.Sprintf("`%s` has no settings under `probers`", proberName),
					"probe_targets", targetName, "probers", proberIndex))
			}
			if proberName == DomainRegistrationProber && !domainNamePattern.MatchString(targetName) {
				problems = append(problems, placedAtKey(root,
					fmt.Sprintf("`%s` is checked by `%s`, so it is named by its domain, such as `example.com`", targetName, proberName),
					"probe_targets", targetName))
			}
		}
	}
	return problems
}
