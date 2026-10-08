package voice

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/internal/vowifi/imscore"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
	"github.com/iniwex5/vowifi-go/internal/vowifi/voice/callstate"
)

// earlyOutboundUpdate reports an UPDATE with SDP inside the early dialog of a
// call we originated. AIS sends one after the first second of early media to
// move the session onto new payload types (AMR-WB 104 -> 111); an iPhone
// answers it with 200, while the re-INVITE path answered 491 because the call
// was not connected yet, and AIS stopped all downlink media.
func earlyOutboundUpdate(call *Call, request imscore.InboundVoiceRequest) bool {
	return call != nil && len(request.Body) > 0 &&
		call.CallDirection() == callstate.DirectionOutbound &&
		call.CallState() != callstate.StateConnected && !call.IsTerminalState()
}

func (a *Agent) answerEarlyOutboundUpdate(request imscore.InboundVoiceRequest, call *Call) (imscore.InboundVoiceResult, error) {
	if request.Responder == nil || !isVoiceSDPContentType(request.ContentType) {
		return voiceResult(488), nil
	}
	if err := validateSDPMediaEndpoint(request.Body, "IMS early UPDATE"); err != nil {
		return voiceResult(488), nil
	}
	call.inboundDecisionMu.Lock()
	defer call.inboundDecisionMu.Unlock()
	clientLocal, imsLocal := call.localSDPs()
	answer, err := earlyUpdateAnswer(string(request.Body), call.remoteSDPValue(), imsLocal)
	if err != nil {
		logging.Info("IMS 去电早期 UPDATE 无法应答", "call_id", call.CallID(), "err", err)
		return voiceResult(488), nil
	}
	clientRemote := call.clientRemoteSDPValue()
	if _, err := ProcessIncomingIMSSDP(call, request.Body, clientRelayIP); err != nil {
		return voiceResult(488), nil
	}
	if info, parseErr := ParseSDP([]byte(answer)); parseErr == nil {
		if err := configureRelayDTMF(call.RTPRelay(), info); err != nil {
			return voiceResult(488), nil
		}
	}
	// The client keeps the stream it attached on 183; only the IMS side moved.
	call.setRemoteSDP(string(request.Body), clientRemote)
	call.setLocalSDP(clientLocal, answer)
	if err := request.Responder.Respond(a.voiceSDPResponse(call, 200, answer)); err != nil {
		return voiceResult(0), err
	}
	logging.Info("IMS 去电早期 UPDATE 已应答", "call_id", call.CallID(),
		"offer", sdpLogValue(string(request.Body)), "answer", sdpLogValue(answer))
	return voiceResult(0), nil
}

// earlyUpdateAnswer answers a new offer with the audio codec already in use
// (same name, clock and framing) and telephone-event at that clock, using the
// payload types of the new offer, in the form an iPhone sends.
func earlyUpdateAnswer(offer, negotiated, local string) (string, error) {
	offerInfo, err := ParseSDP([]byte(offer))
	if err != nil || offerInfo == nil {
		return "", fmt.Errorf("parse UPDATE offer: %w", err)
	}
	current, err := firstAudioCodec(negotiated)
	if err != nil {
		return "", err
	}
	localInfo, err := ParseSDP([]byte(local))
	if err != nil || localInfo == nil || localInfo.ConnectionIP == "" || localInfo.MediaPort <= 0 {
		return "", fmt.Errorf("local IMS SDP has no endpoint")
	}
	var audio, dtmf *CodecInfo
	for index := range offerInfo.Codecs {
		codec := &offerInfo.Codecs[index]
		if audio == nil && strings.EqualFold(codec.Name, current.Name) && codec.ClockRate == current.ClockRate &&
			sdpOctetAligned(codec.Fmtp) == sdpOctetAligned(current.Fmtp) {
			audio = codec
		}
	}
	if audio == nil {
		return "", fmt.Errorf("UPDATE offer has no %s/%d", current.Name, current.ClockRate)
	}
	for index := range offerInfo.Codecs {
		codec := &offerInfo.Codecs[index]
		if !strings.EqualFold(codec.Name, sdpTelephoneEvent) {
			continue
		}
		if codec.ClockRate == audio.ClockRate {
			dtmf = codec
			break
		}
		if dtmf == nil {
			dtmf = codec
		}
	}
	session := strconv.FormatInt(time.Now().Unix(), 10)
	family := sdpIPFamily(localInfo.ConnectionIP)
	lines := []string{
		"v=0",
		"o=- " + session + " " + session + " IN " + family + " " + localInfo.ConnectionIP,
		"s=-",
		"c=IN " + family + " " + localInfo.ConnectionIP,
		"t=0 0",
	}
	payloads := strconv.Itoa(audio.PayloadType)
	if dtmf != nil {
		payloads += " " + strconv.Itoa(dtmf.PayloadType)
	}
	lines = append(lines, fmt.Sprintf("m=audio %d RTP/AVP %s", localInfo.MediaPort, payloads),
		fmt.Sprintf("a=rtpmap:%d %s/%d", audio.PayloadType, audio.Name, audio.ClockRate))
	if strings.TrimSpace(audio.Fmtp) != "" {
		lines = append(lines, fmt.Sprintf("a=fmtp:%d %s", audio.PayloadType, strings.TrimSpace(audio.Fmtp)))
	}
	if dtmf != nil {
		lines = append(lines,
			fmt.Sprintf("a=rtpmap:%d %s/%d", dtmf.PayloadType, sdpTelephoneEvent, dtmf.ClockRate),
			fmt.Sprintf("a=fmtp:%d 0-15", dtmf.PayloadType))
	}
	lines = append(lines, "a=ptime:20", "a=maxptime:240", "a=sendrecv")
	return strings.Join(lines, "\r\n") + "\r\n", nil
}

func firstAudioCodec(sdp string) (*CodecInfo, error) {
	info, err := ParseSDP([]byte(sdp))
	if err != nil || info == nil {
		return nil, fmt.Errorf("parse negotiated SDP: %w", err)
	}
	for index := range info.Codecs {
		if !strings.EqualFold(info.Codecs[index].Name, sdpTelephoneEvent) {
			return &info.Codecs[index], nil
		}
	}
	return nil, fmt.Errorf("negotiated SDP has no audio codec")
}
