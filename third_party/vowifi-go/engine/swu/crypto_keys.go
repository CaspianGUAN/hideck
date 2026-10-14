package swu

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/iniwex5/vowifi-go/engine/crypto"
	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

// encryptAndWrap encrypts and integrity-protects an IKE message (RFC 7296
// §3.2) and returns the full wire packet. The payload chain is encoded as the
// plaintext, encrypted with SK_ei (initiator) and integrity-protected with
// SK_ai, then wrapped in an Encrypted payload.
func (s *Session) encryptAndWrap(pkt *ikev2.IKEPacket) ([]byte, error) {
	return s.encryptAndWrapWithMsgID(pkt, packetIKEHeader(pkt).MessageID)
}

// encryptAndWrapWithMsgID encrypts the payload chain with an explicit message
// ID (used for retransmissions and rekey exchanges).
func (s *Session) encryptAndWrapWithMsgID(pkt *ikev2.IKEPacket, msgID uint32) ([]byte, error) {
	return s.encryptAndWrapWithKeys(pkt, msgID, s.ikeKeys)
}

func (s *Session) encryptAndWrapWithKeys(
	pkt *ikev2.IKEPacket,
	msgID uint32,
	keys *IKEKeys,
) ([]byte, error) {
	if keys == nil {
		return nil, errors.New("swu: no IKE SA keys")
	}
	encKey, integKey := ikeProtectionKeysFor(keys, packetIKEHeader(pkt).Flags&ikeInitiatorFlag == 0)
	cipher, err := crypto.PrepareCipher(s.encrAlg, encKey)
	if err != nil {
		return nil, fmt.Errorf("prepare cipher: %w", err)
	}
	encodedPayloads, err := ikev2.EncodePayloadChainChecked(pkt.Payloads)
	if err != nil {
		return nil, fmt.Errorf("encode IKE payloads: %w", err)
	}
	plain := padIKEPlaintext(encodedPayloads, cipher.BlockSize())
	iv := make([]byte, cipher.IVSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("generate IV: %w", err)
	}
	firstPayload := firstIKEPayloadType(pkt.Payloads)
	if s.aead {
		return s.encryptAEADIKE(pkt, msgID, firstPayload, plain, iv, cipher)
	}
	encrypted, err := cipher.Seal(nil, plain, iv, nil)
	if err != nil {
		return nil, fmt.Errorf("encrypt IKE message: %w", err)
	}
	integ := crypto.NewIntegrity(s.integAlg)
	if integ == nil {
		return nil, errors.New("swu: no integrity algorithm")
	}
	body := append(append([]byte{}, iv...), encrypted...)
	body = append(body, make([]byte, integ.OutputSize())...)
	raw, err := protectedIKEPacket(pkt, msgID, firstPayload, body).Encode()
	if err != nil {
		return nil, fmt.Errorf("encode protected IKE packet: %w", err)
	}
	checksum := integ.Compute(integKey, raw[:len(raw)-integ.OutputSize()])
	copy(raw[len(raw)-len(checksum):], checksum)
	return raw, nil
}

// decryptAndParse decrypts and parses an incoming IKE message. The packet must
// carry a single Encrypted payload; the decrypted payload chain is returned.
func (s *Session) decryptAndParse(pkt *ikev2.IKEPacket) ([]ikev2.Payload, error) {
	return s.decryptAndParseWithKeys(pkt, s.ikeKeys)
}

// errUnauthenticatedIKE marks an inbound IKE message that failed decryption or
// its integrity check. RFC 7296 2.21 drops such messages without acting on them.
var errUnauthenticatedIKE = errors.New("swu: unauthenticated IKE message")

// authenticatedSyntaxError is a payload decoding failure in a message that
// passed its integrity check. It is not dropped like errUnauthenticatedIKE.
type authenticatedSyntaxError struct{ err error }

func (e *authenticatedSyntaxError) Error() string {
	return "swu: authenticated IKE message has invalid syntax: " + e.err.Error()
}
func (e *authenticatedSyntaxError) Unwrap() error { return e.err }

// localIKEError is a failure of our own key or cipher setup while opening an
// inbound message. It is not the peer's fault and is not dropped.
type localIKEError struct{ err error }

