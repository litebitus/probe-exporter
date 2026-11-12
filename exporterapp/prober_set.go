package exporterapp

import (
	"fmt"
	"net/http"

	"github.com/litebitus/probe-exporter/blackboxprobers"
	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
	"github.com/litebitus/probe-exporter/rdapprober"
)

// proberSet makes each module's prober, every rdap module sharing one bootstrap.
type proberSet struct {
	rdapProber rdapprober.RDAPProber
}

func newProberSet(bootstrap *rdapprober.Bootstrap, client *http.Client) *proberSet {
	return &proberSet{rdapProber: rdapprober.NewRDAPProber(bootstrap, client)}
}

func (probers *proberSet) prober(module exporterconfig.Module) (probeschedule.Prober, error) {
	if module.Prober == exporterconfig.RDAPProber {
		return probers.rdapProber, nil
	}
	return blackboxprobers.NewModuleProber(module.BlackboxModule)
}

func (probers *proberSet) probes(configuration exporterconfig.Configuration) ([]probeschedule.Probe, error) {
	moduleProbers := map[string]probeschedule.Prober{}
	for name, module := range configuration.Modules {
		prober, err := probers.prober(module)
		if err != nil {
			return nil, fmt.Errorf("module `%s`: %w", name, err)
		}
		moduleProbers[name] = prober
	}
	var probes []probeschedule.Probe
	for _, probeTarget := range configuration.ProbeTargets() {
		module := configuration.Modules[probeTarget.Module]
		probes = append(probes, probeschedule.Probe{
			Identity: probeschedule.ProbeIdentity{
				Module:      probeTarget.Module,
				TargetGroup: probeTarget.TargetGroup,
				Target: probeschedule.Target{
					Address:   probeTarget.Target,
					Hostname:  probeTarget.Hostname,
					QueryName: probeTarget.QueryName,
				},
			},
			Labels:         probeTarget.Labels,
			Interval:       probeTarget.Interval,
			Timeout:        module.Timeout,
			Prober:         moduleProbers[probeTarget.Module],
			ProberSettings: module.WrittenText,
		})
	}
	return probes, nil
}
