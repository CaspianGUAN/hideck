package sipclient

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	bridgeEventBuffer  = 16
	controlTimeout     = 10 * time.Second
	resubscribeBackoff = time.Second
)

var dialablePattern = regexp.MustCompile(`^\+?[0-9]{1,32}$`)

// bridge ties one phone-service call to one SIP dialog with the PBX.
type bridge struct {
	client *Client
	media  MediaLeg
	events chan phone.Event

	callID   string
	deviceID string
}

func (c *Client) newBridge(media MediaLeg, callID, deviceID string) *bridge {
	return &bridge{
		client: c, media: media, events: make(chan phone.Event, bridgeEventBuffer),
		callID: callID, deviceID: deviceID,
	}
}

func (c *Client) track(b *bridge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b.callID != "" {
		c.byCall[b.callID] = b
	}
	c.byMedia[b.media.MediaID()] = b
}

func (c *Client) setBridgeCall(b *bridge, callID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b.callID = callID
	c.byCall[callID] = b
}

func (c *Client) untrack(b *bridge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byCall[b.callID] == b {
		delete(c.byCall, b.callID)
	}
	if c.byMedia[b.media.MediaID()] == b {
		delete(c.byMedia, b.media.MediaID())
	}
}

func (c *Client) bridgeFor(event phone.Event) *bridge {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b := c.byCall[event.Call.CallID]; b != nil {
		return b
	}
	if event.Call.MediaID != "" {
		return c.byMedia[event.Call.MediaID]
	}
	return nil
}

func (c *Client) followPhoneEvents() {
	for {
		_, stream, unsubscribe := c.phone.Subscribe(^uint64(0))
		for open := true; open; {
			select {
			case <-c.ctx.Done():
				unsubscribe()
				return
			case event, ok := <-stream:
				if !ok {
					open = false
					break
				}
				c.dispatch(event)
			}
		}
		unsubscribe()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(resubscribeBackoff):
		}
	}
}

func (c *Client) dispatch(event phone.Event) {
	if b := c.bridgeFor(event); b != nil {
		select {
		case b.events <- event:
		default:
			logger.Warn("SIP 桥接事件队列已满，丢弃事件", "call_id", event.Call.CallID, "type", event.Type)
		}
		return
	}
	if event.Type == "incoming_call" {
		c.ringPBX(event.Call)
	}
}

// dtmfVia forwards peer DTMF once the bridge exists; the media leg is created
// first, so the callback resolves the bridge lazily.
func (c *Client) dtmfVia(bound *atomic.Pointer[bridge]) func(string) {
	return func(digit string) {
		b := bound.Load()
		if b == nil {
			return
		}
		c.mu.Lock()
		callID := b.callID
		c.mu.Unlock()
		if callID == "" {
			return
		}
		if err := c.phone.DTMF(c.owner, callID, b.media.MediaLease(), digit); err != nil {
			logger.Warn("SIP DTMF 转发失败", "call_id", callID, "digit", digit, "err", err)
		}
	}
}

func (c *Client) hangupPhone(b *bridge) {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := c.phone.Hangup(ctx, c.owner, b.callID, b.media.MediaLease()); err != nil {
		logger.Warn("SIP 侧挂断后结束手机通话失败", "call_id", b.callID, "err", err)
	}
}

// ---- PBX -> mobile (the PBX sends us an INVITE) ----

func (c *Client) onInvite(request *sip.Request, tx sip.ServerTransaction) {
	dialog, err := c.serving.ReadInvite(request, tx)
	if err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(request, sip.StatusBadRequest, "Bad Request", nil))
		return
	}
	// sipgo terminates the INVITE transaction once this handler returns, so
	// the handler owns the call until it ends.
	c.serveOutbound(request, dialog)
}

func (c *Client) serveOutbound(request *sip.Request, dialog *sipgo.DialogServerSession) {
	defer dialog.Close()
	callee := dialedNumber(request)
	if !dialablePattern.MatchString(callee) {
		_ = dialog.Respond(sip.StatusNotFound, "Not Found", nil)
		return
	}
	deviceID := c.pickDevice()
	if deviceID == "" {
		_ = dialog.Respond(sip.StatusServiceUnavailable, "No Modem Available", nil)
		return
	}
	_ = dialog.Respond(sip.StatusTrying, "Trying", nil)
	b, answer, status := c.startOutbound(request, deviceID, callee)
	if b == nil {
		_ = dialog.Respond(status, reasonPhrase(status), nil)
		return
	}
	defer c.untrack(b)
	logger.Info("SIP 呼出已转到手机", "call_id", b.callID, "device_id", deviceID, "callee", callee)
	answered := false
	for {
		select {
		case event := <-b.events:
			switch event.Type {
			case "call_ringing":
				if !answered {
					_ = dialog.Respond(sip.StatusRinging, "Ringing", nil)
				}
			case "call_answered":
				if !answered {
					answered = true
					if err := dialog.RespondSDP([]byte(answer)); err != nil {
						logger.Warn("SIP 200 OK 发送失败", "call_id", b.callID, "err", err)
						c.hangupPhone(b)
						return
					}
				}
			case "call_ended":
				if answered {
					c.byeServer(dialog)
				} else {
					status := endStatus(event.Call)
					_ = dialog.Respond(status, reasonPhrase(status), nil)
				}
				return
			}
		case <-dialog.Context().Done():
			c.hangupPhone(b)
			return
		}
	}
}

