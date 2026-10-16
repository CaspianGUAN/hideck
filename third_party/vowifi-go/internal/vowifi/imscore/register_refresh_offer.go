package imscore

import (
	"strings"

	"github.com/iniwex5/vowifi-go/internal/vowifi/policy"
)

// refreshOffersFreshClientSA reports whether a REGISTER refresh must offer a
// new port-c and SPIs. AIS answers a refresh whose Security-Client repeats
// the active SA with 500 "AKA IP+Port conflict"; an iPhone offers fresh
// values (TS 33.203 7.4) over the current SA and gets a plain 200 back.
func (s *Service) refreshOffersFreshClientSA() bool {
	return s != nil && s.cfg != nil && strings.HasPrefix(strings.TrimSpace(s.cfg.CarrierPresetID), "ais_th_")
}

// refreshSecurityOffer keeps the active SA for sending the refresh and only
// advertises a new port-c and SPI pair, with port-s unchanged, the way the
// iPhone's refresh does. The registrar answers without a new challenge, so
// the advertised values are never installed.
func refreshSecurityOffer(current *securityAgreement, template policy.IMSRegisterTemplate) (*securityAgreement, error) {
	if current == nil {
		return nil, nil
	}
	offer := current.client
	spiC, err := randomUint32(map[uint32]struct{}{offer.SPIC: {}, offer.SPIS: {}})
	if err != nil {
		return nil, err
	}
	spiS, err := randomUint32(map[uint32]struct{}{offer.SPIC: {}, offer.SPIS: {}, spiC: {}})
	if err != nil {
		return nil, err
	}
	port, err := randomUint32(nil)
	if err != nil {
		return nil, err
	}
	offer.SPIC, offer.SPIS = spiC, spiS
	offer.PortC = uint16(49152 + port%16384)
	if offer.PortC == current.client.PortC || offer.PortC == current.client.PortS {
		offer.PortC ^= 1
	}
	refreshed := *current
	refreshed.clientHeader = buildTemplateSecurityClient(offer, template)
	return &refreshed, nil
}
