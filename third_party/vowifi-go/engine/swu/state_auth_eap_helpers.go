package swu

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"

	engineeap "github.com/iniwex5/vowifi-go/engine/eap"
	"github.com/iniwex5/vowifi-go/engine/swu/eapaka"
)

func (s *Session) resolveATCheckcodeValue(
	mode string,
	eapType uint8,
	hasCheckcode bool,
	serverValue []byte,
	shouldEcho bool,
) []byte {
	if !hasCheckcode {
		return nil
	}
	switch normalizeAKAChallengeMode(mode) {
	case "checkcode":
		if shouldEcho {
			return append([]byte(nil), serverValue...)
		}
	case "recompute":
		hashType := "sha1"
		if eapType == eapaka.TypeAKAPrime {
			hashType = "sha256"
		}
		checkcode := s.calcAKACheckcodeWithPending(hashType, nil)
		if len(checkcode) > 0 {
			return append([]byte{0, 0}, checkcode...)
		}
	}
	return nil
}

func (s *Session) appendAKAChallengeMetaAttrs(
	attrs []eapaka.Attribute,
	request eapaka.Packet,
) []eapaka.Attribute {
	mode := normalizeAKAChallengeMode(s.cfg.AKAChallengeMode)
	checkcode, hasCheckcode := eapaka.FindAttribute(request.Attributes, eapaka.AttributeCheckcode)
	_, hasResultInd := eapaka.FindAttribute(request.Attributes, eapaka.AttributeResultInd)
	includeResult := mode == "checkcode" || mode == "recompute"
	includeCheckcode := includeResult || ((mode == "minimal" || mode == "empty") && request.Type == eapaka.TypeAKA)

	filtered := make([]eapaka.Attribute, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Type != eapaka.AttributeResultInd && attr.Type != eapaka.AttributeCheckcode && attr.Type != eapaka.AttributeMAC {
			filtered = append(filtered, attr)
		}
	}
	if includeResult && hasResultInd {
		filtered = append(filtered, eapaka.ResultIndAttribute())
	}
	if includeCheckcode && hasCheckcode {
		// RFC 4187 10.13: a challenge carrying AT_CHECKCODE is answered with
		// AT_CHECKCODE. Mode "empty" sends an empty value. Mode "omit"
		// leaves the attribute out, which is VoCat's AIS challenge.
		value, err := checkcode.CheckcodeValue()
		if err == nil {
			filtered = append(filtered, eapaka.CheckcodeAttribute(s.challengeCheckcode(mode, request.Type, value)))
		}
	}
	return append(filtered, eapaka.MACAttribute(nil))
}

func (s *Session) applyAKAChallengeMode(
	response, request eapaka.Packet,
	kAut []byte,
) (eapaka.Packet, []byte, error) {
	response.Attributes = s.appendAKAChallengeMetaAttrs(response.Attributes, request)
	attrs, err := eapaka.MarshalAttributes(response.Attributes)
	if err != nil {
		return eapaka.Packet{}, nil, err
	}
	raw, err := buildSignedEAPResponse(
		response.Type, response.Identifier, response.Subtype, attrs, kAut,
	)
	if err != nil {
		return eapaka.Packet{}, nil, err
	}
	proof, err := buildEAPMACSelfCheckProof(raw, kAut)
	if err != nil {
		return eapaka.Packet{}, nil, err
	}
	if !proof.Match {
		return eapaka.Packet{}, nil, errors.New("swu: generated EAP response failed MAC self-check")
	}
	parsed, err := eapaka.ParsePacket(raw)
	if err != nil {
		return eapaka.Packet{}, nil, err
	}
	return parsed, raw, nil
}

func modeForCheckcode(mode string) string {
	if mode == "minimal" {
		return "checkcode"
	}
	return mode
}

func (s *Session) calcAKACheckcodeWithPending(hashType string, pending []byte) []byte {
	var digest hash.Hash = sha1.New()
	if hashType == "sha256" {
		digest = sha256.New()
	}
	for _, packet := range s.eapTranscript {
		_, _ = digest.Write(packet)
	}
	if len(pending) > 0 {
		_, _ = digest.Write(pending)
	}
	return digest.Sum(nil)
}

