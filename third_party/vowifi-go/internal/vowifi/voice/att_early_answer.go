package voice

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imscore"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imsendpoint"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
	"github.com/iniwex5/vowifi-go/internal/vowifi/voice/callstate"
)

// An iPhone answers an AT&T MT INVITE that supports 100rel with a reliable
// 183 carrying its SDP answer and a PIDF-LO, then a reliable 180 with the
// PIDF-LO, and finally a 200 OK without SDP (Red Pocket, 2026-10-09). With a
// plain 180 and an SDP answer in the 200, AT&T played the caller "no
// voicemail set up" after the answer and dropped the call.
const attEarlyAnswerWait = 8 * time.Second

func (a *Agent) attEarlyAnswerWanted(request imscore.InboundVoiceRequest, reliable, precondition bool) bool {
	if a == nil || precondition || reliable || !attHomeDomain(a.imsSnapshot().Realm) {
		return false
	}
	supported := strings.TrimSpace(request.Supported)
	if supported == "" && request.Request != nil {
		supported = requestHeaderValue(request.Request, "Supported")
	}
	return sipHeaderHasToken(supported, "100rel")
}

// attEarlyFormats picks the payload types an iPhone answers with: the first
// AMR-WB (bandwidth-efficient preferred) and the telephone-event at its
// clock, falling back to AMR and then to the first offered codec.
func attEarlyFormats(offer string) []int {
	info, err := ParseSDP([]byte(offer))
	if err != nil || info == nil {
		return nil
	}
	var chosen *CodecInfo
	for _, pick := range []func(CodecInfo) bool{
		func(c CodecInfo) bool { return strings.EqualFold(c.Name, "AMR-WB") && !sdpOctetAligned(c.Fmtp) },
		func(c CodecInfo) bool { return strings.EqualFold(c.Name, "AMR-WB") },
		func(c CodecInfo) bool { return strings.EqualFold(c.Name, "AMR") && !sdpOctetAligned(c.Fmtp) },
		func(c CodecInfo) bool { return strings.EqualFold(c.Name, "AMR") },
		func(c CodecInfo) bool { return !strings.EqualFold(c.Name, "telephone-event") },
	} {
		for index := range info.Codecs {
			if pick(info.Codecs[index]) {
				chosen = &info.Codecs[index]
				break
			}
		}
		if chosen != nil {
			break
		}
	}
	if chosen == nil {
		return nil
	}
	formats := []int{chosen.PayloadType}
	for _, codec := range info.Codecs {
		if strings.EqualFold(codec.Name, "telephone-event") && codec.ClockRate == chosen.ClockRate {
			formats = append(formats, codec.PayloadType)
			break
		}
	}
	return formats
}

// restrictSDPFormats keeps only the listed payload types in the audio m= line
// and drops the rtpmap/fmtp lines of the others.
func restrictSDPFormats(sdp string, formats []int) string {
	keep := make(map[string]bool, len(formats))
	for _, format := range formats {
		keep[strconv.Itoa(format)] = true
	}
	lines := splitSDPTextLines(strings.TrimRight(sdp, "\r\n"))
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "m=audio "):
			fields := strings.Fields(line)
			if len(fields) > 3 {
				kept := fields[:3:3]
				for _, field := range fields[3:] {
					if keep[field] {
						kept = append(kept, field)
					}
				}
				line = strings.Join(kept, " ")
			}
		case strings.HasPrefix(line, "a=rtpmap:"), strings.HasPrefix(line, "a=fmtp:"):
			value := line[strings.Index(line, ":")+1:]
			format, _, _ := strings.Cut(value, " ")
			if !keep[format] {
				continue
			}
		}
		result = append(result, line)
	}
	return strings.Join(result, "\r\n") + "\r\n"
}

// attEarlySDP builds the answer an iPhone puts in its reliable 183: the
// chosen offer formats at our IMS media address.
func attEarlySDP(offer string, formats []int, localIP string, port int, msisdn string, now time.Time) string {
	family := "IP4"
	if ip := net.ParseIP(localIP); ip != nil && ip.To4() == nil {
		family = "IP6"
	}
	user := "-"
	if msisdn = strings.TrimSpace(msisdn); msisdn != "" {
		user = "tel:" + msisdn
	}
	session := strconv.FormatInt(now.Unix(), 10)
	lines := []string{
		"v=0",
		"o=" + user + " " + session + " 0 IN " + family + " " + localIP,
		"s=-",
		"c=IN " + family + " " + localIP,
		"t=0 0",
	}
	media := "m=audio " + strconv.Itoa(port) + " RTP/AVP"
	for _, format := range formats {
		media += " " + strconv.Itoa(format)
	}
	lines = append(lines, media)
	maxptime := ""
	for _, line := range splitSDPTextLines(restrictSDPFormats(offer, formats)) {
		switch {
		case strings.HasPrefix(line, "a=rtpmap:"), strings.HasPrefix(line, "a=fmtp:"):
			lines = append(lines, line)
		case strings.HasPrefix(line, "a=maxptime:"):
			maxptime = line
		}
	}
	lines = append(lines, "a=ptime:20")
	if maxptime != "" {
		lines = append(lines, maxptime)
	}
	lines = append(lines, "a=sendrecv")
	return shapeIMSSDP(strings.Join(lines, "\r\n") + "\r\n")
}

