package imscore

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/internal/smscodec"
)

func listenProtectedTestUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func startProtectedTestUDP(t *testing.T, s *Service) (*protectedUDPTransport, *net.UDPConn, *net.UDPConn) {
	t.Helper()
	remoteC, remoteS := listenProtectedTestUDP(t), listenProtectedTestUDP(t)
	client, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	s.mu.Lock()
	s.cfg.LocalIP = net.IPv4(127, 0, 0, 1)
	s.cfg.LocalAddr = s.cfg.LocalIP.String()
	s.cfg.IMSNetwork = &captureIPSecNetwork{SystemIMSNetwork: NewSystemIMSNetwork(s.cfg.LocalIP)}
	s.registrationRemote = cloneUDPAddr(remoteS.LocalAddr().(*net.UDPAddr))
	s.externalTransport = false
	s.protectedClientPort, s.protectedServerPort = tcpPort(client.Addr()), tcpPort(server.Addr())
	s.mu.Unlock()
	reserved, err := s.reserveProtectedUDPPorts(server, client)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.securityServerIO = reserved
	s.mu.Unlock()
	err = s.startProtectedUDP(
		securityMechanism{PortC: uint16(tcpPort(client.Addr())), PortS: uint16(tcpPort(server.Addr()))},
		securityMechanism{PortC: uint16(remoteC.LocalAddr().(*net.UDPAddr).Port), PortS: uint16(remoteS.LocalAddr().(*net.UDPAddr).Port)},
	)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	path := s.protectedUDP
	s.mu.RUnlock()
	return path, remoteC, remoteS
}

func readProtectedTestUDP(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64*1024)
	n, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	return string(buffer[:n])
}

func startProtectedTestTCPOutbound(t *testing.T, s *Service) <-chan string {
	t.Helper()
	serviceConn, networkConn := net.Pipe()
	t.Cleanup(func() { _ = serviceConn.Close(); _ = networkConn.Close() })
	s.mu.Lock()
	s.registrationTCP = serviceConn
	s.registrationTCPProtected = true
	s.registrationTransport = "tcp"
	s.mu.Unlock()
	outbound := make(chan string, 2)
	go func() {
		reader := bufio.NewReader(networkConn)
		for {
			request, err := readSIPStreamMessage(reader)
			if err != nil {
				return
			}
			outbound <- request
			s.transport.DeliverResponse(registerResponseForRequest(request, 202, nil))
		}
	}()
	return outbound
}

func TestProtectedUDPSMSRepliesAndDeduplicates(t *testing.T) {
	s, subscriber, _ := newInboundSMSTestService(t)
	tcpOutbound := startProtectedTestTCPOutbound(t, s)
	path, remoteC, remoteS := startProtectedTestUDP(t, s)
	raw := inboundSMSRequest(t, imsSMSContentType, inboundRPData(t, 0x43, "+447700900123", "UDP delivery"))
	raw = strings.ReplaceAll(raw, "SIP/2.0/TCP", "SIP/2.0/UDP")
	if _, err := remoteC.WriteToUDP([]byte(raw), path.server.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	if response := readProtectedTestUDP(t, remoteS); !strings.HasPrefix(response, "SIP/2.0 200") {
		t.Fatalf("SIP reply = %s", response)
	}
	report := waitForOutboundSMSControl(t, tcpOutbound)
	body, err := rawSIPBody(report)
	if err != nil || string(body) != string(smscodec.BuildRPAck(0x43)) {
		t.Fatalf("RP report = %x, %v", body, err)
	}
	if !strings.HasPrefix(sipHeaderValue(report, "Via"), "SIP/2.0/TCP ") {
		t.Fatal("RP report did not use the registered TCP flow")
	}
	waitForProtectedRPAck(t, s, "TCP", 1)
	select {
	case <-subscriber.events:
	case <-time.After(time.Second):
		t.Fatal("SMS not delivered")
	}
	if err := s.dispatchProtectedUDP(path, path.remoteClient, raw); err != nil {
		t.Fatal(err)
	}
	if reply := readProtectedTestUDP(t, remoteS); !strings.HasPrefix(reply, "SIP/2.0 200") {
		t.Fatal("duplicate was not acknowledged")
	}
	select {
	case <-subscriber.events:
		t.Fatal("duplicate SMS delivery")
	default:
	}
}

func waitForProtectedRPAck(t *testing.T, s *Service, wantTransport string, wantCount int64) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		s.lastMTAckMu.Lock()
		transport, failure := s.lastMTAckTransport, s.lastMTAckErr
		s.lastMTAckMu.Unlock()
		if transport == wantTransport && failure == "" && s.mtAckSendOK.Load() == wantCount {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("RP-ACK was not accepted: transport=%q error=%q ok=%d failed=%d", transport, failure, s.mtAckSendOK.Load(), s.mtAckSendErr.Load())
		}
	}
}

