package imscore

import "testing"

func TestSameRegisterContactHostMatchesEquivalentIPv6(t *testing.T) {
	if !sameRegisterContactHost("[2405:9800:9700:238f::1]", "[2405:9800:9700:238F:0:0:0:1]") {
		t.Fatal("equivalent IPv6 Contact hosts did not match")
	}
	if sameRegisterContactHost("[2405:9800:9700:238f::1]", "[2405:9800:9700:238f::2]") {
		t.Fatal("different IPv6 Contact hosts matched")
	}
	if !sameRegisterContactHost("Example.COM", "example.com") {
		t.Fatal("host names must compare case-insensitively")
	}
}
