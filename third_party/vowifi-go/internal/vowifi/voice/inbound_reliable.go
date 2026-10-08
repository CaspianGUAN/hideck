package voice

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imscore"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imsendpoint"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
)

const inboundReliableAnswerTimeout = 4 * time.Second

func inboundEarlyAnswerRequested(request imscore.InboundVoiceRequest) (reliable, precondition bool) {
	require := strings.TrimSpace(request.Require)
	if require == "" && request.Request != nil {
		require = requestHeaderValue(request.Request, "Require")
	}
	supported := strings.TrimSpace(request.Supported)
	if supported == "" && request.Request != nil {
		supported = requestHeaderValue(request.Request, "Supported")
	}
	precondition = sipHeaderHasToken(require, "precondition") || sdpHasPreconditions(string(request.Body))
	// Supported: 100rel alone keeps the existing 180 then 200 path. A reliable
	// answer is required when the INVITE demands 100rel, and when preconditions
	// are present on an endpoint that offered 100rel. An iPhone on AIS answers
	// the same INVITE with 180 then 200.
	reliable = sipHeaderHasToken(require, "100rel") || (sipHeaderHasToken(supported, "100rel") && precondition)
	return reliable, precondition
}

func (c *Call) setInboundEarlyAnswer(reliable, precondition bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.inboundReliable100rel = reliable
	c.inboundPreconditions = precondition
	c.mu.Unlock()
}

func (c *Call) inboundEarlyAnswerMode() (reliable, precondition bool) {
	if c == nil {
		return false, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inboundReliable100rel, c.inboundPreconditions
}

func (c *Call) armInboundPRACK(rseq uint32) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Timers.RSeq = rseq
	c.reliableProvisional = true
	ch := make(chan struct{})
	c.inboundPRACK = ch
	return ch
}

func (c *Call) noteInboundPRACK(rseq uint32) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inboundPRACK == nil {
		return false
	}
	if rseq != 0 && c.Timers.RSeq != 0 && rseq != c.Timers.RSeq {
		return false
	}
	close(c.inboundPRACK)
	c.inboundPRACK = nil
	return true
}

func (c *Call) armInboundPreconditionWait() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inboundPreconditionPending = true
	ch := make(chan struct{})
	c.inboundPreconditionWait = ch
	return ch
}

func (c *Call) awaitingInboundPreconditions() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inboundPreconditionPending
}

func (c *Call) signalInboundPreconditions() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inboundPreconditionPending = false
	if c.inboundPreconditionWait == nil {
		return
	}
	close(c.inboundPreconditionWait)
	c.inboundPreconditionWait = nil
}

func prackRequestRSeq(request imscore.InboundVoiceRequest) uint32 {
	if request.Request == nil {
		return 0
	}
	return rackRSeq(requestHeaderValue(request.Request, "RAck"))
}

func rackRSeq(rack string) uint32 {
	fields := strings.Fields(strings.TrimSpace(rack))
	if len(fields) == 0 {
		return 0
	}
	parsed, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return 0
	}
	return uint32(parsed)
}

func (a *Agent) observeInboundPRACK(event imsendpoint.Event) {
	call := a.callForIMSEvent(event)
	if call == nil {
		return
	}
	rseq := uint32(0)
	if event.Request != nil {
		rseq = rackRSeq(requestHeaderValue(event.Request, "RAck"))
	}
	if call.noteInboundPRACK(rseq) {
		logging.Info("IMS 来电收到 PRACK", "call_id", call.CallID(), "rseq", rseq)
	}
}

func (a *Agent) sendInboundEarlyAnswer(
	call *Call,
	sdp string,
	reliable, precondition bool,
) (<-chan struct{}, <-chan struct{}, error) {
	var require []string
	var prackCh, updateCh <-chan struct{}
	rseq := uint32(0)
	if reliable {
		rseq = 1
		prackCh = call.armInboundPRACK(rseq)
		require = append(require, "100rel")
	}
	if precondition {
		require = append(require, "precondition")
		if inboundOffererQoSPending(call.remoteSDPValue()) {
			updateCh = call.armInboundPreconditionWait()
		}
	}
	requireValue := strings.Join(require, ", ")
	if err := a.writeInboundEarlyAnswer(call, sdp, requireValue, rseq); err != nil {
		return nil, nil, err
	}
	logging.Info("IMS 来电已发送 183", "call_id", call.CallID(), "require", requireValue, "rseq", rseq)
	return prackCh, updateCh, nil
}

