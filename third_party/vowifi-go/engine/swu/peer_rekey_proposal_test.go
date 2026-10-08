package swu

import (
	"bytes"
	"testing"

	enginecrypto "github.com/iniwex5/vowifi-go/engine/crypto"
	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

func TestCreateChildSAProtocolAcceptsMultipleProposals(t *testing.T) {
	session := newProposalSession(t)
	decoy := unacceptableIKEProposal()
	match := buildIKEProposalsForSession(session)[0]
	match.SPI = []byte{1, 2, 3, 4, 5, 6, 7, 8}
	match.ProposalNum = 4
	match.AddTransform(ikev2.TransformTypeDH, ikev2.MODP_1024_bit)
	payloads := []ikev2.Payload{&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{decoy, match}}}

	protocol, err := createChildSAProtocol(payloads)
	if err != nil || protocol != ikev2.ProtoIKE {
		t.Fatalf("protocol = %d, err=%v", protocol, err)
	}
	if err := session.narrowPeerRekeySA(payloads, protocol); err != nil {
		t.Fatalf("narrowPeerRekeySA: %v", err)
	}
	sa := payloads[0].(*ikev2.EncryptedPayloadSA)
	if len(sa.Proposals) != 1 || sa.Proposals[0].ProposalNum != 1 || !bytes.Equal(sa.Proposals[0].SPI, match.SPI) {
		t.Fatalf("selected proposal = %#v", sa.Proposals[0])
	}
	if err := session.validateIKERekeyProposal(sa.Proposals[0]); err != nil {
		t.Fatalf("validateIKERekeyProposal: %v", err)
	}
}

func TestNarrowPeerRekeySARejectsUnknownProposal(t *testing.T) {
	session := newProposalSession(t)
	payloads := []ikev2.Payload{&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{unacceptableIKEProposal()}}}
	protocol, err := createChildSAProtocol(payloads)
	if err != nil {
		t.Fatalf("createChildSAProtocol: %v", err)
	}
	if err := session.narrowPeerRekeySA(payloads, protocol); err == nil {
		t.Fatal("unknown proposal was accepted")
	}
}

func TestCreateChildSAProtocolRejectsMixedProtocols(t *testing.T) {
	payloads := []ikev2.Payload{&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{
		unacceptableIKEProposal(),
		ikev2.NewProposal(2, ikev2.ProtoESP, []byte{1, 2, 3, 4}),
	}}}
	if _, err := createChildSAProtocol(payloads); err == nil {
		t.Fatal("mixed IKE and ESP proposals were accepted")
	}
}

func TestPeerChildRekeyDHGroupAdoptsIKEGroup(t *testing.T) {
	session := newProposalSession(t)
	peerDH, err := enginecrypto.NewDiffieHellman(session.dhGroup)
	if err != nil || peerDH.GenerateKey() != nil {
		t.Fatalf("peer DH: %v", err)
	}
	proposal := buildESPProposalsForSession(session, 0xb1c2d3e4)[0]
	proposal.AddTransform(ikev2.TransformTypeDH, ikev2.AlgorithmType(session.dhGroup))
	payloads := []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{proposal}},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.AlgorithmType(session.dhGroup), KEData: peerDH.PublicKeyBytes()},
	}
	if got := session.peerChildRekeyDHGroup(payloads); got != session.dhGroup {
		t.Fatalf("DH group = %d, want %d", got, session.dhGroup)
	}
}

func newProposalSession(t *testing.T) *Session {
	t.Helper()
	session := NewSession(&Config{})
	if session.initErr != nil {
		t.Fatalf("NewSession: %v", session.initErr)
	}
	return session
}

func unacceptableIKEProposal() *ikev2.Proposal {
	proposal := ikev2.NewProposal(1, ikev2.ProtoIKE, []byte{9, 8, 7, 6, 5, 4, 3, 2})
	proposal.AddTransform(ikev2.TransformTypeEncr, ikev2.ENCR_3DES)
	proposal.AddTransform(ikev2.TransformTypePRF, ikev2.PRF_HMAC_SHA2_512)
	proposal.AddTransform(ikev2.TransformTypeInteg, ikev2.AUTH_HMAC_SHA2_512_256)
	proposal.AddTransform(ikev2.TransformTypeDH, ikev2.MODP_1024_bit)
	return proposal
}
