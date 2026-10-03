package swu

import (
	"fmt"
	"strings"
)

func normalizeMNC(mnc string) string {
	if len(mnc) == 2 {
		return "0" + mnc
	}
	return mnc
}

func normalizeMCC(mcc string) string { return mcc }

func effectiveMCCMNC(imsi string, cfg *Config) (string, string) {
	mcc, mnc := "", ""
	if len(imsi) >= 5 {
		mcc, mnc = imsi[:3], imsi[3:5]
	}
	if cfg != nil && cfg.MCC != "" {
		mcc = cfg.MCC
	}
	if cfg != nil && cfg.MNC != "" {
		mnc = cfg.MNC
	}
	return normalizeMCC(mcc), normalizeMNC(mnc)
}

func buildNAI(imsi string, cfg *Config) string {
	mcc, mnc := effectiveMCCMNC(imsi, cfg)
	return fmt.Sprintf("%s%s@nai.epc.mnc%s.mcc%s.3gppnetwork.org", naiPrefix(cfg), imsi, mnc, mcc)
}

// naiPrefix is the TS 23.003 19.3.2 permanent-identity leading digit: "0"
// asks for EAP-AKA, "6" for EAP-AKA'. ike_identity_mode "aka_prime" lets a
// carrier whose AAA requires EAP-AKA' (it signals AT_BIDDING) get it.
func naiPrefix(cfg *Config) string {
	if cfg != nil {
		switch strings.ToLower(strings.TrimSpace(cfg.IKEIdentityMode)) {
		case "aka_prime", "epc_nai_aka_prime":
			return "6"
		}
	}
	return "0"
}
