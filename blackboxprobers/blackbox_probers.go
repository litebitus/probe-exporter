// Package blackboxprobers carries out the exporter's probers that blackbox_exporter's probers can make, a check's settings turned into a blackbox module and each run's place into what blackbox's prober is handed, running exactly as blackbox's own handler does.
package blackboxprobers

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"strconv"

	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
	"go.yaml.in/yaml/v3"

	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
)

// standInQueryName passes blackbox's required `query_name` check; each run asks for its own host.
const standInQueryName = "query-name.invalid"

// responseBodyLimit is the most of a response body an `http` check reads.
const responseBodyLimit = "1MiB"

// BlackboxProber is one of the exporter's probers, with the settings it checks one target with, carried out by a blackbox prober.
type BlackboxProber struct {
	proberName string
	module     blackboxconfig.Module
	probe      prober.ProbeFn
	path       string
}

// NewBlackboxProber is the exporter's prober of a name, with the settings it checks a target with, its TLS checks trusting the certificate authorities in trustedAuthoritiesFile, or the system's where it is empty.
func NewBlackboxProber(proberName string, settings exporterconfig.CheckSettings, trustedAuthoritiesFile string) (BlackboxProber, error) {
	moduleSettings, err := blackboxModuleSettings(proberName, settings, trustedAuthoritiesFile)
	if err != nil {
		return BlackboxProber{}, err
	}
	moduleText, err := yaml.Marshal(moduleSettings)
	if err != nil {
		return BlackboxProber{}, err
	}
	var module blackboxconfig.Module
	if err := yaml.Unmarshal(moduleText, &module); err != nil {
		return BlackboxProber{}, fmt.Errorf("prober `%s`: %w", proberName, err)
	}
	return BlackboxProber{proberName: proberName, module: module, probe: prober.Probers[module.Prober], path: settings.Path}, nil
}

func blackboxModuleSettings(proberName string, settings exporterconfig.CheckSettings, trustedAuthoritiesFile string) (map[string]any, error) {
	tlsSettings := map[string]any{}
	if trustedAuthoritiesFile != "" {
		tlsSettings["ca_file"] = trustedAuthoritiesFile
	}
	moduleSettings := map[string]any{"timeout": settings.Timeout.String()}
	switch proberName {
	case exporterconfig.CertificateProber:
		moduleSettings["prober"] = "tcp"
		moduleSettings["tcp"] = map[string]any{"preferred_ip_protocol": "ip4", "tls": true, "tls_config": tlsSettings}
	case exporterconfig.DNSProber:
		moduleSettings["prober"] = "dns"
		moduleSettings["dns"] = map[string]any{"preferred_ip_protocol": "ip4", "query_name": standInQueryName}
	case exporterconfig.HTTPProber:
		moduleSettings["prober"] = "http"
		moduleSettings["http"] = map[string]any{
			"preferred_ip_protocol": "ip4", "valid_status_codes": settings.StatusCodes, "tls_config": tlsSettings,
			"body_size_limit": responseBodyLimit,
		}
	case exporterconfig.PingProber:
		moduleSettings["prober"] = "icmp"
		moduleSettings["icmp"] = map[string]any{"preferred_ip_protocol": "ip4"}
	case exporterconfig.TCPProber:
		moduleSettings["prober"] = "tcp"
		moduleSettings["tcp"] = map[string]any{"preferred_ip_protocol": "ip4"}
	default:
		return nil, fmt.Errorf("blackbox_exporter carries out no prober `%s`", proberName)
	}
	return moduleSettings, nil
}

// Probe runs against one run's place: a certificate check asks the address and port for the host's certificate, a tcp check connects to the address and port, an http check requests the path from the address and port as the host over the port's protocol, a dns check asks the address and port, over the port's protocol, for the host's record of the run's type, and a ping pings the address.
func (blackboxProber BlackboxProber) Probe(ctx context.Context, target probeschedule.Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	module := blackboxProber.module
	blackboxTarget := target.Address
	hostAndPort := net.JoinHostPort(target.Address, strconv.Itoa(target.Port))
	switch blackboxProber.proberName {
	case exporterconfig.CertificateProber:
		module.TCP.TLSConfig.ServerName = target.Host
		blackboxTarget = hostAndPort
	case exporterconfig.TCPProber:
		blackboxTarget = hostAndPort
	case exporterconfig.HTTPProber:
		headers := maps.Clone(module.HTTP.Headers)
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Host"] = target.Host
		module.HTTP.Headers = headers
		module.HTTP.HTTPClientConfig.TLSConfig.ServerName = target.Host
		blackboxTarget = target.Protocol + "://" + hostAndPort + blackboxProber.path
	case exporterconfig.DNSProber:
		module.DNS.QueryName = target.Host
		module.DNS.QueryType = target.RecordType
		module.DNS.TransportProtocol = target.Protocol
		blackboxTarget = hostAndPort
	}
	return blackboxProber.probe(ctx, blackboxTarget, module, registry, logger)
}
