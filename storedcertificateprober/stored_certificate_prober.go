// Package storedcertificateprober reads the certificates an environment variable holds, as PEM, and publishes when the earliest of them expires.
package storedcertificateprober

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

// StoredCertificateProber reads a run's environment variable through a lookup, as `os.LookupEnv` answers one.
type StoredCertificateProber struct {
	lookupEnvironmentVariable func(name string) (string, bool)
}

// NewStoredCertificateProber is the prober reading environment variables through the lookup given.
func NewStoredCertificateProber(lookupEnvironmentVariable func(name string) (string, bool)) StoredCertificateProber {
	return StoredCertificateProber{lookupEnvironmentVariable: lookupEnvironmentVariable}
}

// Probe reads every certificate in the run's environment variable, skipping PEM blocks of other types, and succeeds when it holds at least one and each reads; the variable's value is never logged.
func (prober StoredCertificateProber) Probe(_ context.Context, target probeschedule.Target, registry *prometheus.Registry, logger *slog.Logger) bool {
	variable := target.EnvironmentVariable
	value, isSet := prober.lookupEnvironmentVariable(variable)
	if !isSet {
		logger.Error("the environment variable is not set", "environment_variable", variable)
		return false
	}
	var earliestExpiry time.Time
	certificatesRead := 0
	remaining := []byte(value)
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		if block.Type != "CERTIFICATE" {
			logger.Debug("skipped a PEM block that holds no certificate", "environment_variable", variable, "pem_type", block.Type)
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			logger.Error("failed to read a certificate", "environment_variable", variable, "err", err)
			return false
		}
		logger.Debug("read a certificate", "environment_variable", variable, "subject", certificate.Subject.String(),
			"expires_at", certificate.NotAfter)
		if certificatesRead == 0 || certificate.NotAfter.Before(earliestExpiry) {
			earliestExpiry = certificate.NotAfter
		}
		certificatesRead++
	}
	if certificatesRead == 0 {
		logger.Error("the environment variable holds no PEM certificate", "environment_variable", variable)
		return false
	}
	expiry := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_stored_certificate_expiry_timestamp_seconds",
		Help: "When the earliest certificate the environment variable holds expires",
	})
	expiry.Set(float64(earliestExpiry.Unix()))
	registry.MustRegister(expiry)
	return true
}
