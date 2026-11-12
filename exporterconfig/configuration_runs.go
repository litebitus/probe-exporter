package exporterconfig

import (
	"maps"
	"slices"
)

// Run is one check's place: a target, the prober checking it, the address checked, and the port, host and record type the check takes, zero and empty where its prober takes none.
type Run struct {
	ProbeTarget string
	Prober      string
	Address     string
	Port        Port
	Host        string
	RecordType  string
}

// Runs are every check the configuration makes: the targets and each target's probers in name order, each prober at every address, on every port, for every host and record type it takes, in the order written; a target without `serves` is checked for each address as its own host.
func (configuration Configuration) Runs() []Run {
	var runs []Run
	for _, targetName := range slices.Sorted(maps.Keys(configuration.ProbeTargets)) {
		target := configuration.ProbeTargets[targetName]
		for _, proberName := range slices.Sorted(maps.Keys(target.Probers)) {
			settings := configuration.CheckSettings(targetName, proberName)
			addresses := target.Addresses
			if proberName == DomainRegistrationProber {
				addresses = []string{targetName}
			}
			for _, address := range addresses {
				for _, port := range settings.Ports {
					for _, host := range hostsOf(proberName, target, address) {
						for _, recordType := range recordTypesOf(proberName, settings) {
							runs = append(runs, Run{
								ProbeTarget: targetName, Prober: proberName, Address: address, Port: port, Host: host, RecordType: recordType,
							})
						}
					}
				}
			}
		}
	}
	return runs
}

// hostsOf are the hosts a prober checks a target for at an address: the hosts it serves, or for a `certificate` or `http` check of a target that states none, the address itself.
func hostsOf(proberName string, target ProbeTarget, address string) []string {
	switch proberName {
	case CertificateProber, HTTPProber:
		if len(target.Serves) == 0 {
			return []string{address}
		}
		return target.Serves
	case DNSProber:
		return target.Serves
	}
	return []string{""}
}

func recordTypesOf(proberName string, settings CheckSettings) []string {
	if proberName == DNSProber {
		return settings.RecordTypes
	}
	return []string{""}
}
