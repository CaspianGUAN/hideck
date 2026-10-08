package voice

import (
	"strings"

	"github.com/iniwex5/vowifi-go/internal/vowifi/voice/callstate"
)

// sdpQoSReservedLocal is the RFC 3312 / IR.51 originating offer after the
// Wi-Fi access resources are available. IR.51 still requires preconditions in
// this state, and IR.92 requires a later UPDATE for the status/codec update.
const sdpQoSReservedLocal = "" +
	"a=curr:qos local sendrecv\r\n" +
	"a=curr:qos remote none\r\n" +
	"a=des:qos mandatory local sendrecv\r\n" +
	"a=des:qos optional remote sendrecv\r\n"

// sdpQoSEstablishedRemote is the RFC 3312 current remote status learned from
// the early offer/answer exchange and used by the status UPDATE and re-INVITEs.
const sdpQoSEstablishedRemote = "a=curr:qos remote sendrecv"

func sdpHasPreconditions(sdp string) bool {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(strings.ToLower(line), "a=curr:qos") ||
			strings.HasPrefix(strings.ToLower(line), "a=des:qos") {
			return true
		}
	}
	return false
}

func sdpQoSCurrent(sdp, side string) string {
	prefix := "a=curr:qos " + strings.ToLower(strings.TrimSpace(side)) + " "
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, "\r")))
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func advertiseEstablishedSessionQoS(sdp string) string {
	if strings.TrimSpace(sdp) == "" || !sdpHasPreconditions(sdp) {
		return sdp
	}
	var rewritten strings.Builder
	for _, source := range splitSDPTextLines(sdp) {
		line := strings.TrimRight(source, "\r")
		trimmed := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(trimmed, "a=curr:qos remote none") {
			line = sdpQoSEstablishedRemote
		}
		rewritten.WriteString(line)
		rewritten.WriteString("\r\n")
	}
	return rewritten.String()
}

// ensureTerminatingPreconditions adds the terminating QoS answer. Local
// resources are already available on Wi-Fi. Remote current status is copied
// from the offerer's local current status.
func ensureTerminatingPreconditions(offer, answer string) string {
	if strings.TrimSpace(answer) == "" || sdpHasPreconditions(answer) {
		return answer
	}
	remote := sdpQoSCurrent(offer, "local")
	if remote == "" {
		remote = "none"
	}
	qos := "a=curr:qos local sendrecv\r\n" +
		"a=curr:qos remote " + remote + "\r\n" +
		"a=des:qos mandatory local sendrecv\r\n" +
		"a=des:qos mandatory remote sendrecv\r\n"
	return insertAudioQoS(answer, qos)
}

func insertAudioQoS(sdp, qos string) string {
	lines := splitSDPTextLines(sdp)
	audioIndex := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "m=audio ") {
			audioIndex = index
			break
		}
	}
	if audioIndex < 0 {
		return sdp
	}
	insertAt := len(lines)
	for index := audioIndex + 1; index < len(lines); index++ {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[index])), "m=") {
			insertAt = index
			break
		}
	}
	qosLines := splitSDPTextLines(qos)
	result := make([]string, 0, len(lines)+len(qosLines))
	result = append(result, lines[:insertAt]...)
	result = append(result, qosLines...)
	result = append(result, lines[insertAt:]...)
	return strings.Join(result, "\r\n") + "\r\n"
}

func refreshTerminatingRemoteQoS(sdp, remoteCurrent string) string {
	if !sdpHasPreconditions(sdp) {
		return sdp
	}
	remoteCurrent = strings.ToLower(strings.TrimSpace(remoteCurrent))
	if remoteCurrent == "" {
		remoteCurrent = "none"
	}
	var rewritten strings.Builder
	for _, source := range splitSDPTextLines(sdp) {
		line := strings.TrimRight(source, "\r")
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "a=curr:qos remote ") {
			line = "a=curr:qos remote " + remoteCurrent
		}
		rewritten.WriteString(line)
		rewritten.WriteString("\r\n")
	}
	return rewritten.String()
}

// inboundOffererQoSPending reports that the offerer has not reserved the
// resources its own mandatory local precondition requires.
func inboundOffererQoSPending(offer string) bool {
	if !sdpHasPreconditions(offer) {
		return false
	}
	current, mandatory := parseSDPQoSStatus(offer)
	desired := mandatory["local"]
	if desired == 0 {
		return false
	}
	return current["local"]&desired != desired
}

func ensureOriginatingPreconditions(sdp string) string {
	if strings.TrimSpace(sdp) == "" || sdpHasPreconditions(sdp) {
		return sdp
	}
	lines := splitSDPTextLines(sdp)
	audioIndex := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "m=audio ") {
			audioIndex = index
			break
		}
	}
	if audioIndex < 0 {
		return sdp
	}
	insertAt := len(lines)
	for index := audioIndex + 1; index < len(lines); index++ {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[index])), "m=") {
			insertAt = index
			break
		}
	}
	qosLines := splitSDPTextLines(sdpQoSReservedLocal)
	result := make([]string, 0, len(lines)+len(qosLines))
	result = append(result, lines[:insertAt]...)
	result = append(result, qosLines...)
	result = append(result, lines[insertAt:]...)
	return strings.Join(result, "\r\n") + "\r\n"
}

func markLocalSDPSessionEstablished(call *Call) {
	if call == nil {
		return
	}
	client, ims := call.localSDPs()
	updated := advertiseEstablishedSessionQoS(ims)
	if updated == ims {
		return
	}
	call.setLocalSDP(client, updated)
}

func sdpPreconditionsSatisfied(remoteSDP string) bool {
	if !sdpHasPreconditions(remoteSDP) {
		return true
	}
	current, mandatory := parseSDPQoSStatus(remoteSDP)
	for statusType, desired := range mandatory {
		if current[statusType]&desired != desired {
			return false
		}
	}
	return true
}

func parseSDPQoSStatus(sdp string) (map[string]uint8, map[string]uint8) {
	current := make(map[string]uint8)
	mandatory := make(map[string]uint8)
	for _, source := range splitSDPTextLines(sdp) {
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(source)))
		switch {
		case len(fields) == 3 && fields[0] == "a=curr:qos":
			current[fields[1]] |= sdpQoSDirectionMask(fields[2])
		case len(fields) == 4 && fields[0] == "a=des:qos" && fields[1] == "mandatory":
			mandatory[fields[2]] |= sdpQoSDirectionMask(fields[3])
		}
	}
	return current, mandatory
}

func sdpQoSDirectionMask(direction string) uint8 {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "send":
		return 1
	case "recv":
		return 2
	case "sendrecv":
		return 3
	default:
		return 0
	}
}

func (c *Call) claimPreconditionStatusUpdate() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.preconditionUpdateSent {
		return false
	}
	c.preconditionUpdateSent = true
	return true
}

func (c *Call) setPreconditionMet(met bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.MediaState.PreconditionMet = met
	c.mu.Unlock()
}

func (c *Call) preconditionMetValue() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.MediaState.PreconditionMet
}

func (a *Agent) applyCallPreconditions(call *Call, remoteSDP string) {
	if call == nil {
		return
	}
	met := sdpPreconditionsSatisfied(remoteSDP)
	call.setPreconditionMet(met)
	if met {
		if call.CallState() == callstate.StatePreconditionWait {
			_ = call.TransitionChecked(callstate.StateEarlyMedia)
		}
		return
	}
	if call.CallState() == callstate.StateEarlyMedia {
		_ = call.TransitionChecked(callstate.StatePreconditionWait)
	}
}
