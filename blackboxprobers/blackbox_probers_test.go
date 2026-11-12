package blackboxprobers

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/client_golang/prometheus"
	promconfig "github.com/prometheus/common/config"

	"github.com/litebitus/probe-exporter/probeschedule"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func probeWith(t *testing.T, module blackboxconfig.Module, target probeschedule.Target) bool {
	t.Helper()
	moduleProber, err := NewModuleProber(module)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return moduleProber.Probe(ctx, target, prometheus.NewRegistry(), quietLogger())
}

// serverNameRecorder is a TLS listener that records the server name each handshake sends.
type serverNameRecorder struct {
	server *httptest.Server
	mutex  sync.Mutex
	names  []string
}

func newServerNameRecorder(t *testing.T) *serverNameRecorder {
	recorder := &serverNameRecorder{server: httptest.NewUnstartedServer(http.NotFoundHandler())}
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

func (recorder *serverNameRecorder) sent() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return slices.Clone(recorder.names)
}

// A tcp target's name is sent as the TLS server name, while the run connects to its address.
func TestATCPTargetsNameIsTheTLSServerName(t *testing.T) {
	recorder := newServerNameRecorder(t)
	module := blackboxconfig.DefaultModule
	module.Prober = "tcp"
	module.TCP.TLS = true
	module.TCP.TLSConfig = promconfig.TLSConfig{InsecureSkipVerify: true}
	address := strings.TrimPrefix(recorder.server.URL, "https://")
	if !probeWith(t, module, probeschedule.Target{Name: "mock-host.example", Address: address}) {
		t.Fatal("the run failed")
	}
	if sent := recorder.sent(); len(sent) != 1 || sent[0] != "mock-host.example" {
		t.Errorf("server names sent = %v", sent)
	}
}

// A grpc target's name is sent as the TLS server name; the listener is no gRPC server, so only the handshake is checked.
func TestAGRPCTargetsNameIsTheTLSServerName(t *testing.T) {
	recorder := newServerNameRecorder(t)
	module := blackboxconfig.DefaultModule
	module.Prober = "grpc"
	module.GRPC = blackboxconfig.DefaultGRPCProbe
	module.GRPC.TLS = true
	module.GRPC.TLSConfig = promconfig.TLSConfig{InsecureSkipVerify: true}
	address := strings.TrimPrefix(recorder.server.URL, "https://")
	probeWith(t, module, probeschedule.Target{Name: "mock-host.example", Address: address})
	if sent := recorder.sent(); len(sent) == 0 || sent[0] != "mock-host.example" {
		t.Errorf("server names sent = %v", sent)
	}
}

// An http target's name is sent as the `Host` header, the settings' own headers kept and the settings themselves left unchanged.
func TestAnHTTPTargetsNameIsTheHostHeader(t *testing.T) {
	var host, mockHeader string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, mockHeader = request.Host, request.Header.Get("X-Mock")
	}))
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "http"
	module.HTTP.Headers = map[string]string{"X-Mock": "mock-value"}
	if !probeWith(t, module, probeschedule.Target{Name: "mock-host.example", Address: server.URL}) {
		t.Fatal("the run failed")
	}
	if host != "mock-host.example" || mockHeader != "mock-value" {
		t.Errorf("host = %q, X-Mock = %q", host, mockHeader)
	}
	if _, changed := module.HTTP.Headers["Host"]; changed {
		t.Error("the module gained the target's Host header")
	}
}

func TestHTTPSettingsWithoutHeadersStillSendTheName(t *testing.T) {
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { host = request.Host }))
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "http"
	module.HTTP.Headers = nil
	probeWith(t, module, probeschedule.Target{Name: "mock-host.example", Address: server.URL})
	if host != "mock-host.example" {
		t.Errorf("host = %q", host)
	}
}

// A dns target's name is what its address, a DNS server, is asked.
func TestADNSTargetsNameIsTheNameAsked(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var asked []string
	server := &dns.Server{PacketConn: listener, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
		mutex.Lock()
		asked = append(asked, request.Question[0].Name)
		mutex.Unlock()
		answer := new(dns.Msg)
		answer.SetReply(request)
		record, _ := dns.NewRR(request.Question[0].Name + " 60 IN A 192.0.2.1")
		answer.Answer = append(answer.Answer, record)
		writer.WriteMsg(answer)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	module := blackboxconfig.DefaultModule
	module.Prober = "dns"
	module.DNS.TransportProtocol = "udp"
	module.DNS.IPProtocol = "ip4"
	module.DNS.QueryType = "A"
	if !probeWith(t, module, probeschedule.Target{Name: "mock-name.example", Address: listener.LocalAddr().String()}) {
		t.Fatal("the run failed")
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(asked) != 1 || asked[0] != "mock-name.example." {
		t.Errorf("asked = %v", asked)
	}
}

func TestAModuleNamingAProberBlackboxLacksIsRefused(t *testing.T) {
	_, err := NewModuleProber(blackboxconfig.Module{Prober: "mock"})
	if err == nil || err.Error() != "blackbox_exporter has no prober `mock`" {
		t.Errorf("err = %v", err)
	}
}
