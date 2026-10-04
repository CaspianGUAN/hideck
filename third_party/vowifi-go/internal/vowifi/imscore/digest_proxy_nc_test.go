package imscore

import (
	"strings"
	"testing"
)

func challengeResponse(t *testing.T, status int, header string) *sipResponse {
	t.Helper()
	raw := "SIP/2.0 " + map[int]string{401: "401 Unauthorized", 407: "407 Proxy Authentication Required"}[status] + "\r\n" +
		"Via: SIP/2.0/UDP 192.0.2.1;branch=z9hG4bK1\r\nCall-ID: c1\r\nCSeq: 1 REGISTER\r\n" +
		header + `: Digest realm="ims.example", nonce="AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", algorithm=AKAv1-MD5, qop="auth"` + "\r\nContent-Length: 0\r\n\r\n"
	response, err := parseSIPResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// RFC 3261 22.3: a 407 Proxy-Authenticate challenge is answered with
// Proxy-Authorization; a 401 keeps Authorization.
func TestRegisterAnswersProxyChallengeWithProxyAuthorization(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	for _, tc := range []struct {
		status        int
		challenge     string
		want, notWant string
	}{
		{407, "Proxy-Authenticate", "Proxy-Authorization", "Authorization"},
		{401, "WWW-Authenticate", "Authorization", "Proxy-Authorization"},
	} {
		challenge, err := service.extractChallenge(challengeResponse(t, tc.status, tc.challenge), tc.status)
		if err != nil {
			t.Fatalf("%d: extractChallenge: %v", tc.status, err)
		}
		session := service.emergencyRegisterSession()
		session.challenge = challenge
		request := service.buildRegister(session, `Digest username="u", response="r"`)
		if got := rawSIPHeaderValue(request, tc.want); got == "" {
			t.Fatalf("%d challenge: REGISTER lacks %s:\n%s", tc.status, tc.want, request)
		}
		if strings.Contains(request, "\r\n"+tc.notWant+":") {
			t.Fatalf("%d challenge: REGISTER also carries %s:\n%s", tc.status, tc.notWant, request)
		}
	}
}

func TestInitialRegisterKeepsAuthorizationAfterProxyChallenge(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	challenge, err := service.extractChallenge(challengeResponse(t, 407, "Proxy-Authenticate"), 407)
	if err != nil {
		t.Fatal(err)
	}
	session := service.emergencyRegisterSession()
	session.challenge = challenge
	if request := service.buildRegister(session, ""); rawSIPHeaderValue(request, "Authorization") == "" {
		t.Fatalf("unauthenticated REGISTER lost its Authorization placeholder:\n%s", request)
	}
}

// RFC 2617 3.2.2: nc counts the requests answering one nonce and restarts for
// a new challenge.
func TestDigestNonceCountIncrementsPerNonce(t *testing.T) {
	nc := func(challenge *DigestChallenge) string {
		authorization, err := buildDigestAuthorization(challenge, "u@ims.example", "REGISTER", "sip:ims.example", []byte{1, 2, 3, 4}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, after, _ := strings.Cut(authorization, "nc=")
		value, _, _ := strings.Cut(after, ",")
		return value
	}
	first := &DigestChallenge{Realm: "ims.example", Nonce: "bm9uY2U=", QOP: "auth"}
	if got := nc(first); got != "00000001" {
		t.Fatalf("first nc = %q", got)
	}
	if got := nc(first); got != "00000002" {
		t.Fatalf("second nc for the same nonce = %q", got)
	}
	if got := nc(&DigestChallenge{Realm: "ims.example", Nonce: "bmV3", QOP: "auth"}); got != "00000001" {
		t.Fatalf("nc for a new nonce = %q", got)
	}
}
