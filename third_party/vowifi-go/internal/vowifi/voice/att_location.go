package voice

import (
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
)

// attHomeDomain reports an AT&T IMS home domain (one.att.net, or a 310
// network domain an AT&T preset falls back to).
func attHomeDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return domain == "one.att.net" || strings.HasSuffix(domain, ".att.net")
}

// applyATTLocation attaches the location AT&T requires in the first MT
// provisional response. Without it AT&T cancels the call within a second with
// Reason SIP;cause=480;text="Invalid Location". An iPhone sends Geolocation
// pointing at a civic PIDF-LO body carrying only the country (2026-10-09).
func (a *Agent) applyATTLocation(response *sip.Response, request *sip.Request) bool {
	if a == nil || response == nil || request == nil || !attHomeDomain(a.imsSnapshot().Realm) {
		return false
	}
	to := request.To()
	if to == nil || strings.TrimSpace(to.Address.User) == "" || strings.TrimSpace(to.Address.Host) == "" {
		return false
	}
	entity := to.Address.String()
	response.AppendHeader(sip.NewHeader("Geolocation", "<cid:"+to.Address.User+"@"+to.Address.Host+">"))
	response.AppendHeader(sip.NewHeader("Geolocation-Routing", "yes"))
	if response.GetHeader("P-Access-Network-Info") == nil {
		if pani := a.dialog.Context().CachedPANIHdr; pani != nil && strings.TrimSpace(pani.Value()) != "" {
			response.AppendHeader(sip.NewHeader("P-Access-Network-Info", strings.TrimSpace(pani.Value())))
		}
	}
	response.RemoveHeader("Content-Type")
	response.AppendHeader(sip.NewHeader("Content-Type", "application/pidf+xml"))
	response.SetBody([]byte(civicPIDF(entity, "US", time.Now())))
	return true
}

// civicPIDF is an iPhone's country-only civic PIDF-LO (RFC 4119).
func civicPIDF(entity, country string, now time.Time) string {
	return `<?xml version="1.0"?>` + "\r\n" +
		`<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:dm="urn:ietf:params:xml:ns:pidf:data-model" ` +
		`xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:gml="http://www.opengis.net/gml" ` +
		`xmlns:gs="http://www.opengis.net/pidflo/1.0" xmlns:cl="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" ` +
		`entity="` + entity + `">` + "\r\n" +
		"<tuple id=\"Wifi\">\r\n<status>\r\n<gp:geopriv>\r\n<gp:location-info>\r\n<cl:civicAddress>\r\n" +
		"<cl:country>" + country + "</cl:country>\r\n</cl:civicAddress>\r\n</gp:location-info>\r\n" +
		"<gp:usage-rules/>\r\n</gp:geopriv>\r\n</status>\r\n" +
		"<dm:timestamp>" + now.UTC().Format("2006-01-02T15:04:05Z") + "</dm:timestamp>\r\n</tuple>\r\n</presence>\r\n"
}

// attHandsetContact is the Contact an iPhone puts in its AT&T MT responses:
// the registered binding the INVITE was sent to, with the MMTel tags. AT&T
// routes PRACK and ACK to this Contact; with the pub-GRUU no PRACK arrived
// (2026-10-15), so the reliable 183 timed out.
func attHandsetContact(request *sip.Request) *sip.ContactHeader {
	if request == nil || strings.TrimSpace(request.Recipient.Host) == "" {
		return nil
	}
	uri := *request.Recipient.Clone()
	params := sip.NewParams()
	params.Add("+g.3gpp.icsi-ref", aisHandsetMMTelICSI)
	params.Add("+g.3gpp.mid-call", "")
	params.Add("+g.3gpp.smsip", "")
	params.Add("+g.3gpp.srvcc-alerting", "")
	params.Add("text", "")
	return &sip.ContactHeader{Address: uri, Params: params}
}

// applyATTHandsetContact replaces the Contact of an MT response toward AT&T
// and returns it, or nil when the call is not on AT&T.
func (a *Agent) applyATTHandsetContact(response *sip.Response, request *sip.Request) *sip.ContactHeader {
	if a == nil || response == nil || !attHomeDomain(a.imsSnapshot().Realm) {
		return nil
	}
	contact := attHandsetContact(request)
	if contact == nil {
		return nil
	}
	response.RemoveHeader("Contact")
	response.AppendHeader(contact)
	// An iPhone lists the same Allow and Supported in its AT&T 18x and 200.
	supported := aisHandsetSupported
	if response.StatusCode >= 200 {
		supported += ",timer"
	}
	response.RemoveHeader("Supported")
	response.AppendHeader(sip.NewHeader("Supported", supported))
	response.RemoveHeader("Allow")
	response.AppendHeader(sip.NewHeader("Allow", aisHandsetAllow))
	return contact
}
