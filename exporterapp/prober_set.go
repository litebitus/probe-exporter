package exporterapp

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/litebitus/probe-exporter/blackboxprobers"
	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
	"github.com/litebitus/probe-exporter/rdapprober"
)

// proberSet makes each configured prober, `domain_registration` from one shared RDAP bootstrap and every other from blackbox_exporter's probers.
type proberSet struct {
	rdapProber             rdapprober.RDAPProber
	trustedAuthoritiesFile string
}

func newProberSet(bootstrap *rdapprober.Bootstrap, client *http.Client, trustedAuthoritiesFile string) *proberSet {
	return &proberSet{rdapProber: rdapprober.NewRDAPProber(bootstrap, client), trustedAuthoritiesFile: trustedAuthoritiesFile}
}

func (probers *proberSet) prober(proberName string, settings exporterconfig.ProberSettings) (probeschedule.Prober, error) {
	if proberName == exporterconfig.DomainRegistrationProber {
		return probers.rdapProber, nil
	}
	return blackboxprobers.NewBlackboxProber(proberName, settings, probers.trustedAuthoritiesFile)
}

func (probers *proberSet) probes(configuration exporterconfig.Configuration) ([]probeschedule.Probe, error) {
	configured := map[string]probeschedule.Prober{}
	for _, proberName := range slices.Sorted(maps.Keys(configuration.Probers)) {
		prober, err := probers.prober(proberName, configuration.Probers[proberName])
		if err != nil {
			return nil, err
		}
		configured[proberName] = prober
	}
	var probes []probeschedule.Probe
	for _, run := range configuration.Runs() {
		settings := configuration.Probers[run.Prober]
		probes = append(probes, probeschedule.Probe{
			Identity: probeschedule.ProbeIdentity{
				ProbeTarget: run.ProbeTarget,
				Prober:      run.Prober,
				Target:      probeschedule.Target{Address: run.Address, Port: run.Port, Host: run.Host},
			},
			Interval:       settings.Interval,
			Timeout:        settings.Timeout,
			Prober:         configured[run.Prober],
			ProberSettings: fmt.Sprintf("%+v", settings),
		})
	}
	return probes, nil
}
