package imscore

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/smscodec"
	"github.com/iniwex5/vowifi-go/internal/vowifi/events"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
	"github.com/iniwex5/vowifi-go/internal/vowifi/sipkit"
	"github.com/warthog618/sms/encoding/tpdu"
)

const (
	imsSMSContentType = "application/vnd.3gpp.sms"
	// 24.229 originating ICSI marking; 24.341 5.3.2.1 applies 24.229 to RP-ACK.
	// Missing these, originating iFC can deliver RP-ACK to an AS that 488s
	// unmatched In-Reply-To (24.341 5.3.3.4.1).
	imsSMSPreferredService  = "urn:urn-7:3gpp-service.ims.icsi.sms"
	imsSMSAcceptContact     = `*;+g.3gpp.smsip;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.sms"`
	rpCauseTemporaryFailure = byte(41)
	// TS 24.341 5.3.2.3 leaves the MT response to RFC 3428, which reserves
	// 202 for a relay that has not delivered end to end (7) and tells the
	// sender not to assume delivery on 202 (4). We are the final recipient,
	// so 200. The report still goes in a separate MESSAGE (5.3.2.4, B.6-7),
	// and 2xx carries no body (RFC 3428 7).
	inboundRPDataSIPStatus = 200
	inboundSMSAckTimeout   = 10 * time.Second
	inboundSMSFragmentTTL  = 3 * time.Minute
	// A carrier response arrived 35m37s late in production. One hour keeps a
	// bounded recovery margin without delaying the 3-minute user notification.
	inboundSMSLateReassemblyTTL = time.Hour
)

type inboundSMS struct {
	sender            string
	serviceCenter     string
	targetURI         string
	content           string
	timestamp         time.Time
	rpMR              byte
	concatRef         int
	refBits           int
	total             int
	partNo            int
	fragmentSessionID string
	msisdnLess        bool
	deliveryReportTo  string
	peerConn          net.Conn
}

type decodedInboundSMSRequest struct {
	rpdu []byte
	info smscodec.RPDUInfo
	xml  shortMessageInfo
	peer net.Conn
}

type inboundRPDataRequest struct {
	raw      string
	rpdu     []byte
	rpMR     byte
	xml      shortMessageInfo
	peerConn net.Conn
}

type inboundSMSProtocolFailure struct {
	raw         string
	status      int
	rpMR        byte
	sendRPError bool
	err         error
	peerConn    net.Conn
}

type fragmentLifecycleContext struct {
	TraceID, Device, Transport, CallID, Key string
	ArrivedAt                               time.Time
	Message                                 inboundSMS
}

func inboundAckHeaders(request *sip.Request) (string, string, string, string) {
	if request == nil {
		return "", "", "", ""
	}
	callID := sipkit.FirstHeaderValue(request, "Call-ID", false)
	assertedIdentity := sipkit.FirstHeaderValue(request, "P-Asserted-Identity", false)
	from := sipkit.FirstHeaderValue(request, "From", false)
	to := sipkit.FirstHeaderValue(request, "To", false)
	if strings.TrimSpace(to) == "" {
		to = sipkit.FirstHeaderValue(request, "P-Called-Party-ID", false)
	}
	if strings.TrimSpace(to) == "" {
		to = firstNonBlank(assertedIdentity, from)
	}
	return callID, assertedIdentity, from, to
}

