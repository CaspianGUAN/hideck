package qmi

import "strings"

// ICCID is not an IMSI: retain every interior nibble, including A-F.
func decodeICCID(data []byte) string {
	const hexDigits = "0123456789ABCDEF"
	decoded := make([]byte, 0, 2*len(data))
	for _, octet := range data {
		decoded = append(decoded, hexDigits[octet&0x0F], hexDigits[octet>>4])
	}
	return strings.TrimRight(string(decoded), "F")
}
