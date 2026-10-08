package phone

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	amrWBPayloadType   = 104
	amrWBOAPayloadType = 110
	amrPayloadType     = 102
	amrOAPayloadType   = 114
	evsPayloadType     = 106
	dtmfPayloadType    = 101
)

type rtpEndpoint struct {
	Address     *net.UDPAddr
	Codec       string
	PayloadType uint8
	ClockRate   int
	Fmtp        string
	// DTMFPayloadType and DTMFClockRate are the peer's telephone-event at the
	// audio codec's clock rate (else 8000), or 0 when the peer offered none.
	DTMFPayloadType uint8
	DTMFClockRate   int
}

type rtpCodec struct {
	name      string
	clockRate int
}

type endpointSelection struct {
	host         string
	port         int
	payloadTypes []int
	codecs       map[int]rtpCodec
	fmtps        map[int]string
	supported    []string
}

func plainAudioSDP(port int, realtimeCodecs []string) string {
	payloads, attributes := advertisedAudioCodecs(realtimeCodecs)
	return renderPlainAudioSDP(port, payloads, attributes, dtmfPayloadType, 8000)
}

func plainSelectedAudioSDP(port int, endpoint rtpEndpoint) string {
	payload := strconv.Itoa(int(endpoint.PayloadType))
	attribute := fmt.Sprintf("a=rtpmap:%s %s/%d", payload, endpoint.Codec, endpoint.ClockRate)
	attributes := []string{attribute}
	if endpoint.Fmtp != "" {
		attributes = append(attributes, "a=fmtp:"+payload+" "+endpoint.Fmtp)
	}
	dtmf, dtmfClock := selectedDTMF(endpoint)
	return renderPlainAudioSDP(port, []string{payload, strconv.Itoa(dtmf)}, attributes, dtmf, dtmfClock)
}

// selectedDTMF answers telephone-event with the peer's own payload type and
// clock rate. AIS offers AMR-WB as 101 with telephone-event/16000, the form
// the calling handset uses; a fixed 101/8000 collides with the codec or needs
// a transcoder, and AIS hangs up with "Media Negotiation Failed".
func selectedDTMF(endpoint rtpEndpoint) (int, int) {
	if endpoint.DTMFPayloadType != 0 && endpoint.DTMFPayloadType != endpoint.PayloadType {
		return int(endpoint.DTMFPayloadType), endpoint.DTMFClockRate
	}
	if int(endpoint.PayloadType) != dtmfPayloadType {
		return dtmfPayloadType, 8000
	}
	for payload := 96; payload <= 127; payload++ {
		if payload != int(endpoint.PayloadType) {
			return payload, 8000
		}
	}
	return dtmfPayloadType, 8000
}

func renderPlainAudioSDP(port int, payloads, attributes []string, dtmf, dtmfClock int) string {
	return fmt.Sprintf(
		"v=0\r\no=hideck 0 0 IN IP4 127.0.0.1\r\ns=HiDeck Phone\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP %s\r\n%s\r\na=rtpmap:%d telephone-event/%d\r\na=fmtp:%d 0-15\r\na=ptime:20\r\na=sendrecv\r\n",
		port, strings.Join(payloads, " "), strings.Join(attributes, "\r\n"), dtmf, dtmfClock, dtmf,
	)
}

func advertisedAudioCodecs(realtimeCodecs []string) ([]string, []string) {
	payloads := make([]string, 0, len(realtimeCodecs)+3)
	attributes := make([]string, 0, len(realtimeCodecs)*2+2)
	seen := make(map[string]struct{})
	for _, codec := range realtimeCodecs {
		codec = strings.ToUpper(strings.TrimSpace(codec))
		if _, duplicate := seen[codec]; duplicate {
			continue
		}
		seen[codec] = struct{}{}
		switch codec {
		case "AMR-WB":
			payloads = append(payloads, strconv.Itoa(amrWBPayloadType), strconv.Itoa(amrWBOAPayloadType))
			attributes = append(attributes,
				fmt.Sprintf("a=rtpmap:%d AMR-WB/16000", amrWBPayloadType),
				fmt.Sprintf("a=fmtp:%d mode-change-capability=2; max-red=0", amrWBPayloadType),
				fmt.Sprintf("a=rtpmap:%d AMR-WB/16000", amrWBOAPayloadType),
				fmt.Sprintf("a=fmtp:%d octet-align=1; mode-change-capability=2; max-red=0", amrWBOAPayloadType),
			)
		case "AMR":
			payloads = append(payloads, strconv.Itoa(amrPayloadType), strconv.Itoa(amrOAPayloadType))
			attributes = append(attributes,
				fmt.Sprintf("a=rtpmap:%d AMR/8000", amrPayloadType),
				fmt.Sprintf("a=fmtp:%d mode-change-capability=2; max-red=0", amrPayloadType),
				fmt.Sprintf("a=rtpmap:%d AMR/8000", amrOAPayloadType),
				fmt.Sprintf("a=fmtp:%d octet-align=1; mode-change-capability=2; max-red=0", amrOAPayloadType),
			)
		case "EVS":
			payloads = append(payloads, strconv.Itoa(evsPayloadType))
			attributes = append(attributes,
				fmt.Sprintf("a=rtpmap:%d EVS/16000", evsPayloadType),
				fmt.Sprintf("a=fmtp:%d evs-mode-switch=1; hf-only=0; br=6.6-23.85; bw=wb; ch-aw-recv=-1; max-red=0", evsPayloadType),
			)
		}
	}
	payloads = append(payloads, "0", "8", strconv.Itoa(dtmfPayloadType))
	attributes = append(attributes, "a=rtpmap:0 PCMU/8000", "a=rtpmap:8 PCMA/8000")
	return payloads, attributes
}