func (c *Client) startOutbound(request *sip.Request, deviceID, callee string) (*bridge, string, int) {
	var bound atomic.Pointer[bridge]
	media, err := c.phone.CreateMedia(c.owner, c.localIP, c.dtmfVia(&bound))
	if err != nil {
		logger.Warn("SIP 呼出创建媒体失败", "err", err)
		return nil, "", sip.StatusInternalServerError
	}
	answer, err := media.AnswerOffer(string(request.Body()))
	if err != nil {
		c.phone.ReleaseMedia(media)
		logger.Warn("SIP 呼出 SDP 不可用", "err", err)
		return nil, "", sip.StatusNotAcceptableHere
	}
	b := c.newBridge(media, "", deviceID)
	bound.Store(b)
	c.track(b)
	ctx, cancel := context.WithTimeout(c.ctx, controlTimeout)
	defer cancel()
	if err := c.devices.PrepareCall(ctx, deviceID); err != nil {
		c.untrack(b)
		c.phone.ReleaseMedia(media)
		logger.Warn("SIP 呼出准备模组失败", "device_id", deviceID, "err", err)
		return nil, "", sip.StatusServiceUnavailable
	}
	view, err := c.phone.StartCall(phone.StartCallRequest{
		Owner: c.owner, DeviceID: deviceID, Callee: callee,
		MediaID: media.MediaID(), Lease: media.MediaLease(),
	})
	if err != nil {
		c.untrack(b)
		c.phone.ReleaseMedia(media)
		logger.Warn("SIP 呼出发起手机通话失败", "device_id", deviceID, "err", err)
		if strings.Contains(err.Error(), "already has an active call") {
			return nil, "", sip.StatusBusyHere
		}
		return nil, "", sip.StatusServiceUnavailable
	}
	c.setBridgeCall(b, view.CallID)
	return b, answer, 0
}

func (c *Client) byeServer(dialog *sipgo.DialogServerSession) {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := dialog.Bye(ctx); err != nil {
		logger.Warn("SIP BYE 发送失败", "err", err)
	}
}

// ---- mobile -> PBX (an incoming mobile call rings the PBX) ----

func (c *Client) ringPBX(call phone.CallView) {
	if c.settings.InboundTo == "" || !c.isRegistered() {
		return
	}
	if c.settings.DeviceID != "" && call.DeviceID != c.settings.DeviceID {
		return
	}
	var bound atomic.Pointer[bridge]
	media, err := c.phone.CreateMedia(c.owner, c.localIP, c.dtmfVia(&bound))
	if err != nil {
		logger.Warn("SIP 来电创建媒体失败", "call_id", call.CallID, "err", err)
		return
	}
	b := c.newBridge(media, call.CallID, call.DeviceID)
	bound.Store(b)
	c.track(b)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.untrack(b)
		c.serveInbound(b, call)
	}()
}

type answerResult struct {
	dialog *sipgo.DialogClientSession
	err    error
}

func (c *Client) serveInbound(b *bridge, call phone.CallView) {
	ringCtx, stopRinging := context.WithTimeout(c.ctx, c.settings.RingTimeout)
	defer stopRinging()
	request := c.inboundInvite(call.Peer, b.media.OfferSDP())
	dialog, err := c.dialogs.WriteInvite(ringCtx, request)
	if err != nil {
		c.phone.ReleaseMedia(b.media)
		logger.Warn("SIP 来电 INVITE 发送失败", "call_id", b.callID, "err", err)
		return
	}
	defer dialog.Close()
	logger.Info("手机来电已振铃 SIP 分机", "call_id", b.callID, "caller", call.Peer, "target", c.settings.InboundTo)
	result := make(chan answerResult, 1)
	go func() {
		err := dialog.WaitAnswer(ringCtx, sipgo.AnswerOptions{
			Username: c.settings.AuthUsername, Password: c.settings.Password,
		})
		result <- answerResult{dialog: dialog, err: err}
	}()
	answered := c.awaitPBXAnswer(b, result, stopRinging)
	if !answered {
		c.phone.ReleaseMedia(b.media)
		return
	}
	c.bridgeAnsweredInbound(b, dialog)
}

// awaitPBXAnswer waits for the PBX to pick up, giving way if the browser
// answers first or the caller goes away.
func (c *Client) awaitPBXAnswer(b *bridge, result <-chan answerResult, stopRinging context.CancelFunc) bool {
	for {
		select {
		case outcome := <-result:
			if outcome.err != nil {
				if !errors.Is(outcome.err, context.Canceled) && !errors.Is(outcome.err, context.DeadlineExceeded) {
					logger.Info("SIP 分机未接听来电", "call_id", b.callID, "err", outcome.err)
				}
				return false
			}
			return true
		case event := <-b.events:
			switch {
			case event.Type == "call_ended",
				event.Type == "call_answered" && event.Call.MediaID != b.media.MediaID():
				stopRinging()
				if outcome := <-result; outcome.err == nil {
					// The PBX picked up as we cancelled; release its leg.
					c.byeClient(outcome.dialog)
				}
				return false
			}
		}
	}
}

