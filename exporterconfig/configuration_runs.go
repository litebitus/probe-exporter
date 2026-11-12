package exporterconfig

import (
	"maps"
	"slices"
)

// Run is one check's place: a target, the prober checking it, the address checked, and the port and host the check takes, zero and empty where its prober takes none.
type Run struct {
	ProbeTarget string
	Prober      string
	Address     string
	Port        int
	Host        string
}

// Runs are every check the configuration makes, the targets in name order, each target's probers, addresses, ports and hosts in the order written.
func (configuration Configuration) Runs() []Run {
	var runs []Run
	for _, targetName := range slices.Sorted(maps.Keys(configuration.ProbeTargets)) {
		target := configuration.ProbeTargets[targetName]
		for _, proberName := range target.Probers {
			place := Run{ProbeTarget: targetName, Prober: proberName}
			if proberName == DomainRegistrationProber {
				place.Address = targetName
				runs = append(runs, place)
				continue
			}
			for _, address := range target.Addresses {
				place.Address = address
				for _, port := range portsOf(proberName, configuration.Probers[proberName]) {
					place.Port = port
					for _, host := range hostsOf(proberName, target) {
						place.Host = host
						runs = append(runs, place)
					}
				}
			}
		}
	}
	return runs
}

func portsOf(proberName string, settings ProberSettings) []int {
	switch proberName {
	case CertificateProber, HTTPProber, TCPProber:
		return settings.Ports
	}
	return []int{0}
}

func hostsOf(proberName string, target ProbeTarget) []string {
	switch proberName {
	case CertificateProber, DNSProber, HTTPProber:
		return target.Serves
	}
	return []string{""}
}
