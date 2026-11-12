package exporterapp

import (
	"fmt"
	"net/http"

	"github.com/litebitus/probe-exporter/blackboxprobers"
	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
	"github.com/litebitus/probe-exporter/rdapprober"
)

// proberSet makes each probe's prober, every rdap probe sharing one bootstrap.
type proberSet struct {
	rdapProber rdapprober.RDAPProber
}

func newProberSet(bootstrap *rdapprober.Bootstrap, client *http.Client) *proberSet {
	return &proberSet{rdapProber: rdapprober.NewRDAPProber(bootstrap, client)}
}

func (probers *proberSet) prober(probe exporterconfig.Probe) (probeschedule.Prober, error) {
	if probe.Prober == exporterconfig.RDAPProber {
		return probers.rdapProber, nil
	}
	return blackboxprobers.NewModuleProber(probe.BlackboxModule)
}

func (probers *proberSet) probes(configuration exporterconfig.Configuration) ([]probeschedule.Probe, error) {
	probeProbers := map[string]probeschedule.Prober{}
	for name, probe := range configuration.Probes {
		prober, err := probers.prober(probe)
		if err != nil {
			return nil, fmt.Errorf("probe `%s`: %w", name, err)
		}
		probeProbers[name] = prober
	}
	var probes []probeschedule.Probe
	for _, probeTarget := range configuration.ProbeTargets() {
		probe := configuration.Probes[probeTarget.Probe]
		probes = append(probes, probeschedule.Probe{
			Identity: probeschedule.ProbeIdentity{
				Probe:       probeTarget.Probe,
				TargetGroup: probeTarget.TargetGroup,
				Target:      probeschedule.Target{Name: probeTarget.Name, Address: probeTarget.Address},
			},
			Labels:         probeTarget.Labels,
			Interval:       probeTarget.Interval,
			Timeout:        probe.Timeout,
			Prober:         probeProbers[probeTarget.Probe],
			ProberSettings: probe.WrittenSettings,
		})
	}
	return probes, nil
}
