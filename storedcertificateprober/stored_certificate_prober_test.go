package storedcertificateprober

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/litebitus/probe-exporter/probeschedule"
)

// mockCertificatePEM is a self-signed certificate for a mock name, expiring at the time given, as PEM.
func mockCertificatePEM(t *testing.T, commonName string, expiresAt time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    expiresAt.AddDate(-1, 0, 0),
		NotAfter:     expiresAt,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

type probeOutcome struct {
	success bool
	series  map[string]float64
	log     string
}

// probeVariable runs the prober against one environment variable, with the variables given as its whole environment.
func probeVariable(t *testing.T, environment map[string]string, variable string) probeOutcome {
	t.Helper()
	prober := NewStoredCertificateProber(func(name string) (string, bool) {
		value, isSet := environment[name]
		return value, isSet
	})
	registry := prometheus.NewRegistry()
	log := &strings.Builder{}
	success := prober.Probe(context.Background(), probeschedule.Target{EnvironmentVariable: variable}, registry,
		slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[string]float64{}
	for _, family := range families {
		series[family.GetName()] = family.Metric[0].GetGauge().GetValue()
	}
	return probeOutcome{success: success, series: series, log: log.String()}
}

const expirySeries = "probe_stored_certificate_expiry_timestamp_seconds"

// A variable holding several certificates answers when the earliest of them expires, whatever their order, skipping a PEM block of another type.
func TestAStoredCertificateAnswersWhenTheEarliestCertificateExpires(t *testing.T) {
	earliest := time.Date(2027, 6, 3, 6, 44, 37, 0, time.UTC)
	mockKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("mock-key")}))
	value := mockCertificatePEM(t, "mock-leaf", earliest.AddDate(1, 0, 0)) + mockKey + mockCertificatePEM(t, "mock-ca", earliest)
	outcome := probeVariable(t, map[string]string{"MOCK_CHAIN_CRT": value}, "MOCK_CHAIN_CRT")
	if !outcome.success || outcome.series[expirySeries] != float64(earliest.Unix()) {
		t.Errorf("outcome = %+v", outcome)
	}
	for _, part := range []string{`subject="CN=mock-leaf"`, `subject="CN=mock-ca"`, `pem_type="PRIVATE KEY"`} {
		if !strings.Contains(outcome.log, part) {
			t.Errorf("the log holds no %q: %s", part, outcome.log)
		}
	}
	if strings.Contains(outcome.log, "BEGIN") || strings.Contains(outcome.log, "mock-key") {
		t.Errorf("the log holds the variable's value: %s", outcome.log)
	}
}

// A run fails, naming why and never the value, when its variable is unset, holds no certificate, or holds one that does not read; it then publishes no expiry.
func TestAStoredCertificateThatCannotBeReadFails(t *testing.T) {
	brokenCertificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("mock-broken")}))
	environment := map[string]string{
		"MOCK_EMPTY":   "",
		"MOCK_TEXT":    "mock-not-pem",
		"MOCK_BROKEN":  brokenCertificate,
		"MOCK_KEY_PEM": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("mock-key")})),
	}
	for variable, want := range map[string]string{
		"MOCK_UNSET":   "the environment variable is not set",
		"MOCK_EMPTY":   "the environment variable holds no PEM certificate",
		"MOCK_TEXT":    "the environment variable holds no PEM certificate",
		"MOCK_KEY_PEM": "the environment variable holds no PEM certificate",
		"MOCK_BROKEN":  "failed to read a certificate",
	} {
		outcome := probeVariable(t, environment, variable)
		if outcome.success || len(outcome.series) != 0 || !strings.Contains(outcome.log, want) ||
			!strings.Contains(outcome.log, "environment_variable="+variable) {
			t.Errorf("%s: outcome = %+v, want %q", variable, outcome, want)
		}
		if strings.Contains(outcome.log, "mock-not-pem") || strings.Contains(outcome.log, "mock-key") {
			t.Errorf("%s: the log holds the variable's value: %s", variable, outcome.log)
		}
	}
}
