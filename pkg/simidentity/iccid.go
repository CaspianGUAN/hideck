package simidentity

import (
	"strings"
	"unicode"
)

// CanonicalICCID preserves interior hexadecimal digits; only trailing F is padding.
func CanonicalICCID(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "\"")
	if strings.HasPrefix(value, "imsi:") || strings.HasPrefix(value, "reader-imsi-") {
		return value
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	// Non-hex historical/synthetic keys have no BCD padding semantics.
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	}) != -1 {
		return value
	}
	return strings.TrimRight(strings.ToUpper(value), "F")
}

// DecodeICCID decodes EF_ICCID without using decimal-only IMSI/phone decoders.
func DecodeICCID(data []byte) string {
	const hexDigits = "0123456789ABCDEF"
	decoded := make([]byte, 0, 2*len(data))
	for _, octet := range data {
		decoded = append(decoded, hexDigits[octet&0x0F], hexDigits[octet>>4])
	}
	return strings.TrimRight(string(decoded), "F")
}
