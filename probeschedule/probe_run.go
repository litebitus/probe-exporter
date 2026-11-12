package probeschedule

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ProbeLogBound is how many bytes of its log a probe keeps; past it, the earliest lines are dropped.
const ProbeLogBound = 64 * 1024

// RunProbe runs one probe within its timeout and answers its result: the prober's series and log, and whether, when and how long it ran.
func RunProbe(ctx context.Context, probe Probe) Result {
	probeContext, cancel := context.WithTimeout(ctx, probe.Timeout)
	defer cancel()
	probeLog := &boundedLog{bound: ProbeLogBound}
	logger := slog.New(slog.NewTextHandler(probeLog, &slog.HandlerOptions{Level: slog.LevelDebug}))
	registry := prometheus.NewRegistry()
	start := time.Now()
	success := probe.Prober.Probe(probeContext, probe.Identity.Target, registry, logger)
	duration := time.Since(start)
	endedAt := time.Now()
	if !success {
		logger.Error("probe failed", "duration_seconds", duration.Seconds())
	}
	registerGauge(registry, "probe_success", "Whether the probe succeeded", boolValue(success))
	registerGauge(registry, "probe_duration_seconds", "How long the probe took to complete in seconds", duration.Seconds())
	registerGauge(registry, "probe_last_run_timestamp_seconds", "When the probe ended",
		float64(endedAt.UnixNano())/float64(time.Second))
	families, err := registry.Gather()
	if err != nil {
		logger.Error("failed to gather the probe's series", "err", err)
	}
	return Result{
		Identity: probe.Identity,
		Labels:   probe.Labels,
		Success:  success,
		Duration: duration,
		EndedAt:  endedAt,
		Families: families,
		Log:      probeLog.String(),
	}
}

func registerGauge(registry *prometheus.Registry, name, help string, value float64) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	gauge.Set(value)
	registry.MustRegister(gauge)
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

type boundedLog struct {
	bound   int
	mutex   sync.Mutex
	lines   [][]byte
	size    int
	partial []byte
}

func (probeLog *boundedLog) Write(written []byte) (int, error) {
	probeLog.mutex.Lock()
	defer probeLog.mutex.Unlock()
	remaining := append(probeLog.partial, written...)
	for {
		end := bytes.IndexByte(remaining, '\n')
		if end < 0 {
			break
		}
		probeLog.keepLine(bytes.Clone(remaining[:end+1]))
		remaining = remaining[end+1:]
	}
	probeLog.partial = bytes.Clone(remaining)
	return len(written), nil
}

func (probeLog *boundedLog) keepLine(line []byte) {
	probeLog.lines = append(probeLog.lines, line)
	probeLog.size += len(line)
	for probeLog.size > probeLog.bound && len(probeLog.lines) > 0 {
		probeLog.size -= len(probeLog.lines[0])
		probeLog.lines = probeLog.lines[1:]
	}
}

func (probeLog *boundedLog) String() string {
	probeLog.mutex.Lock()
	defer probeLog.mutex.Unlock()
	return string(append(bytes.Join(probeLog.lines, nil), probeLog.partial...))
}
