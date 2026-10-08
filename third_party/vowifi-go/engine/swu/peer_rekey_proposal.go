package swu

import (
	"encoding/binary"
	"errors"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"github.com/iniwex5/vowifi-go/engine/logger"
	"go.uber.org/zap"
)

// narrowPeerRekeySA reduces a peer CREATE_CHILD_SA offer to the one proposal
// that continues the algorithms already in use. ePDGs send the whole proposal
// list, or several transforms of one type, at the 24-hour rekey. Responders
// answer with exactly one proposal.
func (s *Session) narrowPeerRekeySA(payloads []ikev2.Payload, protocol ikev2.ProtocolID) error {
	sa := createChildSAPayload(payloads)
	if sa == nil {
		return errors.New("swu: CREATE_CHILD_SA request missing a single SA proposal")
	}
	offered := len(sa.Proposals)
	var chosen *ikev2.Proposal
	switch protocol {
	case ikev2.ProtoIKE:
		chosen = s.selectPeerIKERekeyProposal(sa.Proposals)
	case ikev2.ProtoESP:
		chosen = s.selectPeerESPRekeyProposal(sa.Proposals, payloads)
	default:
		return errors.New("swu: CREATE_CHILD_SA request missing a single SA proposal")
	}
	if chosen == nil {
		return errors.New("swu: CREATE_CHILD_SA request has no acceptable SA proposal")
	}
	if offered != 1 {
		logger.Info("selected peer CREATE_CHILD_SA proposal",
			zap.Int("offered", offered), zap.Uint8("protocol", uint8(protocol)))
	}
	sa.Proposals = []*ikev2.Proposal{chosen}
	return nil
}

func (s *Session) selectPeerIKERekeyProposal(proposals []*ikev2.Proposal) *ikev2.Proposal {
	for _, proposal := range proposals {
		if proposal == nil || proposal.ProtocolID != ikev2.ProtoIKE || len(proposal.SPI) != 8 ||
			binary.BigEndian.Uint64(proposal.SPI) == 0 {
			continue
		}
		encryption := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeEncr, ikev2.AlgorithmType(s.encrAlg), s.encKeyBits)
		prf := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypePRF, ikev2.AlgorithmType(s.prfAlg), 0)
		dh := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeDH, ikev2.AlgorithmType(s.dhGroup), 0)
		if encryption == nil || prf == nil || dh == nil {
			continue
		}
		transforms := []*ikev2.Transform{cloneTransform(encryption), cloneTransform(prf)}
		if !s.aead {
			integrity := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeInteg, ikev2.AlgorithmType(s.integAlg), 0)
			if integrity == nil {
				continue
			}
			transforms = append(transforms, cloneTransform(integrity))
		}
		transforms = append(transforms, cloneTransform(dh))
		return narrowedRekeyProposal(ikev2.ProtoIKE, proposal.SPI, transforms)
	}
	return nil
}

func (s *Session) selectPeerESPRekeyProposal(proposals []*ikev2.Proposal, payloads []ikev2.Payload) *ikev2.Proposal {
	currentDH := s.currentChildDHGroup()
	keGroup, hasKE := childRekeyKEGroup(payloads)
	for _, proposal := range proposals {
		if proposal == nil || proposal.ProtocolID != ikev2.ProtoESP || len(proposal.SPI) != 4 ||
			binary.BigEndian.Uint32(proposal.SPI) == 0 {
			continue
		}
		encryption := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeEncr, ikev2.AlgorithmType(s.espCipher), s.espEncKeyBits)
		if encryption == nil {
			continue
		}
		transforms := []*ikev2.Transform{cloneTransform(encryption)}
		if s.espInteg != 0 {
			integrity := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeInteg, ikev2.AlgorithmType(s.espInteg), 0)
			if integrity == nil {
				continue
			}
			transforms = append(transforms, cloneTransform(integrity))
		}
		dhGroup, ok := s.acceptablePeerESPDH(proposal, currentDH, hasKE, keGroup)
		if !ok {
			continue
		}
		if dhGroup != 0 {
			dh := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeDH, ikev2.AlgorithmType(dhGroup), 0)
			transforms = append(transforms, cloneTransform(dh))
		}
		esnID := ikev2.AlgorithmType(0)
		if s.espESN {
			esnID = 1
		}
		esn := matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeESN, esnID, 0)
		if esn == nil {
			continue
		}
		transforms = append(transforms, cloneTransform(esn))
		return narrowedRekeyProposal(ikev2.ProtoESP, proposal.SPI, transforms)
	}
	return nil
}

