package blackboxprobers

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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

// A tcp target is dialed as its hostname: the TLS server name the probe sends is the hostname, not the address.
func TestATCPTargetIsDialedAsItsHostname(t *testing.T) {
	var mutex sync.Mutex
	var serverNames []string
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		mutex.Lock()
		defer mutex.Unlock()
		serverNames = append(serverNames, hello.ServerName)
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "tcp"
	module.TCP.TLS = true
	module.TCP.TLSConfig = promconfig.TLSConfig{InsecureSkipVerify: true}
	address := strings.TrimPrefix(server.URL, "https://")
	if !probeWith(t, module, probeschedule.Target{Address: address, Hostname: "mock-host.example"}) {
		t.Fatal("the probe failed")
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(serverNames) != 1 || serverNames[0] != "mock-host.example" {
		t.Errorf("server names sent = %v", serverNames)
	}
}

// An http target is asked for as its hostname, the module's own headers kept and the module itself left unchanged.
func TestAnHTTPTargetIsAskedForAsItsHostname(t *testing.T) {
	var host, mockHeader string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, mockHeader = request.Host, request.Header.Get("X-Mock")
	}))
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "http"
	module.HTTP.Headers = map[string]string{"X-Mock": "mock-value"}
	if !probeWith(t, module, probeschedule.Target{Address: server.URL, Hostname: "mock-host.example"}) {
		t.Fatal("the probe failed")
	}
	if host != "mock-host.example" || mockHeader != "mock-value" {
		t.Errorf("host = %q, X-Mock = %q", host, mockHeader)
	}
	if _, changed := module.HTTP.Headers["Host"]; changed {
		t.Error("the module gained the target's Host header")
	}
}

func TestAnHTTPTargetWithoutAHostnameIsAskedForAsItsAddress(t *testing.T) {
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { host = request.Host }))
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "http"
	if !probeWith(t, module, probeschedule.Target{Address: server.URL}) || host != strings.TrimPrefix(server.URL, "http://") {
		t.Errorf("host = %q", host)
	}
}

func TestAnHTTPModuleWithoutHeadersStillSendsTheHostname(t *testing.T) {
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { host = request.Host }))
	defer server.Close()
	module := blackboxconfig.DefaultModule
	module.Prober = "http"
	module.HTTP.Headers = nil
	probeWith(t, module, probeschedule.Target{Address: server.URL, Hostname: "mock-host.example"})
	if host != "mock-host.example" {
		t.Errorf("host = %q", host)
	}
}

// A dns target, a DNS server, is asked its query name.
func TestADNSTargetIsAskedItsQueryName(t *testing.T) {
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
	if !probeWith(t, module, probeschedule.Target{Address: listener.LocalAddr().String(), QueryName: "mock-name.example"}) {
		t.Fatal("the probe failed")
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