func (s *Service) decodeInboundSMSRequest(raw string) (*decodedInboundSMSRequest, error) {
	contentType := rawSIPHeaderValue(raw, "Content-Type")
	if !isSupportedSMSContentType(contentType) {
		return nil, errUnsupportedSMSContentType
	}
	body, err := rawSIPBody(raw)
	if err != nil {
		return nil, err
	}
	payload, err := extractIMSSMSPayload(contentType, body)
	if err != nil {
		if errors.Is(err, errUnsupportedSMSContentType) {
			return nil, err
		}
		return nil, fmt.Errorf("decode RPDU body: %w", err)
	}
	decoded := &decodedInboundSMSRequest{
		rpdu: payload.rpdu, info: smscodec.ClassifyRPDU(payload.rpdu), xml: payload.xml,
	}
	if err := parseInboundRPDU(payload.rpdu); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func (s *Service) handleInboundSMS(raw string) (inboundSIPResult, error) {
	return s.handleInboundSMSFromPeer(raw, nil)
}

func (s *Service) handleInboundSMSFromPeer(raw string, peer net.Conn) (inboundSIPResult, error) {
	s.logInboundSMSProtocolTrace(raw)
	decoded, err := s.decodeInboundSMSRequest(raw)
	if decoded != nil {
		decoded.peer = peer
	}
	if err != nil && decoded == nil && !isSupportedSMSContentType(rawSIPHeaderValue(raw, "Content-Type")) {
		response, err := buildSIPRequestResponse(raw, 415)
		return inboundSIPResult{response: response}, err
	}
	if err != nil {
		info := smscodec.RPDUInfo{}
		if decoded != nil {
			info = decoded.info
		}
		return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
			raw: raw, status: 400, rpMR: info.MR,
			sendRPError: info.Kind == smscodec.RPDUKindData,
			err:         err, peerConn: peer,
		})
	}
	return s.routeDecodedInboundSMS(raw, decoded)
}

func (s *Service) routeDecodedInboundSMS(raw string, decoded *decodedInboundSMSRequest) (inboundSIPResult, error) {
	if decoded == nil {
		return s.inboundSMSProtocolError(raw, 400, 0, false, errors.New("empty decoded IMS SMS"))
	}
	info, rpdu := decoded.info, decoded.rpdu
	switch {
	case info.Kind == smscodec.RPDUKindData && info.RawType == 0x01:
		return s.handleInboundSMSData(raw, decoded)
	case info.Kind == smscodec.RPDUKindAck && info.RawType == 0x03:
		return s.handleInboundSMSReport(raw, info, "acked", "")
	case info.Kind == smscodec.RPDUKindError && info.RawType == 0x05:
		return s.handleInboundSMSReport(raw, info, "failed", rpErrorReason(rpdu, info.Cause))
	default:
		return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
			raw: raw, status: 400, rpMR: info.MR,
			err: fmt.Errorf("unsupported inbound RPDU type 0x%02x", info.RawType), peerConn: decoded.peer,
		})
	}
}

func rpErrorReason(rpdu []byte, cause int) string {
	reason := fmt.Sprintf("RP-ERROR cause %d", cause)
	details, err := smscodec.ParseRPErrorDetails(rpdu)
	if err != nil || len(details.UserData) == 0 {
		return reason
	}
	report := tpdu.TPDU{Direction: tpdu.MT}
	if err := report.UnmarshalBinary(details.UserData); err != nil || report.SmsType() != tpdu.SmsSubmitReport {
		return reason
	}
	return fmt.Sprintf("%s, SMS-SUBMIT-REPORT FCS 0x%02x", reason, report.FCS)
}

func (s *Service) handleInboundSMSReport(
	raw string,
	info smscodec.RPDUInfo,
	state, errorText string,
) (inboundSIPResult, error) {
	return s.handleInboundRPReport(raw, info, state, errorText)
}

func (s *Service) handleInboundSMSData(raw string, decoded *decodedInboundSMSRequest) (inboundSIPResult, error) {
	if decoded == nil {
		return s.inboundSMSProtocolError(raw, 400, 0, false, errors.New("empty decoded IMS SMS"))
	}
	return s.handleInboundRPDataRequest(inboundRPDataRequest{
		raw: raw, rpdu: decoded.rpdu, rpMR: decoded.info.MR,
		xml: decoded.xml, peerConn: decoded.peer,
	})
}

func (s *Service) handleInboundRPData(raw string, rpdu []byte, rpMR byte, xml shortMessageInfo) (inboundSIPResult, error) {
	return s.handleInboundRPDataRequest(inboundRPDataRequest{
		raw: raw, rpdu: rpdu, rpMR: rpMR, xml: xml,
	})
}

