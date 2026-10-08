package voice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imscore"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imsendpoint"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
	"github.com/iniwex5/vowifi-go/internal/vowifi/voice/callstate"
)

func (a *Agent) handleOutboundProvisional(
	ctx context.Context,
	call *Call,
	response imscore.SIPResponse,
) (resultErr error) {
	return a.handleIMS1xxResponse(ctx, call, response)
}

func (a *Agent) handleIMS1xxResponse(
	ctx context.Context,
	call *Call,
	response imscore.SIPResponse,
) (resultErr error) {
	if call == nil || response.StatusCode >= 200 {
		return nil
	}
	if response.StatusCode <= 100 {
		logging.Info("IMS INVITE 100 Trying", "status", response.StatusCode, "reason", response.Reason)
		return nil
	}
	if response.StatusCode == 199 {
		call.terminateEarlyDialog(voiceHeaderTag(voiceResponseHeader(response.Headers, "To")))
		logging.Info("IMS INVITE 199 early dialog terminated", "reason", response.Reason)
		return nil
	}
	logOutboundInviteResponse("IMS INVITE 临时响应", response)
	confirmed := call.CallState() == callstate.StateConnected
	if !confirmed {
		call.MarkInviteProvisional(response.StatusCode)
		if response.StatusCode == 180 {
			a.emitCallRinging(call)
		}
	}
	call.learnVoiceDialog(response)
	toTag := voiceHeaderTag(voiceResponseHeader(response.Headers, "To"))
	if isVoiceSDPContentType(voiceResponseHeader(response.Headers, "Content-Type")) &&
		sipHeaderHasToken(voiceResponseHeader(response.Headers, "Require"), "precondition") {
		call.noteDialogPrecondition(toTag, true)
	}
	call.applyVoiceSessionExpires(voiceResponseHeader(response.Headers, "Session-Expires"))
	preconditionSDP := ""
	if isVoiceSDPContentType(voiceResponseHeader(response.Headers, "Content-Type")) && len(response.Body) > 0 {
		if err := a.updateRemoteMedia(call, response); err != nil {
			logging.WarnRate("ims-invite-provisional-sdp", "IMS INVITE 临时响应 SDP 处理失败",
				"status", response.StatusCode, "err", err)
		} else if !confirmed {
			// Early media is negotiated. The client attaches now so RTP flows
			// both ways before the answer; an SBC stops sending otherwise.
			a.emitCallMediaUpdated(call)
		}
		if !confirmed {
			remoteSDP := string(response.Body)
			a.applyCallPreconditions(call, remoteSDP)
			if sdpHasPreconditions(remoteSDP) {
				preconditionSDP = remoteSDP
			}
		}
	}
	if !sipHeaderHasToken(voiceResponseHeader(response.Headers, "Require"), "100rel") {
		a.queuePreconditionStatusUpdate(call, preconditionSDP)
		return nil
	}
	rseq, err := reliableProvisionalRSeq(response)
	if err != nil {
		return err
	}
	if !call.markReliableProvisionalRSeq(rseq) {
		return nil
	}
	if call.hasLocalInviteTransaction() && !confirmed {
		return nil
	}
	if err := a.sendReliableProvisionalPRACK(ctx, call, rseq); err != nil {
		return err
	}
	a.queuePreconditionStatusUpdate(call, preconditionSDP)
	return nil
}

func logOutboundInviteRequest(raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	body := ""
	if index := strings.Index(raw, "\r\n\r\n"); index >= 0 {
		body = raw[index+4:]
	}
	fromTag := voiceHeaderTag(voiceRawHeader(raw, "From"))
	toTag := voiceHeaderTag(voiceRawHeader(raw, "To"))
	callID := voiceRawHeader(raw, "Call-ID")
	routes := voiceRawHeaders(raw, "Route")
	logging.Info("IMS INVITE outbound",
		"ruri_host", inviteRequestURIHost(raw),
		"ruri_user_kind", inviteRequestURIUserKind(raw),
		"via", voiceRawHeader(raw, "Via"),
		"via_port", sipHeaderSentByPort(voiceRawHeader(raw, "Via")),
		"route", strings.Join(routes, ","),
		"route_hops", len(routes),
		"route_orig", voiceRouteHasOrig(routes),
		"cseq", voiceRawHeader(raw, "CSeq"),
		"accept", voiceRawHeader(raw, "Accept"),
		"contact_has_ob", strings.Contains(strings.ToLower(voiceRawHeader(raw, "Contact")), ";ob"),
		"contact_has_instance", strings.Contains(strings.ToLower(voiceRawHeader(raw, "Contact")), "+sip.instance"),
		"session_expires_present", voiceRawHeader(raw, "Session-Expires") != "",
		"body_bytes", len(body),
		"content_length", voiceRawHeader(raw, "Content-Length"),
		"qos_remote", sdpQoSCurrent(body, "remote"),
		"to_has_tag", toTag != "",
		"call_id_kind", inviteTokenKind(callID),
		"call_id_len", len(strings.TrimSpace(callID)),
		"from_tag_kind", inviteTokenKind(fromTag),
		"from_tag_len", len(fromTag),
		"from_tag_suffix", inviteTokenSuffix(fromTag),
		"sdp", sdpLogValue(body),
	)
}

