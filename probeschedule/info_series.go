package probeschedule

import "github.com/prometheus/client_golang/prometheus"

// PublishInfo registers a prober's info series, `probe_<prober>_info` valued 1, labeled with the fact of each chosen label; where no label is chosen it registers nothing.
func PublishInfo(registry *prometheus.Registry, proberName string, facts map[string]string, chosenLabels []string) {
	if len(chosenLabels) == 0 {
		return
	}
	labels := prometheus.Labels{}
	for _, label := range chosenLabels {
		labels[label] = facts[label]
	}
	info := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "probe_" + proberName + "_info",
		Help:        "Facts the " + proberName + " prober read about what it checked, as labels",
		ConstLabels: labels,
	})
	info.Set(1)
	registry.MustRegister(info)
}
