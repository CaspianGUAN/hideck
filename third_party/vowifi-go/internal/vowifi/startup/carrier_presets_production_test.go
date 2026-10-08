package startup

import (
	"testing"

	"github.com/iniwex5/vowifi-go/internal/vowifi/policy"
	"github.com/iniwex5/vowifi-go/internal/vowifi/profile"
)

type carrierStartupExpectation struct {
	name, mcc, mnc, normalizedMNC, imsi, presetID string
	epdg, epdgSource, espFirst, templateID        string
	e911Provider, akaMode                         string
	dpdSeconds, reauthSeconds                     int
}

var originalCarrierStartupExpectations = []carrierStartupExpectation{
	{name: "Vodafone NL", mcc: "204", mnc: "04", normalizedMNC: "004", imsi: "204040000000001", presetID: "vodafone_nl_20404", epdg: "epdg.epc.mnc004.mcc204.pub.3gppnetwork.org", epdgSource: "preset", espFirst: "aes256-sha256", templateID: "vodafone_nl_20404_ios", akaMode: "minimal", dpdSeconds: 120},
	{name: "KPN/Simyo NL", mcc: "204", mnc: "08", normalizedMNC: "008", imsi: "204080000000001", presetID: "kpn_nl_20408", epdg: "epdg.epc.mnc008.mcc204.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "kpn_nl_20408", akaMode: "minimal", dpdSeconds: 120},
	{name: "Orange FR", mcc: "208", mnc: "01", normalizedMNC: "001", imsi: "208010000000001", presetID: "orange_fr_20801", epdg: "epdg.epc.mnc001.mcc208.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "orange_fr_20801", akaMode: "minimal", dpdSeconds: 120},
	{name: "Sunrise", mcc: "228", mnc: "002", normalizedMNC: "002", imsi: "228020000000001", presetID: "sunrise_22802", epdg: "epdg.epc.mnc002.mcc228.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes128-sha256", templateID: "defaultIMSRegisterTemplate", akaMode: "checkcode", dpdSeconds: 120},
	{name: "giffgaff", mcc: "234", mnc: "10", normalizedMNC: "010", imsi: "234102356143376", presetID: "giffgaff_23410", epdg: "epdg.epc.mnc010.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha512", templateID: "giffgaff", akaMode: "minimal", dpdSeconds: 120},
	{name: "Vodafone UK", mcc: "234", mnc: "15", normalizedMNC: "015", imsi: "234150000000001", presetID: "vodafone_uk_23415", epdg: "epdg.epc.mnc015.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha512", templateID: "vodafone_uk_23415", akaMode: "minimal", dpdSeconds: 120},
	{name: "Three UK", mcc: "234", mnc: "020", normalizedMNC: "020", imsi: "234200000000001", presetID: "three_uk_234020", epdg: "epdg.epc.mnc020.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes128-sha256", templateID: "three_uk_234020", akaMode: "minimal", dpdSeconds: 120},
	{name: "1GLOBAL", mcc: "234", mnc: "25", normalizedMNC: "025", imsi: "234250000000001", presetID: "oneglobal_23425", epdg: "epdg.epc.mnc025.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "oneglobal_23425", akaMode: "minimal", dpdSeconds: 120},
	{name: "Lycamobile UK", mcc: "234", mnc: "26", normalizedMNC: "026", imsi: "234260000000001", presetID: "lycamobile_uk_23426", epdg: "epdg.epc.mnc026.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "lycamobile_uk_23426", akaMode: "minimal", dpdSeconds: 120},
	{name: "EE UK 30", mcc: "234", mnc: "30", normalizedMNC: "030", imsi: "234300000000001", presetID: "ee_uk_23430", epdg: "epdg.epc.mnc030.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "ee_uk_23430", akaMode: "minimal", dpdSeconds: 120},
	{name: "EE UK 31", mcc: "234", mnc: "31", normalizedMNC: "031", imsi: "234310000000001", presetID: "ee_uk_23431", epdg: "epdg.epc.mnc031.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "ee_uk_23431", akaMode: "minimal", dpdSeconds: 120},
	{name: "EE UK 32", mcc: "234", mnc: "32", normalizedMNC: "032", imsi: "234320000000001", presetID: "ee_uk_23432", epdg: "epdg.epc.mnc032.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "ee_uk_23432", akaMode: "minimal", dpdSeconds: 120},
	{name: "CTExcel", mcc: "234", mnc: "33", normalizedMNC: "033", imsi: "234336575868434", presetID: "CTEUK_23433", epdg: "epdg.epc.mnc033.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256gcm16", templateID: "CTEUK_23433", akaMode: "minimal", dpdSeconds: 120},
	{name: "Lebara UK", mcc: "234", mnc: "87", normalizedMNC: "087", imsi: "234870000000001", presetID: "lebara_uk_23487", epdg: "epdg.epc.mnc087.mcc234.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha512", templateID: "lebara_uk_23487", akaMode: "minimal", dpdSeconds: 120},
	{name: "Elisa EE", mcc: "248", mnc: "02", normalizedMNC: "002", imsi: "248020000000001", presetID: "elisa_ee_24802", epdg: "epdg.epc.mnc002.mcc248.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "elisa_ee_24802", akaMode: "minimal", dpdSeconds: 120},
	{name: "Telekom DE", mcc: "262", mnc: "01", normalizedMNC: "001", imsi: "262010000000001", presetID: "telekom_de_26201", epdg: "epdg.epc.mnc001.mcc262.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "telekom_de_26201", akaMode: "minimal", dpdSeconds: 120},
	{name: "Vodafone DE", mcc: "262", mnc: "02", normalizedMNC: "002", imsi: "262020000000001", presetID: "vodafone_de_26202", epdg: "epdg.epc.mnc002.mcc262.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "vodafone_de_26202", akaMode: "minimal", dpdSeconds: 120},
	{name: "O2 DE 03", mcc: "262", mnc: "03", normalizedMNC: "003", imsi: "262030000000001", presetID: "O2_de_26203", epdg: "epdg.epc.mnc003.mcc262.pub.3gppnetwork.org", epdgSource: "preset", espFirst: "aes256-sha256", templateID: "O2_de_26203_ios", akaMode: "minimal", dpdSeconds: 120},
	{name: "O2 DE 07", mcc: "262", mnc: "07", normalizedMNC: "007", imsi: "262070000000001", presetID: "O2_de_26207_alias", epdg: "epdg.epc.mnc007.mcc262.pub.3gppnetwork.org", epdgSource: "preset", espFirst: "aes256-sha256", templateID: "O2_de_26203", akaMode: "minimal", dpdSeconds: 120},
	{name: "T-Mobile 240", mcc: "310", mnc: "240", normalizedMNC: "240", imsi: "310240000000001", presetID: "T-Mobile_240", epdg: "epdg.epc.mnc240.mcc310.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes128-sha256", templateID: "defaultIMSRegisterTemplate", e911Provider: "T-Mobile_entitlement", akaMode: "minimal", dpdSeconds: 120},
	{name: "T-Mobile 260", mcc: "310", mnc: "260", normalizedMNC: "260", imsi: "310260000000001", presetID: "T-Mobile_260", epdg: "epdg.epc.mnc260.mcc310.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes128-sha256", templateID: "defaultIMSRegisterTemplate", e911Provider: "T-Mobile_entitlement", akaMode: "minimal", dpdSeconds: 120},
	{name: "AT&T 280", mcc: "310", mnc: "280", normalizedMNC: "280", imsi: "310280233621715", presetID: "att_310280", epdg: "epdg.epc.att.net", epdgSource: "preset", espFirst: "aes128-sha256", templateID: "att_310280", e911Provider: "att_entitlement", akaMode: "minimal", dpdSeconds: 120},
	{name: "LycaMobile AT&T", mcc: "310", mnc: "410", normalizedMNC: "410", imsi: "310410000000001", presetID: "LycaMobile_310410", epdg: "epdg.epc.att.net", epdgSource: "preset", espFirst: "aes128-sha256", templateID: "att_310280", e911Provider: "att_entitlement", akaMode: "minimal", dpdSeconds: 120},
	{name: "Y!mobile", mcc: "440", mnc: "00", normalizedMNC: "000", imsi: "440000000000001", presetID: "ymobile_44000", epdg: "epdg.epc.mnc000.mcc440.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "ymobile_44000", akaMode: "minimal", dpdSeconds: 120},
	{name: "Docomo", mcc: "440", mnc: "10", normalizedMNC: "010", imsi: "440100000000001", presetID: "docomo_44010", epdg: "epdg.epc.mnc010.mcc440.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "docomo_44010", akaMode: "minimal", dpdSeconds: 120},
	{name: "SoftBank", mcc: "440", mnc: "20", normalizedMNC: "020", imsi: "440200000000001", presetID: "softbank_44020", epdg: "epdg.epc.mnc020.mcc440.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "softbank_44020", akaMode: "minimal", dpdSeconds: 120},
	{name: "KDDI au", mcc: "440", mnc: "51", normalizedMNC: "051", imsi: "440510000000001", presetID: "kddi_44051", epdg: "epdg.epc.mnc051.mcc440.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "kddi_44051", akaMode: "minimal", dpdSeconds: 120},
	{name: "csl", mcc: "454", mnc: "000", normalizedMNC: "000", imsi: "454000000000001", presetID: "csl_454000", epdg: "epdg.epc.mnc000.mcc454.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "csl_454000", akaMode: "omit", dpdSeconds: 120},
	{name: "Three HK", mcc: "454", mnc: "003", normalizedMNC: "003", imsi: "454030000000001", presetID: "three_hk_454003", epdg: "wlan.three.com.hk", epdgSource: "preset", espFirst: "aes256-sha256", templateID: "three_hk_454003", akaMode: "checkcode", dpdSeconds: 120},
	{name: "CMHK 12", mcc: "454", mnc: "12", normalizedMNC: "012", imsi: "454120000000001", presetID: "cmhk_45412", epdg: "epdg.epc.mnc012.mcc454.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "cmhk_45412", akaMode: "minimal", dpdSeconds: 120},
	{name: "CMHK 13", mcc: "454", mnc: "13", normalizedMNC: "013", imsi: "454130000000001", presetID: "cmhk_45413", epdg: "epdg.epc.mnc013.mcc454.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "cmhk_45413", akaMode: "minimal", dpdSeconds: 120},
	{name: "One NZ", mcc: "530", mnc: "01", normalizedMNC: "001", imsi: "530010000000001", presetID: "one_nz_53001", epdg: "epdg.epc.mnc001.mcc530.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256gcm16", templateID: "defaultIMSRegisterTemplate", akaMode: "minimal", dpdSeconds: 120},
	{name: "Spark NZ", mcc: "530", mnc: "05", normalizedMNC: "005", imsi: "530050000000001", presetID: "spark_nz_53005", epdg: "epdg.epc.mnc005.mcc530.pub.3gppnetwork.spark.co.nz", epdgSource: "preset", espFirst: "aes256-sha256", templateID: "spark_nz_53005_ios", akaMode: "minimal", dpdSeconds: 120},
	{name: "2degrees NZ", mcc: "530", mnc: "24", normalizedMNC: "024", imsi: "530240000000001", presetID: "2degrees_nz_53024", epdg: "epdg.ims.2degrees.net.nz", epdgSource: "preset", espFirst: "aes256-sha512", templateID: "2degrees_nz_53024_ios", akaMode: "minimal", dpdSeconds: 120},
	{name: "Hotlink MY", mcc: "502", mnc: "12", normalizedMNC: "012", imsi: "502120000000001", presetID: "hotlink_my_50212", epdg: "epdg.epc.mnc012.mcc502.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "hotlink_my_50212", akaMode: "minimal", dpdSeconds: 120},
	{name: "Globe PH", mcc: "515", mnc: "02", normalizedMNC: "002", imsi: "515020000000001", presetID: "globe_ph_51502", epdg: "epdg.epc.mnc002.mcc515.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "globe_ph_51502", akaMode: "minimal", dpdSeconds: 120},
	{name: "Smart PH", mcc: "515", mnc: "03", normalizedMNC: "003", imsi: "515030000000001", presetID: "smart_ph_51503", epdg: "epdg.epc.mnc003.mcc515.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "smart_ph_51503", akaMode: "minimal", dpdSeconds: 120},
	{name: "DITO PH", mcc: "515", mnc: "66", normalizedMNC: "066", imsi: "515660000000001", presetID: "dito_51566", epdg: "epdg.epc.mnc066.mcc515.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "dito_51566", akaMode: "minimal", dpdSeconds: 120},
	{name: "AIS TH 01", mcc: "520", mnc: "01", normalizedMNC: "001", imsi: "520010000000001", presetID: "ais_th_52001", epdg: "epdg.epc.mnc001.mcc520.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "vocat", templateID: "ais_th_52001", akaMode: "omit", dpdSeconds: 120},
	{name: "AIS TH 03", mcc: "520", mnc: "03", normalizedMNC: "003", imsi: "520030000000001", presetID: "ais_th_52003", epdg: "epdg.epc.mnc003.mcc520.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "vocat", templateID: "ais_th_52003", akaMode: "omit", dpdSeconds: 120},
	{name: "MTN NG", mcc: "621", mnc: "30", normalizedMNC: "030", imsi: "621300000000001", presetID: "mtn_ng_62130", epdg: "epdg.epc.mnc030.mcc621.pub.3gppnetwork.org", epdgSource: "standard", espFirst: "aes256-sha256", templateID: "mtn_ng_62130", akaMode: "minimal", dpdSeconds: 120},
}

func TestPrepareStartProjectsEveryOriginalCarrierPreset(t *testing.T) {
	identity := profile.IMSIdentityResult{
		RequestedSource: "isim", ActualSource: "isim", AKAAppPreference: profile.AKAAppISIMStrict,
		Applied: true, IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
	}
	for _, expectation := range originalCarrierStartupExpectations {
		expectation := expectation
		t.Run(expectation.name, func(t *testing.T) {
			prepared, err := PrepareStart("device", profile.Profile{
				IMSI: expectation.imsi, MCC: expectation.mcc, MNC: expectation.mnc,
			}, "", identity, nil, nil)
			if err != nil {
				t.Fatalf("PrepareStart: %v", err)
			}
			assertOriginalCarrierProjection(t, prepared, expectation)
		})
	}
}

func assertOriginalCarrierProjection(
	t *testing.T,
	prepared profile.PreparedSession,
	want carrierStartupExpectation,
) {
	t.Helper()
	plan := prepared.CarrierPlan
	if plan.Metadata.MCC != want.mcc || plan.Metadata.MNC != want.normalizedMNC ||
		plan.Metadata.PresetID != want.presetID || plan.Metadata.MatchedTemplate != want.presetID {
		t.Fatalf("carrier metadata = %+v", plan.Metadata)
	}
	if prepared.EPDGAddr != want.epdg || prepared.EPDGSource != want.epdgSource {
		t.Fatalf("ePDG = %q source %q", prepared.EPDGAddr, prepared.EPDGSource)
	}
	wantEmergency := policy.DefaultCarrierEmergencyEPDGAddr(want.mcc, want.normalizedMNC)
	if plan.EPDG.EmergencyAddr != wantEmergency || prepared.EPDGAddr == wantEmergency {
		t.Fatalf("emergency ePDG = %q ordinary = %q", plan.EPDG.EmergencyAddr, prepared.EPDGAddr)
	}
	if plan.IKE.DPDIntervalSeconds != want.dpdSeconds || plan.IKE.ReauthIntervalSeconds != want.reauthSeconds ||
		plan.IKE.AKAChallengeMode != want.akaMode || len(plan.IKE.ESPProposals) == 0 ||
		plan.IKE.ESPProposals[0] != want.espFirst {
		t.Fatalf("IKE projection = %+v", plan.IKE)
	}
	if plan.IMS.RegisterTemplate.ID != want.templateID || plan.E911.Provider != want.e911Provider {
		t.Fatalf("IMS/E911 projection: template=%q E911=%+v", plan.IMS.RegisterTemplate.ID, plan.E911)
	}
}
