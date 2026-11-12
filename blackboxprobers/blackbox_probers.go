// Package blackboxprobers holds blackbox_exporter's probers behind the schedule's prober interface, each with its module, probing exactly as blackbox's own handler does.
package blackboxprobers

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

// ModuleProber is one blackbox prober with its module.
type ModuleProber struct {
	module blackboxconfig.Module
	probe  prober.ProbeFn
}

// NewModuleProber is the blackbox prober a module names, with the module.
func NewModuleProber(module blackboxconfig.Module) (ModuleProber, error) {
	probe, known := prober.Probers[module.Prober]
	if !known {
		return ModuleProber{}, fmt.Errorf("blackbox_exporter has no prober `%s`", module.Prober)
	}
	return ModuleProber{module: module, probe: probe}, nil
}

// Probe probes one target with the module, dialing it as its hostname for `http` and `tcp` and asking its query name for `dns`.
func (moduleProber ModuleProber) Probe(ctx context.Context, target probeschedule.Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	module := moduleProber.module
	if target.Hostname != "" {
		switch module.Prober {
		case "http":
			headers := maps.Clone(module.HTTP.Headers)
			if headers == nil {
				headers = map[string]string{}
			}
			headers["Host"] = target.Hostname
			module.HTTP.Headers = headers
		case "tcp":
			module.TCP.TLSConfig.ServerName = target.Hostname
		}
	}
	if target.QueryName != "" {
		module.DNS.QueryName = target.QueryName
	}
	return moduleProber.probe(ctx, target.Address, module, registry, logger)
}
