package blackboxprobers

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/probeschedule"
)

// trustedHost is a name the test servers' certificate is issued for.
const trustedHost = "example.com"

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mockSettings() exporterconfig.CheckSettings {
	return exporterconfig.CheckSettings{Interval: time.Minute, Timeout: 3 * time.Second, Path: "/mock-path", StatusCodes: []int{200}}
}

func newProber(t *testing.T, proberName string, settings exporterconfig.CheckSettings, trustedAuthoritiesFile string) BlackboxProber {
	t.Helper()
	blackboxProber, err := NewBlackboxProber(proberName, settings, trustedAuthoritiesFile)
	if err != nil {
		t.Fatal(err)
	}
	return blackboxProber
}

func probeWith(t *testing.T, blackboxProber BlackboxProber, target probeschedule.Target) (bool, *prometheus.Registry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	registry := prometheus.NewRegistry()
	return blackboxProber.Probe(ctx, target, registry, quietLogger()), registry
}

// trustedAuthorities is a file holding the certificate a test server presents, for the TLS checks to trust.
func trustedAuthorities(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mock-authorities.pem")
	text := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hostAndPort(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, _ := strconv.Atoi(port)
	return host, portNumber
}

// serverNameRecorder is a TLS listener that records the server name each handshake sends and the host each request names.
type serverNameRecorder struct {
	server *httptest.Server
	mutex  sync.Mutex
	names  []string
	hosts  []string
	paths  []string
}

func newServerNameRecorder(t *testing.T, status int) *serverNameRecorder {
	recorder := &serverNameRecorder{}
	recorder.server = httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.mutex.Lock()
		recorder.hosts = append(recorder.hosts, request.Host)
		recorder.paths = append(recorder.paths, request.URL.Path)
		recorder.mutex.Unlock()
		writer.WriteHeader(status)
	}))
	recorder.server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.names = append(recorder.names, hello.ServerName)
		return nil, nil
	}}
	recorder.server.StartTLS()
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (recorder *serverNameRecorder) recorded() (names, hosts, paths []string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return slices.Clone(recorder.names), slices.Clone(recorder.hosts), slices.Clone(recorder.paths)
}

// A certificate check asks the address and port for the host's certificate, trusting the authorities given, and reports when it expires.
func TestACertificateCheckAsksTheAddressForTheHostsCertificate(t *testing.T) {
	recorder := newServerNameRecorder(t, http.StatusOK)
	address, port := hostAndPort(t, recorder.server)
	certificateProber := newProber(t, exporterconfig.CertificateProber, mockSettings(), trustedAuthorities(t, recorder.server))
	succeeded, registry := probeWith(t, certificateProber, probeschedule.Target{Address: address, Port: port, Protocol: "tcp", Host: trustedHost})
	if !succeeded {
		t.Fatal("the check failed")
	}
	if names, _, _ := recorder.recorded(); !slices.Equal(names, []string{trustedHost}) {
		t.Errorf("server names sent = %v", names)
	}
	families, _ := registry.Gather()
	if !slices.ContainsFunc(families, func(family *dto.MetricFamily) bool { return family.GetName() == "probe_ssl_earliest_cert_expiry" }) {
		t.Error("the check reported no certificate expiry")
	}
}

func TestACertificateCheckFailsOnACertificateNotIssuedForTheHost(t *testing.T) {
	recorder := newServerNameRecorder(t, http.StatusOK)
	address, port := hostAndPort(t, recorder.server)
	certificateProber := newProber(t, exporterconfig.CertificateProber, mockSettings(), trustedAuthorities(t, recorder.server))
	target := probeschedule.Target{Address: address, Port: port, Protocol: "tcp", Host: "mock-other.invalid"}
	if succeeded, _ := probeWith(t, certificateProber, target); succeeded {
		t.Error("a certificate not issued for the host passed")
	}
}

// An https check requests the path from the address and port as the host, the host also its TLS server name, and succeeds on the status codes given.
func TestAnHTTPSCheckRequestsThePathAsTheHost(t *testing.T) {
	recorder := newServerNameRecorder(t, http.StatusOK)
	address, port := hostAndPort(t, recorder.server)
	httpProber := newProber(t, exporterconfig.HTTPProber, mockSettings(), trustedAuthorities(t, recorder.server))
	if succeeded, _ := probeWith(t, httpProber, probeschedule.Target{Address: address, Port: port, Protocol: "https", Host: trustedHost}); !succeeded {
		t.Fatal("the check failed")
	}
	names, hosts, paths := recorder.recorded()
	if !slices.Equal(names, []string{trustedHost}) || !slices.Equal(hosts, []string{trustedHost}) || !slices.Equal(paths, []string{"/mock-path"}) {
		t.Errorf("server names %v, hosts %v, paths %v", names, hosts, paths)
	}
}

