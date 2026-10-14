package simidentity

import "testing"

func TestCanonicalICCID(t *testing.T) {
	for input, want := range map[string]string{
		" \"8986 00123456f7890123\" ": "898600123456F7890123",
		"898600abcdef123456ff":        "898600ABCDEF123456",
		"8901234567890123456F":        "8901234567890123456",
		"ICCF":                        "ICCF", "card-a": "card-a",
		"imsi:AbcF": "imsi:AbcF", "reader-imsi-AbcF": "reader-imsi-AbcF", "": "",
	} {
		if got := CanonicalICCID(input); got != want {
			t.Fatalf("CanonicalICCID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDecodeICCID(t *testing.T) {
	for _, test := range []struct {
		data []byte
		want string
	}{
		{[]byte{0x98, 0x10, 0x32, 0xF4, 0x65, 0xF7}, "8901234F567"},
		{[]byte{0x98, 0xBA, 0xDC, 0xFE, 0x10}, "89ABCDEF01"},
		{nil, ""},
	} {
		if got := DecodeICCID(test.data); got != test.want {
			t.Fatalf("decode = %q, want %q", got, test.want)
		}
	}
}
