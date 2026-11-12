package exporterconfig

import (
	"fmt"
	"maps"
	"slices"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
)

// checkConfiguration holds a configuration the schema admits to the rules a schema cannot state, placing each problem in the file: a probe's `timeout` and `interval` above zero, and each name checked at each address once in a group.
func checkConfiguration(configuration Configuration, root yamldslspec.Node) []yamldslspec.Diagnostic {
	var problems []yamldslspec.Diagnostic
	for _, probeName := range slices.Sorted(maps.Keys(configuration.Probes)) {
		probe := configuration.Probes[probeName]
		if probe.Timeout <= 0 {
			problems = append(problems, placedAtValue(root, "`timeout` must be above zero", "probes", probeName, "timeout"))
		}
		if probe.Interval <= 0 {
			problems = append(problems, placedAtValue(root, "`interval` must be above zero", "probes", probeName, "interval"))
		}
		for _, groupName := range slices.Sorted(maps.Keys(probe.TargetGroups)) {
			checked := map[[2]string]bool{}
			for targetIndex, target := range probe.TargetGroups[groupName].Targets {
				for addressIndex, address := range target.Addresses {
					run := [2]string{target.Name, address}
					if checked[run] {
						problems = append(problems, placedAtValue(root,
							fmt.Sprintf("`%s` is checked at `%s` twice in this group", target.Name, address),
							"probes", probeName, "target_groups", groupName, "targets", targetIndex, "addresses", addressIndex))
					}
					checked[run] = true
				}
			}
		}
	}
	return problems
}