// acceptablePeerESPDH allows a peer Child SA rekey to adopt the IKE DH group
// when the Child SA was created without PFS. That is the same fallback the
// local rekey uses after NO_PROPOSAL_CHOSEN.
func (s *Session) acceptablePeerESPDH(proposal *ikev2.Proposal, current uint16, hasKE bool, keGroup uint16) (uint16, bool) {
	if hasKE {
		if keGroup == 0 || matchingRekeyTransform(proposal.Transforms, ikev2.TransformTypeDH, ikev2.AlgorithmType(keGroup), 0) == nil {
			return 0, false
		}
		if current == keGroup || (current == 0 && keGroup == s.dhGroup) {
			return keGroup, true
		}
		return 0, false
	}
	if current != 0 || proposalOffersDH(proposal) {
		return 0, false
	}
	return 0, true
}

func (s *Session) peerChildRekeyDHGroup(payloads []ikev2.Payload) uint16 {
	current := s.currentChildDHGroup()
	group := firstProposalDH(payloads)
	keGroup, hasKE := childRekeyKEGroup(payloads)
	if !hasKE || group == 0 || group != keGroup {
		return current
	}
	if group == current || (current == 0 && group == s.dhGroup) {
		return group
	}
	return current
}

func childRekeyKEGroup(payloads []ikev2.Payload) (uint16, bool) {
	var group uint16
	found := false
	for _, payload := range payloads {
		if payload == nil || payload.Type() != ikev2.PayloadKE {
			continue
		}
		if found {
			return 0, false
		}
		parsed, key, err := parseKEPayload(payload)
		if err != nil || len(key) == 0 {
			return 0, false
		}
		group, found = parsed, true
	}
	return group, found
}

func firstProposalDH(payloads []ikev2.Payload) uint16 {
	sa := createChildSAPayload(payloads)
	if sa == nil || len(sa.Proposals) == 0 {
		return 0
	}
	return proposalDHGroup(sa.Proposals[0])
}

func proposalOffersDH(proposal *ikev2.Proposal) bool {
	for _, transform := range proposal.Transforms {
		normalizeRekeyTransform(transform)
		if transform != nil && transform.Type == ikev2.TransformTypeDH {
			return true
		}
	}
	return false
}

func matchingRekeyTransform(
	transforms []*ikev2.Transform,
	kind ikev2.TransformType,
	id ikev2.AlgorithmType,
	keyBits uint16,
) *ikev2.Transform {
	for _, transform := range transforms {
		normalizeRekeyTransform(transform)
		if transform == nil || transform.Type != kind || transform.ID != id {
			continue
		}
		if kind == ikev2.TransformTypeEncr {
			if err := validateEncryptionKeyLength(transform, keyBits); err != nil {
				continue
			}
		} else if len(transform.Attributes) != 0 {
			continue
		}
		return transform
	}
	return nil
}

func normalizeRekeyTransform(transform *ikev2.Transform) {
	if transform == nil {
		return
	}
	if transform.Type == 0 {
		transform.Type = ikev2.TransformType(transform.TransformType)
	}
	if transform.ID == 0 {
		transform.ID = ikev2.AlgorithmType(transform.TransformID)
	}
}

func narrowedRekeyProposal(protocol ikev2.ProtocolID, spi []byte, transforms []*ikev2.Transform) *ikev2.Proposal {
	return &ikev2.Proposal{
		LastProposal: true, ProposalNum: 1, ProtocolID: protocol,
		SPI: append([]byte(nil), spi...), SPISize: uint8(len(spi)),
		Transforms: transforms, NumTransforms: uint8(len(transforms)),
	}
}