func parseRTPEndpoint(raw string, supported ...string) (rtpEndpoint, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	host, port := "", 0
	var payloadTypes []int
	codecs, fmtps := make(map[int]rtpCodec), make(map[int]string)
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		switch {
		case len(fields) >= 3 && strings.HasPrefix(line, "c="):
			host = fields[len(fields)-1]
		case len(fields) >= 4 && strings.HasPrefix(line, "m=audio"):
			port, _ = strconv.Atoi(fields[1])
			payloadTypes = appendPayloadTypes(payloadTypes, fields[3:])
		case strings.HasPrefix(line, "a=rtpmap:"):
			parseRTPMap(line, codecs)
		case strings.HasPrefix(line, "a=fmtp:"):
			parseEndpointFMTP(line, fmtps)
		}
	}
	if host == "" || port <= 0 {
		return rtpEndpoint{}, errors.New("phone: SDP has no usable RTP endpoint")
	}
	endpoint, err := selectRTPEndpoint(endpointSelection{
		host: host, port: port, payloadTypes: payloadTypes,
		codecs: codecs, fmtps: fmtps, supported: supported,
	})
	if err != nil {
		return endpoint, err
	}
	for _, clockRate := range []int{endpoint.ClockRate, 8000} {
		for _, payloadType := range payloadTypes {
			codec := codecs[payloadType]
			if codec.name == "TELEPHONE-EVENT" && codec.clockRate == clockRate && payloadType > 0 && payloadType < 128 {
				endpoint.DTMFPayloadType, endpoint.DTMFClockRate = uint8(payloadType), clockRate
				return endpoint, nil
			}
		}
	}
	return endpoint, nil
}

func appendPayloadTypes(destination []int, fields []string) []int {
	for _, field := range fields {
		if value, err := strconv.Atoi(field); err == nil {
			destination = append(destination, value)
		}
	}
	return destination
}

func parseRTPMap(line string, codecs map[int]rtpCodec) {
	parts := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "a=rtpmap:"))
	if len(parts) != 2 {
		return
	}
	payloadType, err := strconv.Atoi(parts[0])
	encoding := strings.Split(parts[1], "/")
	if err != nil || len(encoding) < 2 {
		return
	}
	clockRate, err := strconv.Atoi(encoding[1])
	if err == nil {
		codecs[payloadType] = rtpCodec{name: strings.ToUpper(encoding[0]), clockRate: clockRate}
	}
}

func parseEndpointFMTP(line string, fmtps map[int]string) {
	parts := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "a=fmtp:"))
	if len(parts) < 2 {
		return
	}
	payloadType, err := strconv.Atoi(parts[0])
	if err == nil {
		fmtps[payloadType] = strings.Join(parts[1:], " ")
	}
}

func selectRTPEndpoint(selection endpointSelection) (rtpEndpoint, error) {
	allowed := supportedCodecSet(selection.supported)
	unavailableAMR := ""
	for _, payloadType := range selection.payloadTypes {
		codec := selection.codecs[payloadType]
		if payloadType == 0 && codec.name == "" {
			codec = rtpCodec{name: "PCMU", clockRate: 8000}
		}
		if payloadType == 8 && codec.name == "" {
			codec = rtpCodec{name: "PCMA", clockRate: 8000}
		}
		if codec.name != "PCMU" && codec.name != "PCMA" && codec.name != "AMR" && codec.name != "AMR-WB" && codec.name != "EVS" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[codec.name]; !ok {
				if codec.name == "AMR" || codec.name == "AMR-WB" || codec.name == "EVS" {
					unavailableAMR = codec.name
				}
				continue
			}
		}
		address, err := net.ResolveUDPAddr("udp", net.JoinHostPort(strings.Trim(selection.host, "[]"), strconv.Itoa(selection.port)))
		if err != nil {
			return rtpEndpoint{}, fmt.Errorf("phone: resolve RTP endpoint: %w", err)
		}
		return rtpEndpoint{
			Address: address, Codec: codec.name, PayloadType: uint8(payloadType),
			ClockRate: codec.clockRate, Fmtp: selection.fmtps[payloadType],
		}, nil
	}
	if unavailableAMR != "" {
		return rtpEndpoint{}, fmt.Errorf("phone: negotiated %s codec requires an unavailable encoder", unavailableAMR)
	}
	return rtpEndpoint{}, errors.New("phone: negotiated RTP audio codec is unsupported")
}

func supportedCodecSet(codecs []string) map[string]struct{} {
	result := make(map[string]struct{}, len(codecs))
	for _, codec := range codecs {
		if codec = strings.ToUpper(strings.TrimSpace(codec)); codec != "" {
			result[codec] = struct{}{}
		}
	}
	return result
}