func (a *Agent) writeInboundEarlyAnswer(call *Call, sdp, require string, rseq uint32) error {
	if a == nil || a.dialog == nil || call == nil {
		return errors.New("voice: dialog controller is unavailable")
	}
	invite, request := call.serverInviteContext()
	if invite != nil && request != nil {
		response := call.BuildResponseWithSDP(183, "Session Progress", []byte(sdp))
		if require != "" {
			response.AppendHeader(sip.NewHeader("Require", require))
		}
		if rseq != 0 {
			response.AppendHeader(sip.NewHeader("RSeq", strconv.FormatUint(uint64(rseq), 10)))
		}
		if contact := a.dialog.Context().CachedContactHdr; contact != nil {
			response.RemoveHeader("Contact")
			response.AppendHeader(contact.Clone())
		}
		logging.Info("IMS 来电 183 原文", "call_id", call.CallID(), "sip", sipLogValue(response.String()))
		return a.dialog.RejectServerInvite(
			context.Background(), a.deviceID, invite,
			imsendpoint.ServerInviteRejectOptions{Response: response, Code: 183, Reason: "Session Progress"},
		)
	}
	responder := call.inboundResponseWriter()
	if responder == nil {
		return errInboundResponseUnavailable
	}
	response := imscore.InboundVoiceResponse{
		StatusCode: 183, ContentType: "application/sdp", Body: []byte(sdp),
		ToTag: call.inboundLocalTagValue(), Require: require,
	}
	if rseq != 0 {
		response.RSeq = strconv.FormatUint(uint64(rseq), 10)
	}
	if profile, err := a.registeredDialogProfile(); err == nil {
		response.Contact = profile.ContactURI
	}
	return responder.Respond(response)
}

func waitInboundAnswerGates(call *Call, prack, update <-chan struct{}) error {
	if prack == nil && update == nil {
		return nil
	}
	timer := time.NewTimer(inboundReliableAnswerTimeout)
	defer timer.Stop()
	for prack != nil || update != nil {
		select {
		case <-prack:
			prack = nil
		case <-update:
			update = nil
		case <-call.Done:
			return errors.New("voice: inbound call ended before the reliable answer")
		case <-timer.C:
			return errors.New("voice: inbound reliable answer timed out")
		}
	}
	return nil
}

func (a *Agent) acceptInboundPreconditionUpdateLocked(
	request imscore.InboundVoiceRequest,
	call *Call,
) (imscore.InboundVoiceResult, error) {
	if request.Responder == nil || !isVoiceSDPContentType(request.ContentType) {
		return voiceResult(488), nil
	}
	if err := validateSDPMediaEndpoint(request.Body, "IMS precondition UPDATE"); err != nil {
		return voiceResult(488), nil
	}
	rewritten, err := ProcessIncomingIMSSDP(call, request.Body, clientRelayIP)
	if err != nil {
		return voiceResult(488), nil
	}
	call.setRemoteSDP(string(request.Body), string(rewritten))
	clientSDP, imsAnswer := call.localSDPs()
	imsAnswer = refreshTerminatingRemoteQoS(imsAnswer, sdpQoSCurrent(string(request.Body), "local"))
	call.setLocalSDP(clientSDP, imsAnswer)
	a.applyCallPreconditions(call, string(request.Body))
	if err := request.Responder.Respond(a.voiceSDPResponse(call, 200, imsAnswer)); err != nil {
		return voiceResult(0), err
	}
	if !inboundOffererQoSPending(string(request.Body)) {
		call.signalInboundPreconditions()
	}
	return voiceResult(0), nil
}