func (s *Service) handleInboundRPDataRequest(request inboundRPDataRequest) (inboundSIPResult, error) {
	_, _, _, payload, err := smscodec.ParseRPDataWithAddresses(request.rpdu)
	if err != nil {
		return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
			raw: request.raw, status: 400, rpMR: request.rpMR,
			sendRPError: true, err: err, peerConn: request.peerConn,
		})
	}
	if len(payload) > 0 && payload[0]&0x03 == 0x02 {
		return s.handleInboundTPStatusReportRequest(inboundTPStatusReportRequest{
			raw: request.raw, rpMR: request.rpMR,
			payload: payload, peerConn: request.peerConn,
		})
	}
	message, err := decodeInboundRPData(request.raw, request.rpdu)
	if err != nil {
		return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
			raw: request.raw, status: 400, rpMR: request.rpMR,
			sendRPError: true, err: err, peerConn: request.peerConn,
		})
	}
	message.peerConn = request.peerConn
	if smscodec.IsDummyMSISDN(message.sender) {
		if !hasMSISDNLessFeatureCaps(request.raw) {
			response, responseErr := buildSIPRequestResponse(request.raw, inboundRPDataSIPStatus)
			return inboundSIPResult{response: response}, responseErr
		}
		message.msisdnLess = true
		if from := strings.TrimSpace(request.xml.From); from != "" {
			message.sender = from
			message.deliveryReportTo = from
		}
	}
	s.logInboundSMSCorrelation(request.raw, message)
	response, err := buildSIPRequestResponse(request.raw, inboundRPDataSIPStatus)
	if err != nil {
		return inboundSIPResult{}, err
	}
	if s.smsMemoryIsFull() {
		s.rememberSMSMemoryDenied(request.raw)
		return inboundSIPResult{
			response: response,
			afterReply: func() {
				s.sendRPReportWithRetry(s.rpReportForInbound(request.raw, message, smscodec.BuildRPError(message.rpMR, smscodec.RPCauseMemoryCapacityExceeded)))
			},
		}, nil
	}
	return s.finalizeInboundSMSData(request.raw, message, response)
}

func (s *Service) logInboundSMSCorrelation(raw string, message inboundSMS) {
	audit, ok := s.latestOutboundSMSAudit(2 * time.Minute)
	if !ok {
		return
	}
	logging.RunDebug("IMS inbound SMS follows recent outbound",
		"call_id", strings.TrimSpace(rawSIPHeaderValue(raw, "Call-ID")),
		"mo_trace_id", audit.TraceID, "mo_call_id", audit.CallID,
		"mo_to", audit.To, "mo_age_ms", time.Since(audit.At).Milliseconds(),
		"sender", normalizeFragmentIdentity(message.sender), "rp_mr", message.rpMR)
}

func (s *Service) finalizeInboundSMSData(
	raw string,
	message inboundSMS,
	response string,
) (inboundSIPResult, error) {
	fragmentKey := inboundSMSFragmentKey(message)
	shouldDispatch, assembleErr := s.assembleInboundSMS(raw, &message)
	if assembleErr != nil {
		return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
			raw: raw, status: 400, rpMR: message.rpMR,
			sendRPError: true, err: assembleErr, peerConn: message.peerConn,
		})
	}
	if shouldDispatch {
		s.publishInboundSMSWithFragment(message, message.fragmentSessionID, false)
	}
	s.reportMTQueueBlocked(mtSMSIdentity(message))
	fingerprint := buildMTSMSFingerprint(message, raw)
	return inboundSIPResult{
		smsReceived: true,
		response:    response,
		afterReply: func() {
			if fragmentKey != "" {
				s.markFragmentAcked(fragmentKey, message.partNo)
			}
			if s.smsMemoryIsFull() {
				s.rememberSMSMemoryDenied(raw)
				s.sendRPReportWithRetry(s.rpReportForInbound(
					raw, message, smscodec.BuildRPError(message.rpMR, smscodec.RPCauseMemoryCapacityExceeded),
				))
				return
			}
			report := s.rpReportForInbound(raw, message, smscodec.BuildRPAck(message.rpMR))
			report.Fingerprint = fingerprint
			s.sendRPReportWithRetry(report)
		},
	}, nil
}