func TestProtectedUDPProofAfterTCPFailureCancelsRecovery(t *testing.T) {
	s := singleCandidateReplacement(t)
	path, _, remoteS := startProtectedTestUDP(t, s)
	peer := &protectedUDPPeer{service: s, path: path}
	s.recordCurrentDownlinkRequest(peer, s.captureDownlinkCheckpoint())
	conn := newRecoveryCompletionPortS(t)
	s.recordPortSOpened(conn, time.Now())
	s.recordPortSClosed(conn, syscall.ETIMEDOUT, time.Now())
	if s.udpDownlinkProven.Load() {
		t.Fatal("historical UDP proof hid a new transport failure")
	}
	s.armPortSTimeoutFailover()
	if err := s.dispatchProtectedUDP(path, path.remoteClient, optionsRequest("after-tcp-failure")); err != nil {
		t.Fatal(err)
	}
	_ = readProtectedTestUDP(t, remoteS)
	if !s.portSTimeoutDownlinkProven() || s.pendingPortSTimeoutFailover().registrar != "" {
		t.Fatal("current UDP request left timeout recovery pending")
	}
}

func TestProtectedUDPProofCancelsValidationButRetiredPacketsCannot(t *testing.T) {
	s := singleCandidateReplacement(t)
	path, _, remoteS := startProtectedTestUDP(t, s)
	watch := expireReplacementWatchForTest(t, s)
	checkpoint := s.captureDownlinkCheckpoint()
	if err := s.dispatchProtectedUDP(path, path.remoteClient, optionsRequest("protected-udp")); err != nil {
		t.Fatal(err)
	}
	_ = readProtectedTestUDP(t, remoteS)
	s.replacementDownlinkWatchFired(watch)
	if s.registrarPenalties.recoveryInProgress() || !s.udpDownlinkProven.Load() || len(s.RegistrationErrors()) != 0 {
		t.Fatal("UDP request did not validate the current downlink")
	}
	if _, _, committed := s.commitPortSFailover(path.registrar, portSFailoverCause{reason: "port_s_peer_reset"}); committed {
		t.Fatal("already queued RST switched away from the proven UDP downlink")
	}
	s.closeProtectedUDP()
	s.resetPortSRecoveryKnowledge()
	s.recordCurrentDownlinkRequest(&protectedUDPPeer{service: s, path: path}, checkpoint)
	if s.udpDownlinkProven.Load() {
		t.Fatal("retired UDP evidence validated a replacement")
	}
	if err := s.dispatchProtectedUDP(path, path.remoteClient, optionsRequest("retired-udp")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("retired UDP dispatch = %v", err)
	}
}

func TestProtectedUDPShutdownWhileRegisterLockHeld(t *testing.T) {
	s := singleCandidateReplacement(t)
	_, _, _ = startProtectedTestUDP(t, s)
	done := make(chan struct{})
	go func() {
		s.registerMu.Lock()
		defer s.registerMu.Unlock()
		s.StopCurrent()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UDP reader deadlocked shutdown against REGISTER")
	}
}

func TestProtectedUDPRejectsWrongPeerAndReportsReceiverFailure(t *testing.T) {
	s := singleCandidateReplacement(t)
	path, _, _ := startProtectedTestUDP(t, s)
	if err := s.dispatchProtectedUDP(path, path.remoteServer, optionsRequest("wrong-port")); err == nil {
		t.Fatal("unnegotiated endpoint accepted")
	}
	if s.udpDownlinkProven.Load() {
		t.Fatal("rejected packet counted as downlink")
	}
	_ = path.server.Close()
	select {
	case <-s.RegistrationErrors():
	case <-time.After(time.Second):
		t.Fatal("receiver failure was hidden")
	}
}

func TestProtectedRegistrationUDPFallbackSendsFromPortCToPortS(t *testing.T) {
	s, _, _ := newInboundSMSTestService(t)
	_, _, remoteS := startProtectedTestUDP(t, s)
	if err := s.activateProtectedRegistrationUDP(); err != nil {
		t.Fatal(err)
	}
	if got := s.registerRequestTransport(true); got != "udp" {
		t.Fatalf("protected REGISTER transport = %q, want udp", got)
	}
	request := "REGISTER sip:ims.example SIP/2.0\r\nCSeq: 3 REGISTER\r\nContent-Length: 0\r\n\r\n"
	if err := s.transport.Send(request); err != nil {
		t.Fatal(err)
	}
	if got := readProtectedTestUDP(t, remoteS); got != request {
		t.Fatalf("P-CSCF port-s received %q", got)
	}
	s.closeProtectedUDP()
	if got := s.registerRequestTransport(true); got != "tcp" {
		t.Fatalf("protected REGISTER transport after close = %q, want tcp", got)
	}
}
