package voice

import (
	"context"
	"errors"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imscore"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imsendpoint"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
)

type serverInviteAnswer struct {
	status int
	reason string
	sdp    string
}

func (a *Agent) answerStoredServerInvite(call *Call, sdp string) (bool, error) {
	return a.answerStoredServerInviteResult(call, serverInviteAnswer{
		status: 200, reason: "OK", sdp: sdp,
	})
}

func (a *Agent) answerStoredServerInviteResult(
	call *Call,
	answer serverInviteAnswer,
) (bool, error) {
	if a == nil || a.dialog == nil || call == nil {
		return false, errors.New("voice: dialog controller is unavailable")
	}
	invite, request := call.serverInviteContext()
	if invite == nil || request == nil {
		return false, nil
	}
	contact := a.dialog.Context().CachedContactHdr
	if contact == nil {
		return true, errors.New("voice: inbound answer Contact is unavailable")
	}
	sdp := answer.sdp
	if a.aisHandsetResponses() {
		sdp = aisHandsetSDP(sdp)
	}
	response := call.BuildResponseWithSDP(answer.status, answer.reason, []byte(sdp))
	if expires := formatSessionExpiresHeader(call); expires != "" {
		response.AppendHeader(sip.NewHeader("Session-Expires", expires))
		// RFC 4028 9: a 2xx carrying Session-Expires requires the extension.
		response.AppendHeader(sip.NewHeader("Require", "timer"))
	}
	// The registered Contact (contact ID, transport, +sip.instance, MMTel
	// ICSI) replaces the bare session Contact. The INVITE's Accept-Contact
	// requires those feature tags.
	response.RemoveHeader("Contact")
	response.AppendHeader(contact.Clone())
	if pani := a.dialog.Context().CachedPANIHdr; pani != nil {
		if value := strings.TrimSpace(pani.Value()); value != "" {
			response.AppendHeader(sip.NewHeader("P-Access-Network-Info", value))
		}
	}
	answerContact := contact.Clone()
	if handset := a.applyAISHandsetHeaders(response, request, true); handset != nil {
		answerContact = handset
	}
	dialog, err := a.dialog.AnswerServerInvite(
		context.Background(), a.deviceID, invite,
		imsendpoint.ServerInviteAnswerOptions{Response: response, Contact: answerContact},
	)
	logging.Info("IMS 来电最终响应原文", "call_id", call.CallID(), "sip", sipLogValue(response.String()), "err", err)
	if err != nil {
		return true, err
	}
	return true, call.storeDialogHandle(dialog)
}

// sipLogValue keeps a SIP message on one log line.
func sipLogValue(message string) string {
	return strings.Join(splitSDPTextLines(strings.TrimSpace(message)), " | ")
}

func (a *Agent) rejectStoredServerInvite(call *Call, statusCode int) (bool, error) {
	return a.rejectStoredServerInviteWithReason(call, statusCode, imscore.SIPStatusText(statusCode))
}

func (a *Agent) rejectStoredServerInviteWithReason(
	call *Call,
	statusCode int,
	reason string,
) (bool, error) {
	if a == nil || a.dialog == nil || call == nil {
		return false, errors.New("voice: dialog controller is unavailable")
	}
	invite, request := call.serverInviteContext()
	if invite == nil || request == nil {
		return false, nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = strings.TrimSpace(imscore.SIPStatusText(statusCode))
	}
	response := call.BuildResponse(statusCode, reason)
	if statusCode == 180 && call.waitingIndication {
		response.AppendHeader(sip.NewHeader("Alert-Info", "<urn:alert:service:call-waiting>"))
	}
	if statusCode == 480 && call.waitingIndication {
		response.AppendHeader(sip.NewHeader("Reason", `Q.850;cause=19;text="User alerting, no answer"`))
	}
	if statusCode > 100 && statusCode < 200 {
		if a.applyAISHandsetHeaders(response, request, false) != nil {
			logging.Info("IMS 来电临时响应原文", "call_id", call.CallID(), "sip", sipLogValue(response.String()))
		}
	}
	err := a.dialog.RejectServerInvite(
		context.Background(), a.deviceID, invite,
		imsendpoint.ServerInviteRejectOptions{Response: response, Code: statusCode, Reason: reason},
	)
	return true, err
}
