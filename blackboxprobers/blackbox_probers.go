// Package blackboxprobers holds blackbox_exporter's probers behind the schedule's prober interface, each with its probe's settings, running exactly as blackbox's own handler does.
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

// ModuleProber is one blackbox prober with its settings, in blackbox's module format.
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

// Probe runs against one target's address with the module, its name sent as the `Host` header for `http`, the TLS server name for `tcp` and `grpc`, and the name asked for `dns`.
func (moduleProber ModuleProber) Probe(ctx context.Context, target probeschedule.Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	module := moduleProber.module
	switch module.Prober {
	case "http":
		headers := maps.Clone(module.HTTP.Headers)
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Host"] = target.Name
		module.HTTP.Headers = headers
	case "tcp":
		module.TCP.TLSConfig.ServerName = target.Name
	case "grpc":
		module.GRPC.TLSConfig.ServerName = target.Name
	case "dns":
		module.DNS.QueryName = target.Name
	}
	return moduleProber.probe(ctx, target.Address, module, registry, logger)
}
