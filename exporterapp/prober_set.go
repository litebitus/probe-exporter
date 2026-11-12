package exporterapp

import (
	"fmt"
	"net/http"

	"github.com/litebitus/probe-exporter/blackboxprobers"
	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
	"github.com/litebitus/probe-exporter/rdapprober"
)

// proberSet makes the prober each target is checked by, `domain_registration` from one shared RDAP bootstrap and every other from blackbox_exporter's probers.
type proberSet struct {
	rdapProber             rdapprober.RDAPProber
	trustedAuthoritiesFile string
}

func newProberSet(bootstrap *rdapprober.Bootstrap, client *http.Client, trustedAuthoritiesFile string) *proberSet {
	return &proberSet{rdapProber: rdapprober.NewRDAPProber(bootstrap, client), trustedAuthoritiesFile: trustedAuthoritiesFile}
}

func (probers *proberSet) prober(proberName string, settings exporterconfig.CheckSettings) (probeschedule.Prober, error) {
	if proberName == exporterconfig.DomainRegistrationProber {
		return probers.rdapProber, nil
	}
	return blackboxprobers.NewBlackboxProber(proberName, settings, probers.trustedAuthoritiesFile)
}

// probes are the configuration's checks, each with the prober its target is checked by.
func (probers *proberSet) probes(configuration exporterconfig.Configuration) ([]probeschedule.Probe, error) {
	type targetProber struct{ target, prober string }
	made := map[targetProber]probeschedule.Prober{}
	var probes []probeschedule.Probe
	for _, run := range configuration.Runs() {
		settings := configuration.CheckSettings(run.ProbeTarget, run.Prober)
		key := targetProber{run.ProbeTarget, run.Prober}
		if _, isMade := made[key]; !isMade {
			prober, err := probers.prober(run.Prober, settings)
			if err != nil {
				return nil, fmt.Errorf("target `%s`: %w", run.ProbeTarget, err)
			}
			made[key] = prober
		}
		probes = append(probes, probeschedule.Probe{
			Identity: probeschedule.ProbeIdentity{
				ProbeTarget: run.ProbeTarget,
				Prober:      run.Prober,
				Target: probeschedule.Target{
					Address: run.Address, Port: run.Port.Number, Protocol: run.Port.Protocol, Host: run.Host, RecordType: run.RecordType,
				},
			},
			Interval:       settings.Interval,
			Timeout:        settings.Timeout,
			Prober:         made[key],
			ProberSettings: fmt.Sprintf("%+v", settings),
		})
	}
	return probes, nil
}