// An http check on a port whose protocol is `http` requests the path in plain HTTP.
func TestAnHTTPCheckSpeaksThePortsProtocol(t *testing.T) {
	var host, path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, path = request.Host, request.URL.Path
	}))
	defer server.Close()
	address, port := hostAndPort(t, server)
	httpProber := newProber(t, exporterconfig.HTTPProber, mockSettings(), "")
	if succeeded, _ := probeWith(t, httpProber, probeschedule.Target{Address: address, Port: port, Protocol: "http", Host: trustedHost}); !succeeded {
		t.Fatal("the check failed")
	}
	if host != trustedHost || path != "/mock-path" {
		t.Errorf("host %q, path %q", host, path)
	}
}

func TestAnHTTPCheckFailsOnAStatusCodeNotGiven(t *testing.T) {
	recorder := newServerNameRecorder(t, http.StatusServiceUnavailable)
	address, port := hostAndPort(t, recorder.server)
	httpProber := newProber(t, exporterconfig.HTTPProber, mockSettings(), trustedAuthorities(t, recorder.server))
	if succeeded, _ := probeWith(t, httpProber, probeschedule.Target{Address: address, Port: port, Protocol: "https", Host: trustedHost}); succeeded {
		t.Error("a 503 passed a check expecting 200")
	}
}

func TestAnHTTPCheckReadsABoundedBody(t *testing.T) {
	httpProber := newProber(t, exporterconfig.HTTPProber, mockSettings(), "")
	if httpProber.module.HTTP.BodySizeLimit != 1<<20 {
		t.Errorf("body size limit = %d", httpProber.module.HTTP.BodySizeLimit)
	}
}

// A tcp check connects to the address and port, nothing more.
func TestATCPCheckConnectsToTheAddressAndPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- struct{}{}
			connection.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	tcpProber := newProber(t, exporterconfig.TCPProber, mockSettings(), "")
	if succeeded, _ := probeWith(t, tcpProber, probeschedule.Target{Address: "127.0.0.1", Port: port, Protocol: "tcp"}); !succeeded {
		t.Error("the check failed")
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Error("the check made no connection")
	}
	listener.Close()
	if succeeded, _ := probeWith(t, tcpProber, probeschedule.Target{Address: "127.0.0.1", Port: port, Protocol: "tcp"}); succeeded {
		t.Error("a closed port passed")
	}
}

// A dns check asks its address and port, a DNS server, for the host's record of the run's type, over the port's protocol.
func TestADNSCheckAsksTheAddressForTheHostsRecord(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var asked []string
	server := &dns.Server{PacketConn: listener, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
		mutex.Lock()
		asked = append(asked, request.Question[0].Name+" "+dns.TypeToString[request.Question[0].Qtype])
		mutex.Unlock()
		answer := new(dns.Msg)
		answer.SetReply(request)
		record, _ := dns.NewRR(request.Question[0].Name + " 60 IN AAAA 2001:db8::1")
		answer.Answer = append(answer.Answer, record)
		writer.WriteMsg(answer)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	dnsProber := newProber(t, exporterconfig.DNSProber, mockSettings(), "")
	port := listener.LocalAddr().(*net.UDPAddr).Port
	target := probeschedule.Target{Address: "127.0.0.1", Port: port, Protocol: "udp", Host: "mock-name.example", RecordType: "AAAA"}
	if succeeded, _ := probeWith(t, dnsProber, target); !succeeded {
		t.Fatal("the check failed")
	}
	mutex.Lock()
	defer mutex.Unlock()
	if !slices.Equal(asked, []string{"mock-name.example. AAAA"}) {
		t.Errorf("asked = %v", asked)
	}
}

// A ping is blackbox's icmp prober over IPv4 first; it is not run here, as it needs a privilege the test may lack.
func TestAPingIsBlackboxsICMPProber(t *testing.T) {
	pingProber := newProber(t, exporterconfig.PingProber, mockSettings(), "")
	if pingProber.module.Prober != "icmp" || pingProber.module.ICMP.IPProtocol != "ip4" || pingProber.module.Timeout != 3*time.Second {
		t.Errorf("module = %+v", pingProber.module)
	}
}

func TestAProberBlackboxCannotCarryOutIsRefused(t *testing.T) {
	_, err := NewBlackboxProber(exporterconfig.DomainRegistrationProber, mockSettings(), "")
	if err == nil || err.Error() != "blackbox_exporter carries out no prober `domain_registration`" {
		t.Errorf("err = %v", err)
	}
}