func buildSignedEAPResponse(eapType, identifier, subtype uint8, attrs, kAut []byte) ([]byte, error) {
	packet := (&engineeap.EAPPacket{
		Code: engineeap.CodeResponse, Identifier: identifier,
		Type: eapType, Subtype: subtype, Data: append([]byte(nil), attrs...),
	}).Encode()
	macOffset, ok := findEAPAttrOffset(attrs, engineeap.AT_MAC)
	if !ok || 8+macOffset+20 > len(packet) {
		return nil, errors.New("swu: EAP response has no valid AT_MAC")
	}
	mac, err := calculateEAPMAC(eapType, kAut, packet)
	if err != nil {
		return nil, err
	}
	copy(packet[8+macOffset+4:], mac)
	return packet, nil
}

type eapMACSelfCheckProof struct {
	Match      bool
	Received   []byte
	Calculated []byte
	KeyDigest  string
	PacketHash string
}

func buildEAPMACSelfCheckProof(eapRaw, kAut []byte) (*eapMACSelfCheckProof, error) {
	packet, err := eapaka.ParsePacket(eapRaw)
	if err != nil {
		return nil, err
	}
	attr, ok := eapaka.FindAttribute(packet.Attributes, eapaka.AttributeMAC)
	if !ok {
		return nil, errors.New("swu: EAP response is missing AT_MAC")
	}
	received, err := attr.FixedValue(16)
	if err != nil {
		return nil, err
	}
	calculated, err := calculateEAPMAC(packet.Type, kAut, eapRaw)
	if err != nil {
		return nil, err
	}
	return &eapMACSelfCheckProof{
		Match: hmac.Equal(received, calculated), Received: received,
		Calculated: calculated, KeyDigest: eapAttrDigest(kAut), PacketHash: eapAttrDigest(eapRaw),
	}, nil
}

func calculateEAPMAC(eapType uint8, kAut, packet []byte) ([]byte, error) {
	zeroed, err := zeroEAPMAC(packet)
	if err != nil {
		return nil, err
	}
	var digest func() hash.Hash
	switch eapType {
	case eapaka.TypeAKA:
		digest = sha1.New
	case eapaka.TypeAKAPrime:
		digest = sha256.New
	default:
		return nil, unexpectedEAPMethodError("AKA or AKA'", eapType)
	}
	mac := hmac.New(digest, kAut)
	_, _ = mac.Write(zeroed)
	return mac.Sum(nil)[:16], nil
}

func zeroEAPMAC(packet []byte) ([]byte, error) {
	parsed, err := eapaka.ParsePacket(packet)
	if err != nil {
		return nil, err
	}
	attrs, err := eapaka.MarshalAttributes(parsed.Attributes)
	if err != nil {
		return nil, err
	}
	offset, ok := findEAPAttrOffset(attrs, eapaka.AttributeMAC)
	if !ok || 8+offset+20 > len(packet) {
		return nil, errors.New("swu: invalid AT_MAC offset")
	}
	zeroed := append([]byte(nil), packet...)
	clear(zeroed[8+offset+4 : 8+offset+20])
	return zeroed, nil
}

func findEAPAttrOffset(data []byte, attributeType uint8) (int, bool) {
	for offset := 0; offset+2 <= len(data); {
		length := int(data[offset+1]) * 4
		if length == 0 || offset+length > len(data) {
			return 0, false
		}
		if data[offset] == attributeType {
			return offset, true
		}
		offset += length
	}
	return 0, false
}

func eapAttrDigest(value []byte) string {
	if len(value) == 0 {
		return "len=0"
	}
	digest := sha256.Sum256(value)
	return fmt.Sprintf("len=%d sha256=%x", len(value), digest[:8])
}

// challengeCheckcode returns the AT_CHECKCODE value (without the reserved
// bytes) for a challenge response; nil encodes the empty checkcode.
func (s *Session) challengeCheckcode(mode string, eapType uint8, serverValue []byte) []byte {
	// SIMHub's accepted AIS response is 44 bytes for an 8-byte RES:
	// AT_RES + empty AT_CHECKCODE + AT_MAC. Echoing a non-empty server
	// checkcode makes that ePDG return a bare EAP-Failure.
	if normalizeAKAChallengeMode(mode) == "empty" {
		return nil
	}
	checkcodeMode := modeForCheckcode(mode)
	if checkcodeMode == "recompute" && len(s.eapTranscript) == 0 {
		return nil
	}
	resolved := s.resolveATCheckcodeValue(checkcodeMode, eapType, true, serverValue, len(serverValue) > 0)
	if checkcodeMode == "recompute" {
		// recompute prefixes the two reserved bytes.
		if len(resolved) > 2 {
			return resolved[2:]
		}
		return nil
	}
	return resolved
}
