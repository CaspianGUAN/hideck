package sipclient

import (
	"context"

	"github.com/yibaiba/hideck/internal/phone"
)

// MediaLeg is the PBX-facing plain RTP leg of one bridged call.
type MediaLeg interface {
	MediaID() string
	MediaLease() string
	OfferSDP() string
	AnswerOffer(offer string) (string, error)
	ApplyAnswer(answer string) error
}

// Phone is the subset of the phone service the SIP client drives. It is the
// same call model the browser phone uses, so device reservation, history and
// "first leg to answer wins" stay in one place.
type Phone interface {
	CreateMedia(owner, advertiseIP string, onDTMF func(string)) (MediaLeg, error)
	ReleaseMedia(MediaLeg)
	StartCall(phone.StartCallRequest) (phone.CallView, error)
	Answer(context.Context, phone.ControlRequest) (phone.CallView, error)
	Hangup(ctx context.Context, owner, callID, lease string) error
	DTMF(owner, callID, lease, digit string) error
	Subscribe(afterID uint64) ([]phone.Event, <-chan phone.Event, func())
}

// Devices resolves which modem carries PBX calls and prepares it for a call.
type Devices interface {
	DeviceIDs() []string
	PrepareCall(ctx context.Context, deviceID string) error
}

// PhoneService adapts *phone.Service to Phone.
type PhoneService struct{ *phone.Service }

func (p PhoneService) CreateMedia(owner, advertiseIP string, onDTMF func(string)) (MediaLeg, error) {
	media, err := p.Service.CreateExternalMedia(phone.ExternalMediaOptions{
		Owner: owner, AdvertiseIP: advertiseIP, OnDTMF: onDTMF,
	})
	if err != nil {
		return nil, err
	}
	return externalLeg{media}, nil
}

func (p PhoneService) ReleaseMedia(leg MediaLeg) {
	if media, ok := leg.(externalLeg); ok {
		p.Service.ReleaseExternalMedia(media.ExternalMedia)
	}
}

type externalLeg struct{ *phone.ExternalMedia }

func (leg externalLeg) MediaID() string    { return leg.ID }
func (leg externalLeg) MediaLease() string { return leg.Lease }