func sipHeaderSentByPort(via string) int {
	fields := strings.Fields(via)
	if len(fields) < 2 {
		return 0
	}
	sentBy, _, _ := strings.Cut(fields[1], ";")
	_, portText, err := net.SplitHostPort(strings.TrimSpace(sentBy))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 {
		return 0
	}
	return port
}

func inviteRequestURIHost(raw string) string {
	line, _, _ := strings.Cut(raw, "\r\n")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	var uri sip.Uri
	if err := sip.ParseUri(fields[1], &uri); err != nil {
		return ""
	}
	return strings.ToLower(strings.Trim(uri.Host, "[]"))
}

func inviteRequestURIUserKind(raw string) string {
	line, _, _ := strings.Cut(raw, "\r\n")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	var uri sip.Uri
	if err := sip.ParseUri(fields[1], &uri); err != nil {
		return ""
	}
	user := strings.TrimSpace(uri.User)
	switch {
	case user == "":
		return "empty"
	case strings.HasPrefix(user, "+"):
		return "e164"
	case isDigits(user) && len(user) <= 6:
		return "shortcode"
	case isDigits(user):
		return "digits"
	default:
		return "other"
	}
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func voiceRawHeader(raw, name string) string {
	values := voiceRawHeaders(raw, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func voiceRawHeaders(raw, name string) []string {
	prefix := strings.ToLower(name) + ":"
	var values []string
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			values = append(values, strings.TrimSpace(line[len(name)+1:]))
		}
	}
	return values
}

func voiceRouteHasOrig(routes []string) bool {
	for _, route := range routes {
		for _, item := range strings.Split(route, ",") {
			item = strings.ToLower(strings.TrimSpace(item))
			if strings.Contains(item, "sip:orig@") || strings.Contains(item, "sip:orig;") {
				return true
			}
		}
	}
	return false
}

func logOutboundInviteResponse(message string, response imscore.SIPResponse) {
	toTag := voiceHeaderTag(voiceResponseHeader(response.Headers, "To"))
	logging.Info(message,
		"status", response.StatusCode,
		"reason", response.Reason,
		"require", voiceResponseHeader(response.Headers, "Require"),
		"rseq", voiceResponseHeader(response.Headers, "RSeq"),
		"warning", logging.RedactSIPRaw(voiceResponseHeader(response.Headers, "Warning")),
		"network_reason", logging.RedactSIPRaw(voiceResponseHeader(response.Headers, "Reason")),
		"to_has_tag", toTag != "",
		"to_tag_kind", inviteTokenKind(toTag),
		"to_tag_len", len(toTag),
		"to_tag_suffix", inviteTokenSuffix(toTag),
		"sdp", sdpLogValue(string(response.Body)),
	)
}

func inviteTokenKind(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return "empty"
	case inviteLongDigitPattern.MatchString(value):
		return "redacted"
	default:
		return "opaque"
	}
}

func inviteTokenSuffix(value string) string {
	if inviteTokenKind(value) != "opaque" {
		return ""
	}
	if len(value) > 4 {
		return value[len(value)-4:]
	}
	return value
}

var inviteLongDigitPattern = regexp.MustCompile(`\d{8,}`)

func (a *Agent) sendReliableProvisionalPRACK(ctx context.Context, call *Call, rseq uint32) error {
	options := reliableProvisionalPRACKOptions(
		call,
		strconv.FormatUint(uint64(rseq), 10),
		voiceResponseHeaderFromCall(call, "Contact"),
		voiceResponseRoutesFromCall(call),
	)
	return a.sendReliableProvisionalPRACKWithOptions(ctx, call, options)
}

func (a *Agent) sendReliableProvisionalPRACKWithOptions(
	ctx context.Context,
	call *Call,
	options imsendpoint.ReliableProvisionalOptions,
) error {
	if a == nil || a.dialog == nil || call == nil {
		return errors.New("voice: dialog controller is unavailable")
	}
	if options.Invite == nil && options.Dialog == nil {
		return errors.New("voice: IMS INVITE handle is unavailable")
	}
	err := a.dialog.SendReliableProvisionalPRACK(ctx, a.deviceID, options)
	if err != nil {
		return fmt.Errorf("voice: PRACK transaction failed: %w", err)
	}
	logging.Info("IMS PRACK 成功", "rack", options.RAck, "status", 200)
	return nil
}

func voiceResponseHeaderFromCall(call *Call, name string) string {
	if call == nil || !strings.EqualFold(name, "Contact") {
		return ""
	}
	call.mu.RLock()
	defer call.mu.RUnlock()
	return call.DialogState.IMSContact
}

func voiceResponseRoutesFromCall(call *Call) []string {
	if call == nil {
		return nil
	}
	call.mu.RLock()
	defer call.mu.RUnlock()
	return append([]string(nil), call.DialogState.RouteSet...)
}

func reliableProvisionalRSeq(response imscore.SIPResponse) (uint32, error) {
	value := strings.TrimSpace(voiceResponseHeader(response.Headers, "RSeq"))
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("voice: reliable provisional response has invalid RSeq %q", value)
	}
	return uint32(parsed), nil
}

func sipHeaderHasToken(value, token string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), token) {
			return true
		}
	}
	return false
}
