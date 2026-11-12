package probeschedule

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// mockProber answers as it is told, counting its probes and the most it ran at once.
type mockProber struct {
	succeeds    bool
	hold        time.Duration
	probes      atomic.Int64
	running     atomic.Int64
	mostAtATime atomic.Int64
}

func (prober *mockProber) Probe(ctx context.Context, target Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	prober.probes.Add(1)
	now := prober.running.Add(1)
	defer prober.running.Add(-1)
	for {
		most := prober.mostAtATime.Load()
		if now <= most || prober.mostAtATime.CompareAndSwap(most, now) {
			break
		}
	}
	logger.Info("mock probe", "target", target.Address)
	select {
	case <-time.After(prober.hold):
	case <-ctx.Done():
		return false
	}
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "mock_value", Help: "A mock value"})
	gauge.Set(7)
	registry.MustRegister(gauge)
	return prober.succeeds
}

// mockKeeper holds what the schedule hands it.
type mockKeeper struct {
	mutex    sync.Mutex
	results  map[ProbeIdentity]Result
	retained []map[ProbeIdentity]bool
}

func newMockKeeper() *mockKeeper { return &mockKeeper{results: map[ProbeIdentity]Result{}} }

func (keeper *mockKeeper) Keep(result Result) {
	keeper.mutex.Lock()
	defer keeper.mutex.Unlock()
	keeper.results[result.Identity] = result
}

func (keeper *mockKeeper) Retain(identities map[ProbeIdentity]bool) {
	keeper.mutex.Lock()
	defer keeper.mutex.Unlock()
	keeper.retained = append(keeper.retained, identities)
}

func (keeper *mockKeeper) result(identity ProbeIdentity) (Result, bool) {
	keeper.mutex.Lock()
	defer keeper.mutex.Unlock()
	result, kept := keeper.results[identity]
	return result, kept
}

func mockIdentity(target string) ProbeIdentity {
	return ProbeIdentity{Probe: "mock_probe", TargetGroup: "mock-group", Target: Target{Address: target}}
}

func mockProbe(target string, prober Prober, interval time.Duration) Probe {
	return Probe{Identity: mockIdentity(target), Interval: interval, Timeout: time.Second, Prober: prober, ProberSettings: "mock"}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not hold within 3s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestARunProbeHoldsTheProbersSeriesItsOwnAndItsLog(t *testing.T) {
	result := RunProbe(context.Background(), mockProbe("mock-target", &mockProber{succeeds: true}, time.Minute))
	names := map[string]float64{}
	for _, family := range result.Families {
		names[family.GetName()] = family.Metric[0].GetGauge().GetValue()
	}
	if !result.Success || names["mock_value"] != 7 || names["probe_success"] != 1 ||
		names["probe_duration_seconds"] <= 0 || names["probe_last_run_timestamp_seconds"] < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Errorf("result = %+v, series = %v", result, names)
	}
	if !strings.Contains(result.Log, "mock probe") || !strings.Contains(result.Log, "target=mock-target") {
		t.Errorf("log = %q", result.Log)
	}
}

func TestAFailedProbeLogsWhyAndSaysSoInItsSeries(t *testing.T) {
	result := RunProbe(context.Background(), mockProbe("mock-target", &mockProber{succeeds: false}, time.Minute))
	for _, family := range result.Families {
		if family.GetName() == "probe_success" && family.Metric[0].GetGauge().GetValue() != 0 {
			t.Errorf("probe_success = %v", family.Metric[0].GetGauge().GetValue())
		}
	}
	if result.Success || !strings.Contains(result.Log, "probe failed") {
		t.Errorf("result = %+v", result)
	}
}

func TestAProbeIsBoundedByItsTimeout(t *testing.T) {
	probe := mockProbe("mock-target", &mockProber{succeeds: true, hold: time.Minute}, time.Minute)
	probe.Timeout = 20 * time.Millisecond
	start := time.Now()
	result := RunProbe(context.Background(), probe)
	if result.Success || time.Since(start) > time.Second {
		t.Errorf("result = %+v after %v", result, time.Since(start))
	}
}

func TestAProbeLogPastItsBoundDropsItsEarliestLines(t *testing.T) {
	probeLog := &boundedLog{bound: 10}
	probeLog.Write([]byte("first\nsecond\nthi"))
	probeLog.Write([]byte("rd\n"))
	if got := probeLog.String(); got != "third\n" {
		t.Errorf("log = %q", got)
	}
}

func TestAProbesStaggerIsWithinItsIntervalAndTheSameOnEveryStart(t *testing.T) {
	first := StaggerOffset(mockIdentity("mock-a"), time.Minute)
	if first < 0 || first >= time.Minute || first != StaggerOffset(mockIdentity("mock-a"), time.Minute) {
		t.Errorf("offset = %v", first)
	}
	if first == StaggerOffset(mockIdentity("mock-b"), time.Minute) {
		t.Error("two probes start at the same offset")
	}
}

