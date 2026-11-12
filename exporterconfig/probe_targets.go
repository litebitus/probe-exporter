package exporterconfig

import (
	"maps"
	"slices"
	"time"
)

// ProbeTarget is one probe the configuration asks for: a module against one target, dialed as one hostname or asked one query name where its group states them.
type ProbeTarget struct {
	Module      string
	TargetGroup string
	Target      string
	Hostname    string
	QueryName   string
	Labels      map[string]string
	Interval    time.Duration
}

// ProbeTargets are every probe the configuration asks for, in configuration order, each group's in name order.
func (configuration Configuration) ProbeTargets() []ProbeTarget {
	var probeTargets []ProbeTarget
	for _, probeConfig := range configuration.ProbeConfigs {
		for _, name := range slices.Sorted(maps.Keys(probeConfig.TargetGroups)) {
			probeTargets = append(probeTargets, groupProbeTargets(probeConfig, name)...)
		}
	}
	return probeTargets
}

func groupProbeTargets(probeConfig ProbeConfig, groupName string) []ProbeTarget {
	group := probeConfig.TargetGroups[groupName]
	var probeTargets []ProbeTarget
	for _, target := range group.Targets {
		for _, hostname := range valuesOrNone(group.Hostnames) {
			for _, queryName := range valuesOrNone(group.QueryNames) {
				probeTargets = append(probeTargets, ProbeTarget{
					Module:      probeConfig.Module,
					TargetGroup: groupName,
					Target:      target,
					Hostname:    hostname,
					QueryName:   queryName,
					Labels:      group.Labels,
					Interval:    probeConfig.Interval,
				})
			}
		}
	}
	return probeTargets
}

func valuesOrNone(values []string) []string {
	if len(values) == 0 {
		return []string{""}
	}
	return values
}
