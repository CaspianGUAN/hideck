package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/yibaiba/hideck/internal/upstreamproxy"
)

func TestUpstreamProxySaveResultKeepsDNSFailureExplicit(t *testing.T) {
	result := upstreamproxy.ProbeResult{
		Reachable:      true,
		HandshakeOK:    true,
		UDPAssociateOK: true,
		Stage:          upstreamproxy.ProbeStageUDPRelay,
		Diagnosis:      "代理接受 UDP Associate，但公共 DNS UDP 数据无法完成往返",
	}

	status, message := upstreamProxySaveResult("前置代理已保存", result, errors.New("UDP timeout"))

	if status != "warning" {
		t.Fatalf("status = %q, want warning", status)
	}
	if !strings.Contains(message, result.Diagnosis) || !strings.Contains(message, "ePDG/IKE") {
		t.Fatalf("warning does not expose probe scope and failure: %q", message)
	}
}

func TestUpstreamProxySaveResultReportsFullSuccess(t *testing.T) {
	status, message := upstreamProxySaveResult("前置代理已更新", upstreamproxy.ProbeResult{}, nil)
	if status != "ok" || message != "前置代理已更新" {
		t.Fatalf("unexpected success response: status=%q message=%q", status, message)
	}
}

func TestUpstreamProxyPersistenceOnlyBlocksAssociationFailures(t *testing.T) {
	probeErr := errors.New("probe failed")
	associationReady := upstreamproxy.ProbeResult{
		Reachable:      true,
		HandshakeOK:    true,
		UDPAssociateOK: true,
	}
	if upstreamProxyProbeBlocksPersistence(associationReady, probeErr) {
		t.Fatal("public DNS failure must not block a valid UDP association")
	}
	if !upstreamProxyProbeBlocksPersistence(upstreamproxy.ProbeResult{}, probeErr) {
		t.Fatal("SOCKS5 association failure must block persistence")
	}
	if upstreamProxyProbeBlocksPersistence(upstreamproxy.ProbeResult{}, nil) {
		t.Fatal("successful probe must not block persistence")
	}
}

func TestUpstreamProxyReportsUnknownEgressWithoutBlockingPersistence(t *testing.T) {
	result := upstreamproxy.ProbeResult{Egress: &upstreamproxy.EgressProbe{Error: "出口检测超时"}}
	status, message := upstreamProxySaveResult("已保存", result, nil)
	if status != "warning" || !strings.Contains(message, "出口检测超时") {
		t.Fatalf("missing egress warning: %s %s", status, message)
	}
	if upstreamProxyProbeBlocksPersistence(result, nil) {
		t.Fatal("HTTPS diagnostics must not gate IMS routing or proxy persistence")
	}
}
