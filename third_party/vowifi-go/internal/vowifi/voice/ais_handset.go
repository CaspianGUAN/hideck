package voice

import (
	"strings"

	"github.com/emiago/sipgo/sip"
)

// AIS (520/01, 520/03) answers every MT call from HiDeck with BYE "Media
// Negotiation Failed" while an iPhone (iOS 16.2) on the same SIM, receiving the
// same INVITE, is accepted. The iPhone answers 180 then 200 with the SDP below;
// these helpers make HiDeck's responses match that capture (2026-10-08).
const (
	aisHandsetAllow     = "ACK,BYE,CANCEL,INFO,INVITE,MESSAGE,NOTIFY,OPTIONS,PRACK,REFER,UPDATE"
	aisHandsetUserAgent = "iOS/16.2 iPhone"
	aisHandsetSupported = "100rel,path,replaces"
	aisHandsetMMTelICSI = `"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`
)

// aisHomeDomain reports an AIS Thailand home domain (520/01, 520/03).
func aisHomeDomain(domain string) bool {
	domain = strings.ToLower(domain)
	return homeMCCFromDomain(domain) == "520" &&
		(strings.Contains(domain, "mnc001.") || strings.Contains(domain, "mnc003."))
}

func (a *Agent) aisHandsetResponses() bool {
	return a != nil && aisHomeDomain(a.imsSnapshot().Realm)
}

// aisHandsetContact is the INVITE Request-URI without a user part, with the
// iPhone's dialog feature tags: <sip:[ip]:port>;icsi-ref;mid-call;...
func aisHandsetContact(request *sip.Request) *sip.ContactHeader {
	if request == nil {
		return nil
	}
	uri := sip.Uri{Scheme: "sip", Host: request.Recipient.Host, Port: request.Recipient.Port}
	params := sip.NewParams()
	params.Add("+g.3gpp.icsi-ref", aisHandsetMMTelICSI)
	params.Add("+g.3gpp.mid-call", "")
	params.Add("+g.3gpp.ps2cs-srvcc-orig-pre-alerting", "")
	params.Add("+g.3gpp.srvcc-alerting", "")
	return &sip.ContactHeader{Address: uri, Params: params}
}

// applyAISHandsetHeaders rewrites an MT 18x or 2xx toward AIS. It returns the
// Contact it set, or nil when the call is not on AIS.
func (a *Agent) applyAISHandsetHeaders(response *sip.Response, request *sip.Request, final bool) *sip.ContactHeader {
	if !a.aisHandsetResponses() || response == nil {
		return nil
	}
	contact := aisHandsetContact(request)
	if contact == nil {
		return nil
	}
	response.RemoveHeader("Contact")
	response.AppendHeader(contact)
	response.RemoveHeader("Supported")
	supported := aisHandsetSupported
	if final {
		supported += ",timer"
	}
	response.AppendHeader(sip.NewHeader("Supported", supported))
	response.RemoveHeader("Allow")
	response.AppendHeader(sip.NewHeader("Allow", aisHandsetAllow))
	response.RemoveHeader("User-Agent")
	response.AppendHeader(sip.NewHeader("User-Agent", aisHandsetUserAgent))
	if response.GetHeader("P-Access-Network-Info") == nil {
		if pani := a.dialog.Context().CachedPANIHdr; pani != nil && strings.TrimSpace(pani.Value()) != "" {
			response.AppendHeader(sip.NewHeader("P-Access-Network-Info", strings.TrimSpace(pani.Value())))
		}
	}
	return contact
}

// aisHandsetSDP drops b= lines and adds a=maxptime:240 after a=ptime, as the
// iPhone answer does.
func aisHandsetSDP(sdp string) string {
	if strings.TrimSpace(sdp) == "" {
		return sdp
	}
	lines := splitSDPTextLines(strings.TrimRight(sdp, "\r\n"))
	result := make([]string, 0, len(lines)+1)
	hasMaxPtime := strings.Contains(sdp, "a=maxptime:")
	for _, line := range lines {
		if strings.HasPrefix(line, "b=") {
			continue
		}
		result = append(result, line)
		if strings.HasPrefix(line, "a=ptime:") && !hasMaxPtime {
			result = append(result, "a=maxptime:240")
			hasMaxPtime = true
		}
	}
	return strings.Join(result, "\r\n") + "\r\n"
}