// prepareATTEarlyAnswer fixes the media answer before the client exists: the
// client is offered only the chosen formats, so its later answer matches the
// SDP already sent in the reliable 183.
func (a *Agent) prepareATTEarlyAnswer(call *Call) (string, error) {
	relay := call.RTPRelay()
	if relay == nil || relay.IMSPort() <= 0 {
		return "", errors.New("voice: inbound media relay is unavailable")
	}
	call.mu.RLock()
	offer, clientOffer := call.remoteSDP, call.clientRemoteSDP
	call.mu.RUnlock()
	formats := attEarlyFormats(offer)
	if len(formats) == 0 {
		return "", errors.New("voice: AT&T offer has no usable codec")
	}
	msisdn := ""
	if invite := call.inviteRequest(); invite != nil && invite.To() != nil {
		msisdn = invite.To().Address.User
	}
	early := attEarlySDP(offer, formats, a.localIP(), relay.IMSPort(), msisdn, time.Now())
	call.setRemoteSDP(offer, restrictSDPFormats(clientOffer, formats))
	call.mu.Lock()
	call.attEarlySDP = early
	call.attEarlyDone = make(chan struct{})
	call.mu.Unlock()
	return early, nil
}

func (c *Call) attEarlyAnswer() (string, <-chan struct{}) {
	if c == nil {
		return "", nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.attEarlySDP, c.attEarlyDone
}

func (c *Call) inviteRequest() *sip.Request {
	_, request := c.serverInviteContext()
	return request
}

// runATTEarlyAnswer sends the reliable 183 and 180 in turn, each after the
// previous one was acknowledged, and then lets the answer proceed.
func (a *Agent) runATTEarlyAnswer(call *Call, early string) {
	call.mu.RLock()
	done := call.attEarlyDone
	call.mu.RUnlock()
	defer close(done)
	for rseq, step := range []struct {
		status int
		sdp    string
	}{{183, early}, {180, ""}} {
		call.inboundDecisionMu.Lock()
		if call.CallDirection() != callstate.DirectionInbound || call.CallState() != callstate.StateRinging {
			call.inboundDecisionMu.Unlock()
			return
		}
		prack := call.armInboundPRACK(uint32(rseq + 1))
		err := a.writeATTReliableProvisional(call, step.status, step.sdp, uint32(rseq+1))
		call.inboundDecisionMu.Unlock()
		if err != nil {
			logging.Info("IMS 来电 AT&T 可靠临时响应发送失败", "call_id", call.CallID(), "status", step.status, "err", err)
			return
		}
		if err := waitInboundAnswerGates(call, prack, nil); err != nil {
			logging.Info("IMS 来电 AT&T 可靠临时响应未收到 PRACK", "call_id", call.CallID(), "status", step.status, "err", err)
			return
		}
	}
}

func (a *Agent) writeATTReliableProvisional(call *Call, status int, sdp string, rseq uint32) error {
	if a == nil || a.dialog == nil || call == nil {
		return errors.New("voice: dialog controller is unavailable")
	}
	invite, request := call.serverInviteContext()
	if invite == nil || request == nil {
		return errInboundResponseUnavailable
	}
	reason := imscore.SIPStatusText(status)
	response := call.BuildResponse(status, reason)
	response.AppendHeader(sip.NewHeader("Require", "100rel"))
	response.AppendHeader(sip.NewHeader("RSeq", strconv.FormatUint(uint64(rseq), 10)))
	if a.applyATTHandsetContact(response, request) == nil {
		if contact := a.dialog.Context().CachedContactHdr; contact != nil {
			response.RemoveHeader("Contact")
			response.AppendHeader(contact.Clone())
		}
	}
	a.applyATTLocation(response, request)
	if sdp != "" {
		attachSDPWithLocation(response, sdp, request)
	}
	logging.Info("IMS 来电 AT&T 可靠临时响应原文", "call_id", call.CallID(), "sip", sipLogValue(response.String()))
	return a.dialog.RejectServerInvite(
		context.Background(), a.deviceID, invite,
		imsendpoint.ServerInviteRejectOptions{Response: response, Code: status, Reason: reason},
	)
}

const attMultipartBoundary = "MIME_boundary_hideck_att"

// attachSDPWithLocation turns the PIDF-LO body applyATTLocation set into the
// multipart/mixed body an iPhone sends: the SDP, then the PIDF-LO that the
// Geolocation cid points at.
func attachSDPWithLocation(response *sip.Response, sdp string, request *sip.Request) {
	pidf := string(response.Body())
	to := request.To()
	if pidf == "" || to == nil {
		response.RemoveHeader("Content-Type")
		response.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
		response.SetBody([]byte(sdp))
		return
	}
	cid := "<" + to.Address.User + "@" + to.Address.Host + ">"
	body := "--" + attMultipartBoundary + "\r\n" +
		"Content-Type: application/sdp\r\n\r\n" + strings.TrimRight(sdp, "\r\n") + "\r\n" +
		"--" + attMultipartBoundary + "\r\n" +
		"Content-ID: " + cid + "\r\n" +
		"Content-Disposition: render; handling=optional\r\n" +
		"Content-Type: application/pidf+xml\r\n\r\n" + strings.TrimRight(pidf, "\r\n") + "\r\n" +
		"--" + attMultipartBoundary + "--\r\n"
	response.RemoveHeader("Content-Type")
	response.AppendHeader(sip.NewHeader("Content-Type", "multipart/mixed;boundary="+attMultipartBoundary))
	response.SetBody([]byte(body))
}

// awaitATTEarlyAnswer waits, with the decision lock released, until the
// reliable 183 and 180 are acknowledged, so the 200 never overtakes them.
func (a *Agent) awaitATTEarlyAnswer(call *Call, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	call.inboundDecisionMu.Unlock()
	defer call.inboundDecisionMu.Lock()
	timer := time.NewTimer(attEarlyAnswerWait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-call.Done:
		return errors.New("voice: inbound call ended before the AT&T early answer")
	case <-timer.C:
		logging.Info("IMS 来电 AT&T 可靠临时响应未完成，继续应答", "call_id", call.CallID())
		return nil
	}
}