func inboundSMSFragmentKey(message inboundSMS) string {
	if message.total <= 1 {
		return ""
	}
	return buildFragmentSessionKey(fragmentSessionIdentity{
		Sender: message.sender, ServiceCenter: message.serviceCenter, Local: message.targetURI,
		Reference: message.concatRef, RefBits: message.refBits, Total: message.total,
	})
}

func fragmentLifecycleLogFields(ctx fragmentLifecycleContext) []interface{} {
	message := ctx.Message
	return []interface{}{
		"trace_id", ctx.TraceID, "device", ctx.Device,
		"sender", normalizeFragmentIdentity(message.sender), "ref", message.concatRef,
		"ref_bits", message.refBits, "seq", message.partNo, "total", message.total,
		"transport", ctx.Transport, "call_id", ctx.CallID, "rp_mr", int(message.rpMR),
		"arrive_at", ctx.ArrivedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		"content_len", len(message.content), "sc_addr", message.serviceCenter,
		"local_identity", message.targetURI, "key", ctx.Key,
	}
}

func decodeInboundRPData(raw string, rpdu []byte) (inboundSMS, error) {
	rpMR, originator, _, tpdu, err := smscodec.ParseRPDataWithAddresses(rpdu)
	if err != nil {
		return inboundSMS{}, err
	}
	if len(tpdu) == 0 || tpdu[0]&0x03 != 0 {
		return inboundSMS{}, errors.New("inbound RP-DATA does not contain SMS-DELIVER")
	}
	sender, content, timestamp, concat, err := smscodec.DecodeDeliverTPDU(tpdu)
	if err != nil {
		return inboundSMS{}, fmt.Errorf("decode SMS-DELIVER: %w", err)
	}
	sender = strings.TrimSpace(sender)
	if sender == "" {
		sender = strings.TrimSpace(originator)
	}
	targetURI := firstSIPHeaderURI(rawSIPHeaderValue(raw, "To"))
	if parsed, parseErr := parseSIPMessage(raw); parseErr == nil {
		if request, ok := parsed.(*sip.Request); ok {
			_, _, _, to := inboundAckHeaders(request)
			targetURI = firstSIPHeaderURI(to)
		}
	}
	return inboundSMS{
		sender: sender, serviceCenter: strings.TrimSpace(originator),
		targetURI: targetURI,
		content:   content, timestamp: timestamp, rpMR: rpMR,
		concatRef: concat.Ref, refBits: concat.RefBits,
		total: concat.Total, partNo: concat.Seq,
	}, nil
}

func (s *Service) assembleInboundSMS(raw string, message *inboundSMS) (bool, error) {
	if message == nil {
		return false, errors.New("imscore: nil inbound SMS")
	}
	if message.total <= 1 {
		return s.shouldDispatchMTSMS(*message, raw), nil
	}
	traceID, device, transport := "", "", ""
	if s != nil && s.cfg != nil {
		traceID, device = s.cfg.TraceID, s.cfg.DeviceID
		_, _, _, _, transport = s.smsMessageRoute()
	}
	logging.RunDebug("IMS SMS fragment received", fragmentLifecycleLogFields(fragmentLifecycleContext{
		TraceID: traceID, Device: device, Transport: transport,
		CallID: strings.TrimSpace(rawSIPHeaderValue(raw, "Call-ID")),
		Key:    inboundSMSFragmentKey(*message), ArrivedAt: time.Now(), Message: *message,
	})...)
	assembly, err := s.handleSMSFragmentAssembly(message.sender, &smsFragment{
		Ref: message.concatRef, RefBits: message.refBits,
		Total: message.total, Seq: message.partNo, Content: message.content,
		RpMr: message.rpMR, CallID: rawSIPHeaderValue(raw, "Call-ID"),
		ToURI: message.targetURI, ServiceCenter: message.serviceCenter,
	})
	if err != nil || !assembly.Complete {
		return false, err
	}
	message.content = assembly.Content
	message.fragmentSessionID = assembly.SessionID
	return s.shouldDispatchMTSMS(*message, raw), nil
}

