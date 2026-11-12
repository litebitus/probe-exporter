package probeschedule

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// An info series is named for its prober, valued 1, and labeled with exactly the chosen labels' facts; with none chosen there is no series.
func TestAnInfoSeriesCarriesTheChosenLabelsFacts(t *testing.T) {
	facts := map[string]string{"mock_a": "mock-fact-a", "mock_b": "mock-fact-b"}
	registry := prometheus.NewRegistry()
	PublishInfo(registry, "mock_prober", facts, []string{"mock_b"})
	families, err := registry.Gather()
	if err != nil || len(families) != 1 || families[0].GetName() != "probe_mock_prober_info" {
		t.Fatalf("families = %v (%v)", families, err)
	}
	series := families[0].Metric[0]
	if series.GetGauge().GetValue() != 1 || len(series.Label) != 1 || series.Label[0].GetName() != "mock_b" || series.Label[0].GetValue() != "mock-fact-b" {
		t.Errorf("series = %v", series)
	}
	unchosen := prometheus.NewRegistry()
	PublishInfo(unchosen, "mock_prober", facts, nil)
	if families, _ := unchosen.Gather(); len(families) != 0 {
		t.Errorf("with no label chosen, families = %v", families)
	}
}
