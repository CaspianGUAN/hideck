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
