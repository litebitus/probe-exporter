package exporterconfig

import (
	"maps"
	"slices"
	"time"
)

// ProbeTarget is one run's place: a probe, a target's name and one address it is checked at.
type ProbeTarget struct {
	Probe       string
	TargetGroup string
	Name        string
	Address     string
	Labels      map[string]string
	Interval    time.Duration
}

// ProbeTargets are every run's place, the probes and each probe's groups in name order, each group's targets in the order written.
func (configuration Configuration) ProbeTargets() []ProbeTarget {
	var probeTargets []ProbeTarget
	for _, probeName := range slices.Sorted(maps.Keys(configuration.Probes)) {
		probe := configuration.Probes[probeName]
		for _, groupName := range slices.Sorted(maps.Keys(probe.TargetGroups)) {
			group := probe.TargetGroups[groupName]
			for _, target := range group.Targets {
				for _, address := range target.Addresses {
					probeTargets = append(probeTargets, ProbeTarget{
						Probe:       probeName,
						TargetGroup: groupName,
						Name:        target.Name,
						Address:     address,
						Labels:      group.Labels,
						Interval:    probe.Interval,
					})
				}
			}
		}
	}
	return probeTargets
}
