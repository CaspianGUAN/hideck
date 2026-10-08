package voice

import (
	"strconv"
	"strings"
	"time"
)

// shapeIMSSDP gives an SDP sent to IMS the lines a handset sends. The P-CSCF
// authorizes media with the PCRF from this SDP; without b=AS it has no
// bandwidth to request, and AIS answers "Media Negotiation Failed" (MT) or
// stops downlink media after a second (MO). A zero o= session id is also
// replaced, since some SBCs treat it as unset.
func shapeIMSSDP(sdp string) string {
	if strings.TrimSpace(sdp) == "" {
		return sdp
	}
	lines := splitSDPTextLines(strings.TrimRight(sdp, "\r\n"))
	bandwidth := sdpAudioBandwidthKbps(lines)
	hasBandwidth := false
	for _, line := range lines {
		if strings.HasPrefix(line, "b=") {
			hasBandwidth = true
			break
		}
	}
	result := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "o="):
			line = nonZeroSDPOrigin(line)
		case line == "s=HiDeck Phone":
			line = "s=-"
		}
		result = append(result, line)
		if strings.HasPrefix(line, "m=audio ") && !hasBandwidth && bandwidth > 0 {
			result = append(result, "b=AS:"+strconv.Itoa(bandwidth))
		}
	}
	shaped := strings.Join(result, "\r\n")
	if strings.HasSuffix(sdp, "\n") {
		shaped += "\r\n"
	}
	return shaped
}

func nonZeroSDPOrigin(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 6 || fields[1] != "0" || fields[2] != "0" {
		return line
	}
	session := strconv.FormatInt(time.Now().Unix(), 10)
	fields[1], fields[2] = session, session
	return strings.Join(fields, " ")
}

// sdpAudioBandwidthKbps is the b=AS for the largest audio codec offered,
// including IPv6/UDP/RTP overhead at 50 packets per second.
func sdpAudioBandwidthKbps(lines []string) int {
	payloads := map[string]bool{}
	for _, line := range lines {
		if strings.HasPrefix(line, "m=audio ") {
			fields := strings.Fields(line)
			for _, payload := range fields[min(3, len(fields)):] {
				payloads[payload] = true
			}
		}
	}
	best := 0
	consider := func(name string) {
		value := 0
		switch strings.ToUpper(name) {
		case "PCMA", "PCMU":
			value = 88
		case "AMR-WB", "EVS":
			value = 49
		case "AMR":
			value = 38
		}
		if value > best {
			best = value
		}
	}
	if payloads["0"] {
		consider("PCMU")
	}
	if payloads["8"] {
		consider("PCMA")
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "a=rtpmap:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "a=rtpmap:"))
		if len(fields) != 2 || !payloads[fields[0]] {
			continue
		}
		name, _, _ := strings.Cut(fields[1], "/")
		consider(name)
	}
	return best
}