func (c *Client) bridgeAnsweredInbound(b *bridge, dialog *sipgo.DialogClientSession) {
	ctx, cancel := context.WithTimeout(c.ctx, controlTimeout)
	defer cancel()
	ackErr := dialog.Ack(ctx)
	answerErr := b.media.ApplyAnswer(string(dialog.InviteResponse.Body()))
	if ackErr == nil && answerErr == nil {
		_, answerErr = c.phone.Answer(ctx, phone.ControlRequest{
			Owner: c.owner, CallID: b.callID, MediaID: b.media.MediaID(), Lease: b.media.MediaLease(),
		})
	}
	if err := errors.Join(ackErr, answerErr); err != nil {
		logger.Info("SIP 分机接听后未能接管来电，挂断分机", "call_id", b.callID, "err", err)
		c.byeClient(dialog)
		c.phone.ReleaseMedia(b.media)
		return
	}
	logger.Info("SIP 分机已接听手机来电", "call_id", b.callID)
	for {
		select {
		case event := <-b.events:
			if event.Type == "call_ended" {
				c.byeClient(dialog)
				return
			}
		case <-dialog.Context().Done():
			c.hangupPhone(b)
			return
		}
	}
}

func (c *Client) byeClient(dialog *sipgo.DialogClientSession) {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := dialog.Bye(ctx); err != nil {
		logger.Warn("SIP BYE 发送失败", "err", err)
	}
}

func (c *Client) inboundInvite(caller, offer string) *sip.Request {
	request := sip.NewRequest(sip.INVITE, c.serverURI(c.settings.InboundTo))
	request.SetTransport(strings.ToUpper(c.settings.Transport))
	display := strings.TrimSpace(caller)
	from := &sip.FromHeader{DisplayName: display, Address: c.aor(), Params: sip.NewParams()}
	from.Params.Add("tag", sip.GenerateTagN(16))
	request.AppendHeader(from)
	request.AppendHeader(&sip.ToHeader{Address: sip.Uri{Scheme: "sip", User: c.settings.InboundTo, Host: c.settings.ServerHost}})
	if callerID := callerIdentity(caller); callerID != "" {
		identity := "<sip:" + callerID + "@" + c.settings.ServerHost + ">"
		if display != "" {
			identity = "\"" + display + "\" " + identity
		}
		request.AppendHeader(sip.NewHeader("P-Asserted-Identity", identity))
	}
	request.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	request.AppendHeader(sip.NewHeader("User-Agent", userAgentName))
	request.SetBody([]byte(offer))
	return request
}

// ---- helpers ----

func (c *Client) pickDevice() string {
	if c.settings.DeviceID != "" {
		return c.settings.DeviceID
	}
	if ids := c.devices.DeviceIDs(); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// dialedNumber reads the target from the Request-URI user part, which is
// where PJSIP/<number>@<endpoint> puts it.
func dialedNumber(request *sip.Request) string {
	user := request.Recipient.User
	if user == "" {
		if to := request.To(); to != nil {
			user = to.Address.User
		}
	}
	return normalizeNumber(user)
}

func normalizeNumber(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for index, char := range value {
		switch {
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '+' && index == 0:
			builder.WriteRune(char)
		case char == '-' || char == ' ' || char == '(' || char == ')' || char == '.':
		default:
			return ""
		}
	}
	return builder.String()
}

func callerIdentity(caller string) string {
	if number := normalizeNumber(caller); number != "" {
		return number
	}
	return ""
}

// endStatus maps a mobile call that ended before answer to a SIP failure.
func endStatus(call phone.CallView) int {
	reason := strings.ToLower(call.EndReason)
	switch {
	case call.Status == phone.StatusBusy || strings.Contains(reason, "busy") || strings.Contains(reason, "486"):
		return sip.StatusBusyHere
	case strings.Contains(reason, "declin") || strings.Contains(reason, "603"):
		return sip.StatusGlobalDecline
	case strings.Contains(reason, "not found") || strings.Contains(reason, "404"):
		return sip.StatusNotFound
	case strings.Contains(reason, "cancel") || strings.Contains(reason, "local_hangup"):
		return sip.StatusRequestTerminated
	default:
		return sip.StatusTemporarilyUnavailable
	}
}

func reasonPhrase(status int) string {
	switch status {
	case sip.StatusBusyHere:
		return "Busy Here"
	case sip.StatusGlobalDecline:
		return "Decline"
	case sip.StatusNotFound:
		return "Not Found"
	case sip.StatusRequestTerminated:
		return "Request Terminated"
	case sip.StatusNotAcceptableHere:
		return "Not Acceptable Here"
	case sip.StatusServiceUnavailable:
		return "Service Unavailable"
	case sip.StatusInternalServerError:
		return "Server Internal Error"
	default:
		return "Temporarily Unavailable"
	}
}
