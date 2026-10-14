package upstreamproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseEgressUsesLocationNotCloudflareDatacenter(t *testing.T) {
	for _, ip := range []string{"203.0.113.5", "2001:db8::5"} {
		r := parseEgressTrace("ip=" + ip + "\nloc=GB\ncolo=HKG\n")
		if !r.Reachable || r.IP != ip || r.CountryCode != "GB" || r.Error != "" || r.CheckedAt.IsZero() {
			t.Fatalf("incorrect egress: %+v", r)
		}
	}
}

func TestParseEgressRejectsMissingAndInvalidEvidence(t *testing.T) {
	for _, body := range []string{"<html>login</html>", "ip=bad\nloc=GB", "ip=127.0.0.1\nloc=GB", "ip=203.0.113.5\nloc=XX", "ip=203.0.113.5\nloc=T1", "ip=203.0.113.5\nloc=GB\nloc=US"} {
		if r := parseEgressTrace(body); r.Error == "" || r.CountryCode != "" {
			t.Fatalf("invalid evidence accepted: %+v", r)
		}
	}
}

func TestEgressHTTPSActuallyUsesSOCKS5Connect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "www.cloudflare.com" || r.URL.Path != "/cdn-cgi/trace" {
			t.Errorf("unexpected request: %s %s", r.Host, r.URL.Path)
		}
		_, _ = io.WriteString(w, "ip=203.0.113.5\nloc=GB\ncolo=HKG\n")
	}))
	defer origin.Close()
	destination := make(chan string, 1)
	proxy := startProbeServer(t, func(conn net.Conn) {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		greeting := readBytes(t, conn, 2)
		readBytes(t, conn, int(greeting[1]))
		writeBytes(t, conn, []byte{5, 0})
		header := readBytes(t, conn, 5)
		if header[1] != 1 || header[3] != 3 {
			t.Error("must use CONNECT with remote DNS", header)
			return
		}
		host := string(readBytes(t, conn, int(header[4])))
		port := binary.BigEndian.Uint16(readBytes(t, conn, 2))
		if port != 443 {
			t.Error("wrong destination port", port)
		}
		destination <- host
		upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		writeBytes(t, conn, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	})
	transport, err := egressTransport(ProbeConfig{ProxyAddr: proxy})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	result := probeEgress(context.Background(), &http.Client{Transport: transport, Timeout: 2 * time.Second})
	if result.Error != "" || result.CountryCode != "GB" {
		t.Fatalf("proxy egress failed: %+v", result)
	}
	if got := <-destination; got != "www.cloudflare.com" {
		t.Fatal("proxy received wrong destination", got)
	}
}

func TestEgressAuthenticationFailureDoesNotFallBackToDirect(t *testing.T) {
	addr := startProbeServer(t, func(conn net.Conn) {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		header := readBytes(t, conn, 2)
		readBytes(t, conn, int(header[1]))
		writeBytes(t, conn, []byte{5, 255})
	})
	result := probeSOCKS5Egress(context.Background(), ProbeConfig{ProxyAddr: addr, Username: "private-user", Password: "private-pass", Timeout: time.Second})
	if result.Reachable || result.IP != "" || result.Error == "" {
		t.Fatalf("rejected proxy reported egress: %+v", result)
	}
	if strings.Contains(result.Error, "private-user") || strings.Contains(result.Error, "private-pass") {
		t.Fatal("proxy credentials leaked")
	}
}

type traceResponseClient struct {
	status int
	body   string
}

func (c traceResponseClient) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body))}, nil
}

func TestEgressHTTPFailuresAreExplicit(t *testing.T) {
	for _, client := range []traceResponseClient{{503, "ip=203.0.113.5\nloc=GB"}, {302, "redirect"}, {200, strings.Repeat("x", maxTraceBytes+1)}} {
		if r := probeEgress(context.Background(), client); r.Error == "" || r.Reachable {
			t.Fatalf("bad HTTP response accepted: %+v", r)
		}
	}
}
