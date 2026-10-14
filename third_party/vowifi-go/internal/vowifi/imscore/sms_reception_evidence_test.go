package imscore

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
)

func registrationSMSService(t *testing.T) (*Service, net.Conn) {
	t.Helper()
	s := newProtectedKeepaliveTestService(t)
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
	s.registrationTCP, s.registrationTCPProtected = conn, true
	s.registrar = "pcscf.example:5060"
	return s, conn
}

func TestRegistrationSMSReadinessWithoutPortS(t *testing.T) {
	s, conn := registrationSMSService(t)
	if got := s.SMSReadiness(); !got.ReceiverPending || got.Ready || got.HealthReady || !got.MOReady {
		t.Fatalf("unverified reception = %+v", got)
	}
	// Any SIP request or a REGISTER success is not proof of MT SMS handling.
	s.recordCurrentDownlinkRequest(conn, s.captureDownlinkCheckpoint())
	if got := s.SMSReadiness(); got.Ready || !got.ReceiverPending {
		t.Fatalf("generic request = %+v", got)
	}
	s.recordRegistrationSMS(conn, s.captureSMSReceptionCheckpoint())
	if got := s.SMSReadiness(); !got.Ready || !got.HealthReady || got.ReceiverPending || got.Reason != smsReadyReasonRegistrationFlow {
		t.Fatalf("verified reception = %+v", got)
	}
	if s.capturePortSSession().generation != 0 {
		t.Fatal("test unexpectedly established port-s")
	}
}

func TestRegistrationSMSRejectsStaleOrUnprotectedEvidence(t *testing.T) {
	for _, scenario := range []string{"old_peer", "old_generation", "other_registrar", "unprotected", "nil_peer"} {
		t.Run(scenario, func(t *testing.T) {
			s, conn := registrationSMSService(t)
			checkpoint := s.captureSMSReceptionCheckpoint()
			switch scenario {
			case "old_peer":
				replacement, peer := net.Pipe()
				t.Cleanup(func() { _ = replacement.Close(); _ = peer.Close() })
				s.registrationTCP = replacement
			case "old_generation":
				s.resetPortSRecoveryKnowledge()
			case "other_registrar":
				s.registrar = "other.example:5060"
			case "unprotected":
				s.registrationTCPProtected = false
			case "nil_peer":
				conn = nil
			}
			s.recordRegistrationSMS(conn, checkpoint)
			if got := s.SMSReadiness(); got.Ready || got.HealthReady {
				t.Fatalf("stale evidence = %+v", got)
			}
		})
	}
}

func TestRegistrationSMSFailuresInvalidateProof(t *testing.T) {
	for _, failure := range []string{"rst", "timeout", "report", "replace"} {
		t.Run(failure, func(t *testing.T) {
			s, conn := registrationSMSService(t)
			checkpoint := s.captureSMSReceptionCheckpoint()
			s.recordRegistrationSMS(conn, checkpoint)
			switch failure {
			case "rst", "timeout":
				push, peer := net.Pipe()
				t.Cleanup(func() { _ = push.Close(); _ = peer.Close() })
				s.recordPortSOpened(push, time.Now())
				closeErr := errors.New("read tcp: connection reset by peer")
				if failure == "timeout" {
					closeErr = errors.New("read tcp: operation timed out")
				}
				s.recordPortSClosed(push, closeErr, time.Now())
			case "report":
				s.recordRegistrationSMSReportFailure(conn, checkpoint)
			case "replace":
				s.resetPortSRecoveryKnowledge()
			}
			s.recordRegistrationSMS(conn, checkpoint)
			if got := s.SMSReadiness(); got.Ready || got.HealthReady || (failure != "replace" && got.ReceiverPending) {
				t.Fatalf("old proof hid %s: %+v", failure, got)
			}
		})
	}
}

func TestRegistrationSMSReportFailureDoesNotAffectReplacement(t *testing.T) {
	s, conn := registrationSMSService(t)
	old := s.captureSMSReceptionCheckpoint()
	s.resetPortSRecoveryKnowledge()
	s.recordRegistrationSMS(conn, s.captureSMSReceptionCheckpoint())
	s.recordRegistrationSMSReportFailure(conn, old)
	if got := s.SMSReadiness(); !got.Ready {
		t.Fatalf("old report failed new path: %+v", got)
	}
}

func TestRegistrationSMSRejectsInvalidDataAndFailedReply(t *testing.T) {
	s, conn := registrationSMSService(t)
	// No external I/O: use an inbound handler result to exercise dispatch and
	// its reply-success boundary. Only validated RP-DATA sets smsReceived.
	raw := inboundSMSRequest(t, imsSMSContentType, inboundRPData(t, 33, "+15551234567", "test"))
	result, err := s.handleInboundSMSFromPeer(raw, conn)
	if err != nil || !result.smsReceived {
		t.Fatalf("valid RP-DATA result=%+v err=%v", result, err)
	}
	invalid := inboundSMSRequest(t, "text/plain", []byte("not an IMS SMS"))
	result, err = s.handleInboundSMSFromPeer(invalid, conn)
	if result.smsReceived {
		t.Fatalf("invalid MESSAGE became evidence: %+v, %v", result, err)
	}
	// A parsed MESSAGE whose response fails must not establish readiness.
	request, err := sip.ParseMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	err = s.dispatchInboundSIPMessageWithPeer(request, raw, func(string) error { return io.ErrClosedPipe }, conn)
	if err == nil {
		t.Fatal("reply failure hidden")
	}
	if s.SMSReadiness().Ready {
		t.Fatal("failed reply marked receiver ready")
	}
}

func TestRegistrationSMSEvidenceFromSuccessfulMTDispatch(t *testing.T) {
	s := newProtectedKeepaliveTestService(t)
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
	s.registrationTCP, s.registrationTCPProtected = conn, true
	writes, readErr := make(chan string, 2), make(chan error, 1)
	go (peerSMSCapture{service: s, peer: peer, writes: writes, readErr: readErr}).run()
	raw := inboundSMSRequest(t, imsSMSContentType, inboundRPData(t, 34, "+15551234567", "test"))
	request, err := parseSIPMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchInboundSIPMessageWithPeer(request, raw,
		func(response string) error { return s.writeSIPStream(conn, response) }, conn); err != nil {
		t.Fatal(err)
	}
	waitForPeerSIPWrite(t, writes, readErr)
	waitForPeerSIPWrite(t, writes, readErr)
	if got := s.SMSReadiness(); !got.Ready || got.ReceiverPending || got.Reason != smsReadyReasonRegistrationFlow {
		t.Fatalf("successful MT did not verify registered flow: %+v", got)
	}
}
