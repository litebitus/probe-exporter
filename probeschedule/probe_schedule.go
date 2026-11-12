// Package probeschedule runs each probe once per its interval, staggered across the interval, at most a set number at once, and hands each probe's result to the keeper of results.
package probeschedule

import (
	"context"
	"hash/fnv"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Target is what one probe probes: an address, and the hostname it is dialed as or the query name it is asked where its group states one.
type Target struct {
	Address   string
	Hostname  string
	QueryName string
}

// Prober is a kind of check with its module's settings: it probes one target before the context's deadline, registers its series, writes its log, and answers whether the probe succeeded.
type Prober interface {
	Probe(ctx context.Context, target Target, registry *prometheus.Registry, logger *slog.Logger) bool
}

// ProbeIdentity names one probe: its module, its target group and its target.
type ProbeIdentity struct {
	Module      string
	TargetGroup string
	Target      Target
}

// Probe is one probe to run on the schedule, and the settings of its prober as written, which tell a reload whether the probe changed.
type Probe struct {
	Identity       ProbeIdentity
	Labels         map[string]string
	Interval       time.Duration
	Timeout        time.Duration
	Prober         Prober
	ProberSettings string
}

// Result is what a probe's latest run produced.
type Result struct {
	Identity ProbeIdentity
	Labels   map[string]string
	Success  bool
	Duration time.Duration
	EndedAt  time.Time
	Families []*dto.MetricFamily
	Log      string
}

// ResultKeeper holds the results the schedule hands it.
type ResultKeeper interface {
	Keep(result Result)
	Retain(identities map[ProbeIdentity]bool)
}

// Schedule runs the probes it is given.
type Schedule struct {
	keeper ResultKeeper
	logger *slog.Logger

	mutex      sync.Mutex
	running    map[ProbeIdentity]*runningProbe
	probeSlots *probeSlots
	stopping   context.Context
	stop       context.CancelFunc
	waiting    sync.WaitGroup
}

type runningProbe struct {
	settings string
	cancel   context.CancelFunc
}

// NewSchedule is a schedule running no probes yet.
func NewSchedule(keeper ResultKeeper, logger *slog.Logger) *Schedule {
	stopping, stop := context.WithCancel(context.Background())
	return &Schedule{
		keeper:     keeper,
		logger:     logger,
		running:    map[ProbeIdentity]*runningProbe{},
		probeSlots: newProbeSlots(),
		stopping:   stopping,
		stop:       stop,
	}
}

// Apply makes the schedule run exactly these probes, at most maxConcurrentProbes at once: a probe it runs already with the same settings keeps its place and its result, a changed one restarts, and one not given stops and loses its result.
func (schedule *Schedule) Apply(probes []Probe, maxConcurrentProbes int) {
	schedule.mutex.Lock()
	defer schedule.mutex.Unlock()
	schedule.probeSlots.setLimit(maxConcurrentProbes)
	given := map[ProbeIdentity]bool{}
	for _, probe := range probes {
		given[probe.Identity] = true
		settings := probe.fingerprint()
		if running, ok := schedule.running[probe.Identity]; ok {
			if running.settings == settings {
				continue
			}
			running.cancel()
		}
		probeContext, cancel := context.WithCancel(schedule.stopping)
		schedule.running[probe.Identity] = &runningProbe{settings: settings, cancel: cancel}
		schedule.waiting.Add(1)
		go func() {
			defer schedule.waiting.Done()
			schedule.repeat(probeContext, probe)
		}()
	}
	for identity, running := range schedule.running {
		if !given[identity] {
			running.cancel()
			delete(schedule.running, identity)
		}
	}
	schedule.keeper.Retain(given)
}

// Stop stops every probe and waits for those running to end.
func (schedule *Schedule) Stop() {
	schedule.stop()
	schedule.waiting.Wait()
}

func (probe Probe) fingerprint() string {
	var labels []string
	for name, value := range probe.Labels {
		labels = append(labels, name+"="+value)
	}
	slices.Sort(labels)
	return strings.Join(append(labels, probe.Interval.String(), probe.Timeout.String(), probe.ProberSettings), "\x00")
}

// StaggerOffset is how far into its interval a probe first runs, the same for the same probe on every start.
func StaggerOffset(identity ProbeIdentity, interval time.Duration) time.Duration {
	hash := fnv.New64a()
	hash.Write([]byte(strings.Join([]string{
		identity.Module, identity.TargetGroup, identity.Target.Address, identity.Target.Hostname, identity.Target.QueryName,
	}, "\x00")))
	return time.Duration(hash.Sum64() % uint64(interval))
}

func (schedule *Schedule) repeat(ctx context.Context, probe Probe) {
	firstRun := time.NewTimer(StaggerOffset(probe.Identity, probe.Interval))
	defer firstRun.Stop()
	select {
	case <-ctx.Done():
		return
	case <-firstRun.C:
	}
	ticker := time.NewTicker(probe.Interval)
	defer ticker.Stop()
	for {
		schedule.runOnce(ctx, probe)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (schedule *Schedule) runOnce(ctx context.Context, probe Probe) {
	if !schedule.probeSlots.take(ctx) {
		return
	}
	defer schedule.probeSlots.free()
	result := RunProbe(ctx, probe)
	if ctx.Err() != nil {
		return
	}
	if !result.Success {
		schedule.logger.Warn("probe failed",
			"module", probe.Identity.Module, "target_group", probe.Identity.TargetGroup, "target", probe.Identity.Target.Address,
			"hostname", probe.Identity.Target.Hostname, "query_name", probe.Identity.Target.QueryName)
	}
	schedule.keeper.Keep(result)
}