func (s *Service) inboundSMSProtocolError(raw string, status int, rpMR byte, sendRPError bool, protocolErr error) (inboundSIPResult, error) {
	return s.handleInboundSMSProtocolFailure(inboundSMSProtocolFailure{
		raw: raw, status: status, rpMR: rpMR,
		sendRPError: sendRPError, err: protocolErr,
	})
}

func (s *Service) handleInboundSMSProtocolFailure(failure inboundSMSProtocolFailure) (inboundSIPResult, error) {
	response, responseErr := buildSIPRequestResponse(failure.raw, failure.status)
	if responseErr != nil {
		return inboundSIPResult{}, responseErr
	}
	result := inboundSIPResult{response: response}
	if failure.sendRPError {
		result.afterReply = func() {
			s.sendRPReportWithRetry(rpReportRequest{
				Inbound: failure.raw, Body: smscodec.BuildRPError(failure.rpMR, rpCauseTemporaryFailure),
				PeerConn: failure.peerConn, RPMR: failure.rpMR,
			})
		}
	}
	return result, failure.err
}

func (s *Service) publishInboundSMS(message inboundSMS) {
	s.publishInboundSMSWithFragment(message, "", false)
}

func (s *Service) publishInboundSMSWithFragment(
	message inboundSMS,
	fragmentSessionKey string,
	incomplete bool,
) {
	if message.timestamp.IsZero() {
		message.timestamp = time.Now()
	}
	s.bus.Publish(&events.EventSMSReceived{
		DevID: s.cfg.DeviceID, Sender: message.sender, TargetURI: message.targetURI,
		Content: message.content, Time: message.timestamp,
		FragmentSessionKey: strings.TrimSpace(fragmentSessionKey), Incomplete: incomplete,
	})
}

func (s *Service) rpReportForInbound(raw string, message inboundSMS, rpdu []byte) rpReportRequest {
	report := rpReportRequest{
		Inbound: raw, Body: rpdu, RPMR: message.rpMR,
		PeerConn:      message.peerConn,
		ServiceCenter: message.serviceCenter,
		Identity:      mtSMSIdentity(message),
	}
	if message.msisdnLess && strings.TrimSpace(message.deliveryReportTo) != "" {
		if contentType, body, err := buildMSISDNLessSMSPayload(shortMessageInfo{To: message.deliveryReportTo}, rpdu); err == nil {
			report.ContentType = contentType
			report.Body = body
		}
	}
	return report
}

func (s *Service) buildInboundSMSControlRequest(inbound string, body []byte, remoteURI, contentType string, omitBinaryCTE, omitInReplyTo bool) (string, error) {
	remoteURI = strings.TrimSpace(remoteURI)
	if remoteURI == "" {
		remoteURI = specRPAckURI(rpReportRequest{Inbound: inbound})
	}
	if remoteURI == "" {
		return "", errors.New("IMS RP-ACK target is unavailable")
	}
	if strings.ContainsAny(remoteURI, "\r\n") {
		return "", errors.New("IMS RP-ACK target is unavailable")
	}
	callID := inboundCallIDForReply(inbound)
	if callID == "" && !omitInReplyTo {
		return "", errors.New("IMS RP-ACK In-Reply-To is unavailable")
	}
	if omitInReplyTo {
		callID = ""
	}
	return s.buildSMSMESSAGEWithOptions(smsMESSAGEOptions{
		RemoteURI:     remoteURI,
		Body:          body,
		InReplyTo:     callID,
		ContentType:   contentType,
		OmitBinaryCTE: omitBinaryCTE,
		OmitInReplyTo: omitInReplyTo,
	})
}

func inboundCallIDForReply(inbound string) string {
	callID := strings.TrimSpace(rawSIPHeaderValue(inbound, "Call-ID"))
	callID = strings.Trim(callID, `"'`)
	if callID == "" || strings.ContainsAny(callID, "\r\n") {
		return ""
	}
	return callID
}

func normalizedContentType(value string) string {
	value, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(value)), ";")
	return strings.TrimSpace(value)
}
