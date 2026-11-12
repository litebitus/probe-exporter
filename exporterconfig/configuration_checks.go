package exporterconfig

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
)

var domainNamePattern = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?\.?$`)

// checkConfiguration holds a configuration the schema admits to the rules a schema cannot state, placing each problem in the file: every `interval` and `timeout` above zero, every prober a target names given its settings under `probers`, every setting a check needs stated by the prober or the target, no check's `timeout` longer than its `interval`, and a target checked by `domain_registration` named by its domain.
func checkConfiguration(configuration Configuration, root yamldslspec.Node) []yamldslspec.Diagnostic {
	var problems []yamldslspec.Diagnostic
	for _, proberName := range slices.Sorted(maps.Keys(configuration.Probers)) {
		problems = append(problems, timingProblems(configuration.Probers[proberName], root, "probers", proberName)...)
	}
	for _, targetName := range slices.Sorted(maps.Keys(configuration.ProbeTargets)) {
		target := configuration.ProbeTargets[targetName]
		for _, proberName := range slices.Sorted(maps.Keys(target.Probers)) {
			path := []any{"probe_targets", targetName, "probers", proberName}
			problems = append(problems, timingProblems(target.Probers[proberName], root, path...)...)
			if _, isSet := configuration.Probers[proberName]; !isSet {
				problems = append(problems, placedAtKey(root, fmt.Sprintf("`%s` has no settings under `probers`", proberName), path...))
				continue
			}
			settings := configuration.CheckSettings(targetName, proberName)
			for _, missing := range missingSettings(proberName, settings) {
				problems = append(problems, placedAtKey(root,
					fmt.Sprintf("`%s` checks `%s` without `%s`; state it here or under `probers`", proberName, targetName, missing), path...))
			}
			if settings.Timeout > settings.Interval {
				problems = append(problems, placedAtKey(root,
					fmt.Sprintf("`%s` checks `%s` with a `timeout` of %s, longer than its `interval` of %s",
						proberName, targetName, settings.Timeout, settings.Interval), path...))
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

func timingProblems(settings ProberSettings, root yamldslspec.Node, path ...any) []yamldslspec.Diagnostic {
	var problems []yamldslspec.Diagnostic
	for name, duration := range map[string]*time.Duration{"interval": settings.Interval, "timeout": settings.Timeout} {
		if duration != nil && *duration <= 0 {
			problems = append(problems, placedAtValue(root, fmt.Sprintf("`%s` must be above zero", name), append(slices.Clone(path), name)...))
		}
	}
	return problems
}

// missingSettings are the settings a check needs that neither its prober nor its target states.
func missingSettings(proberName string, settings CheckSettings) []string {
	var missing []string
	if len(settings.Ports) == 0 {
		missing = append(missing, "ports")
	}
	if proberName == HTTPProber && settings.Path == "" {
		missing = append(missing, "path")
	}
	if proberName == HTTPProber && len(settings.StatusCodes) == 0 {
		missing = append(missing, "status_codes")
	}
	if proberName == DNSProber && len(settings.RecordTypes) == 0 {
		missing = append(missing, "record_types")
	}
	return missing
}
