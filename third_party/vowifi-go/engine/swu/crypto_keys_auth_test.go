package swu

import (
	"errors"
	"testing"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

// A message that passed its integrity check but does not decode is a real
// failure, not an unauthenticated one to drop.
func TestAuthenticatedSyntaxErrorIsNotDropped(t *testing.T) {
	_, err := decodeAuthenticatedPayloads(ikev2.PayloadType(41), []byte{0x00, 0x00, 0x00, 0x02})
	var syntax *authenticatedSyntaxError
	if err == nil || !errors.As(err, &syntax) || errors.Is(err, errUnauthenticatedIKE) {
		t.Fatalf("err = %v, want an authenticated syntax error", err)
	}
}
