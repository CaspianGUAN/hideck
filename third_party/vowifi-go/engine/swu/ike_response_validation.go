package swu

import (
	"fmt"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

// Authenticate before a response consumes a pending task or legacy waiter.
// Invalid ciphertext must leave its original retransmission timer running.
func (s *Session) authenticateIKEResponse(header *ikev2.IKEHeader, raw []byte) error {
	if s.validBootstrapIKEResponse(header) {
		return nil // IKE_SA_INIT has no shared keys yet.
	}
	packet, err := ikev2.DecodePacket(raw)
	if err != nil {
		return fmt.Errorf("%w: decode IKE response: %w", errUnauthenticatedIKE, err)
	}
	// Rekey can retire and wipe keys concurrently with response dispatch.
	s.mu.RLock()
	defer s.mu.RUnlock()
	context, _, err := s.ikeContextForHeaderLocked(header)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnauthenticatedIKE, err)
	}
	if err := validateIKEContextRole(context, header); err != nil {
		return fmt.Errorf("%w: %w", errUnauthenticatedIKE, err)
	}
	_, err = s.decryptAndParseWithKeys(packet, context.keys)
	return err
}
