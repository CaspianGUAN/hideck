package voice

import (
	"strings"
)

// homeMCCFromDomain reads the MCC from an IMS home domain such as
// ims.mnc003.mcc520.3gppnetwork.org.
func homeMCCFromDomain(domain string) string {
	for _, label := range strings.Split(strings.ToLower(strings.TrimSpace(domain)), ".") {
		if strings.HasPrefix(label, "mcc") && len(label) == 6 {
			return label[3:]
		}
	}
	return ""
}

// muLawRegion reports whether the MCC belongs to a mu-law (PCMU) region:
// North America (3xx) and Japan (440, 441).
func muLawRegion(mcc string) bool {
	return strings.HasPrefix(mcc, "3") || mcc == "440" || mcc == "441"
}

// preferRegionalG711 lists PCMA before PCMU outside mu-law regions. AIS
// picks the first G.711 entry; a PCMU call to a Thai mobile fails with
// "Bearer capability not authorized" or loses media after a second, while
// PCMA calls (IVRs) work.
func preferRegionalG711(sdp, mcc string) string {
	if mcc == "" || muLawRegion(mcc) {
		return sdp
	}
	lines := splitSDPTextLines(sdp)
	for index, line := range lines {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "m=audio ") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 {
			return sdp
		}
		payloads := fields[3:]
		pcmu, pcma := -1, -1
		for position, payload := range payloads {
			switch payload {
			case "0":
				pcmu = position
			case "8":
				pcma = position
			}
		}
		if pcmu < 0 || pcma < 0 || pcma < pcmu {
			return sdp
		}
		payloads[pcmu], payloads[pcma] = payloads[pcma], payloads[pcmu]
		lines[index] = strings.Join(fields, " ")
		return strings.Join(lines, "\r\n") + "\r\n"
	}
	return sdp
}