func (e *localIKEError) Error() string { return e.err.Error() }
func (e *localIKEError) Unwrap() error { return e.err }

func decodeAuthenticatedPayloads(first ikev2.PayloadType, plain []byte) ([]ikev2.Payload, error) {
	payloads, err := ikev2.DecodePayloadChainWithFirst(first, plain)
	if err != nil {
		return nil, &authenticatedSyntaxError{err: err}
	}
	return payloads, nil
}

// decryptAndParseWithKeys adds the received header and payload types to any
// failure, so a message we cannot open can be told apart in the log.
func (s *Session) decryptAndParseWithKeys(pkt *ikev2.IKEPacket, keys *IKEKeys) ([]ikev2.Payload, error) {
	payloads, err := s.openIKE(pkt, keys)
	if err != nil {
		var syntax *authenticatedSyntaxError
		var local *localIKEError
		switch {
		case errors.As(err, &syntax):
			// The message passed its integrity check, so the peer is alive and
			// the syntax error stays a real failure.
			s.markInboundActivity()
		case errors.As(err, &local):
			// Our own keys or cipher setup failed; dropping would hide it.
		default:
			err = fmt.Errorf("%w: %w", errUnauthenticatedIKE, err)
		}
		if header := packetIKEHeader(pkt); header != nil {
			return nil, fmt.Errorf("%w: exchange %d flags 0x%02x msg_id %d spi %016x/%016x payloads %s",
				err, header.ExchangeType, header.Flags, header.MessageID,
				header.SPIi, header.SPIr, ikePayloadTypes(pkt.Payloads))
		}
		return nil, err
	}
	// Only an authenticated message proves the peer is alive (DPD).
	s.markInboundActivity()
	return payloads, nil
}

func (s *Session) openIKE(pkt *ikev2.IKEPacket, keys *IKEKeys) ([]ikev2.Payload, error) {
	if keys == nil {
		return nil, &localIKEError{errors.New("swu: no IKE SA keys")}
	}
	if len(pkt.Payloads) != 1 {
		return nil, errors.New("swu: expected a single Encrypted payload")
	}
	enc, ok := pkt.Payloads[0].(*ikev2.EncryptedPayloadSK)
	if !ok {
		return nil, errors.New("swu: payload is not Encrypted")
	}
	encKey, integKey := ikeProtectionKeysFor(keys, packetIKEHeader(pkt).Flags&ikeInitiatorFlag == 0)
	cipher, err := crypto.PrepareCipher(s.encrAlg, encKey)
	if err != nil {
		return nil, &localIKEError{fmt.Errorf("prepare cipher: %w", err)}
	}
	if s.aead {
		return s.decryptAEADIKE(pkt, enc, cipher)
	}
	integ := crypto.NewIntegrity(s.integAlg)
	if integ == nil {
		return nil, &localIKEError{errors.New("swu: no integrity algorithm")}
	}

	ivSize := cipher.IVSize()
	integSize := integ.OutputSize()
	if len(enc.Data) < ivSize+integSize {
		return nil, errors.New("swu: encrypted payload too short")
	}
	iv := enc.Data[:ivSize]
	ct := enc.Data[ivSize : len(enc.Data)-integSize]
	checksum := enc.Data[len(enc.Data)-integSize:]

	// Verify integrity.
	raw, err := pkt.Encode()
	if err != nil {
		return nil, &localIKEError{fmt.Errorf("encode protected IKE packet: %w", err)}
	}
	if !integ.Verify(integKey, raw[:len(raw)-integSize], checksum) {
		return nil, errors.New("swu: IKE message integrity check failed")
	}

	padded, err := cipher.Open(nil, ct, iv, nil)
	if err != nil {
		// The integrity check already passed, so this is the peer's message.
		return nil, &authenticatedSyntaxError{err: fmt.Errorf("decrypt IKE message: %w", err)}
	}
	plain, err := unpadIKEPlaintext(padded)
	if err != nil {
		return nil, &authenticatedSyntaxError{err: err}
	}
	return decodeAuthenticatedPayloads(enc.NextPayload, plain)
}