func TestTheScheduleRunsEachProbeAndHandsOnItsResult(t *testing.T) {
	keeper := newMockKeeper()
	schedule := NewSchedule(keeper, quietLogger())
	defer schedule.Stop()
	prober := &mockProber{succeeds: true}
	schedule.Apply([]Probe{mockProbe("mock-a", prober, 20*time.Millisecond)}, 1)
	waitFor(t, func() bool { return prober.probes.Load() >= 3 })
	if _, kept := keeper.result(mockIdentity("mock-a")); !kept {
		t.Error("the result was not handed on")
	}
}

func TestAFailedProbeIsHandedOnToo(t *testing.T) {
	keeper := newMockKeeper()
	schedule := NewSchedule(keeper, quietLogger())
	defer schedule.Stop()
	schedule.Apply([]Probe{mockProbe("mock-a", &mockProber{succeeds: false}, 20*time.Millisecond)}, 1)
	waitFor(t, func() bool { _, kept := keeper.result(mockIdentity("mock-a")); return kept })
	if result, _ := keeper.result(mockIdentity("mock-a")); result.Success {
		t.Error("a failed probe was kept as a success")
	}
}

func TestNoMoreProbesRunAtOnceThanTheBoundAllows(t *testing.T) {
	keeper := newMockKeeper()
	schedule := NewSchedule(keeper, quietLogger())
	defer schedule.Stop()
	prober := &mockProber{succeeds: true, hold: 30 * time.Millisecond}
	var probes []Probe
	for _, target := range []string{"mock-a", "mock-b", "mock-c", "mock-d", "mock-e", "mock-f"} {
		probes = append(probes, mockProbe(target, prober, 10*time.Millisecond))
	}
	schedule.Apply(probes, 2)
	waitFor(t, func() bool { return prober.probes.Load() >= 12 })
	if most := prober.mostAtATime.Load(); most > 2 {
		t.Errorf("%d probes ran at once, past the bound of 2", most)
	}
}

// On reload, an unchanged probe keeps running, a changed one restarts, and one no longer given stops and loses its result.
func TestApplyingAgainKeepsUnchangedProbesAndStopsThoseNotGiven(t *testing.T) {
	keeper := newMockKeeper()
	schedule := NewSchedule(keeper, quietLogger())
	defer schedule.Stop()
	kept, changed, removed := &mockProber{succeeds: true}, &mockProber{succeeds: true}, &mockProber{succeeds: true}
	changedProbe := mockProbe("mock-changed", changed, time.Hour)
	schedule.Apply([]Probe{
		mockProbe("mock-kept", kept, time.Hour), changedProbe, mockProbe("mock-removed", removed, time.Hour),
	}, 1)
	keptRunning := schedule.running[mockIdentity("mock-kept")]
	changedRunning := schedule.running[mockIdentity("mock-changed")]
	changedProbe.ProberSettings = "mock-changed-settings"
	schedule.Apply([]Probe{mockProbe("mock-kept", kept, time.Hour), changedProbe}, 1)
	if schedule.running[mockIdentity("mock-kept")] != keptRunning {
		t.Error("an unchanged probe was restarted")
	}
	if schedule.running[mockIdentity("mock-changed")] == changedRunning {
		t.Error("a changed probe kept running with its old settings")
	}
	if _, running := schedule.running[mockIdentity("mock-removed")]; running {
		t.Error("a probe no longer given kept running")
	}
	last := keeper.retained[len(keeper.retained)-1]
	if !last[mockIdentity("mock-kept")] || !last[mockIdentity("mock-changed")] || last[mockIdentity("mock-removed")] {
		t.Errorf("retained = %v", last)
	}
}

func TestAProbesLabelsAreAmongItsSettings(t *testing.T) {
	probe := mockProbe("mock-a", &mockProber{}, time.Minute)
	probe.Labels = map[string]string{"mock_b": "2", "mock_a": "1"}
	relabeled := probe
	relabeled.Labels = map[string]string{"mock_a": "1"}
	if probe.fingerprint() == relabeled.fingerprint() || probe.fingerprint() != probe.fingerprint() {
		t.Error("a probe's labels do not decide its settings")
	}
}

func TestStoppingWaitsForRunningProbesToEnd(t *testing.T) {
	schedule := NewSchedule(newMockKeeper(), quietLogger())
	prober := &mockProber{succeeds: true, hold: time.Hour}
	schedule.Apply([]Probe{mockProbe("mock-a", prober, time.Millisecond)}, 1)
	waitFor(t, func() bool { return prober.running.Load() == 1 })
	schedule.Stop()
	if prober.running.Load() != 0 {
		t.Error("a probe still runs after the schedule stopped")
	}
}

func TestAProbeWaitingForASlotStopsWithTheSchedule(t *testing.T) {
	schedule := NewSchedule(newMockKeeper(), quietLogger())
	holding := &mockProber{succeeds: true, hold: time.Hour}
	waiting := &mockProber{succeeds: true}
	schedule.Apply([]Probe{mockProbe("mock-a", holding, time.Millisecond)}, 1)
	waitFor(t, func() bool { return holding.running.Load() == 1 })
	schedule.Apply([]Probe{mockProbe("mock-a", holding, time.Millisecond), mockProbe("mock-b", waiting, time.Millisecond)}, 1)
	time.Sleep(20 * time.Millisecond)
	schedule.Stop()
	if waiting.probes.Load() != 0 {
		t.Error("a probe ran past the bound")
	}
}
