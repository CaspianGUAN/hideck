package swu

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
)

const (
	deviceIdentityIMEI       = 1
	deviceIdentityIMEISV     = 2
	deviceIdentityHeaderSize = 3
)

// TS 24.302 8.2.9.2: uint16 length includes the identity type and value.
func hasDeviceIdentityRequest(payloads []ikev2.Payload) (bool, error) {
	requested := false
	for _, payload := range payloads {
		notify, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if !ok || notify.NotifyType != ikev2.DEVICE_IDENTITY_3GPP {
			continue
		}
		data := notify.NotifyData
		if len(data) < deviceIdentityHeaderSize || int(binary.BigEndian.Uint16(data[:2])) != len(data)-2 {
			return false, errors.New("swu: malformed DEVICE_IDENTITY notification length")
		}
		knownType := data[2] == deviceIdentityIMEI || data[2] == deviceIdentityIMEISV
		requested = requested || knownType && len(data) == deviceIdentityHeaderSize
	}
	return requested, nil
}

func (s *Session) deviceIdentityValue() ([]byte, error) {
	if s.cfg == nil {
		return nil, nil
	}
	imei := strings.TrimSpace(s.cfg.DeviceIdentityIMEI)
	if imei == "" && s.cfg.EnableDeviceIdentitySpoof {
		imsi, err := requiredConfiguredIMSI(s.cfg)
		if err != nil {
			return nil, err
		}
		imei = spoofAppleIMEI(imsi)
	}
	if imei == "" {
		return nil, nil
	}
	return encodeIMEITBCD(imei)
}

func (s *Session) deviceIdentityReply() (*ikev2.EncryptedPayloadNotify, error) {
	value, err := s.deviceIdentityValue()
	if err != nil || len(value) == 0 {
		return nil, err
	}
	data := make([]byte, deviceIdentityHeaderSize+len(value))
	binary.BigEndian.PutUint16(data[:2], uint16(1+len(value)))
	data[2] = deviceIdentityIMEI
	copy(data[deviceIdentityHeaderSize:], value)
	return &ikev2.EncryptedPayloadNotify{
		NotifyType: ikev2.DEVICE_IDENTITY_3GPP, NotifyData: data,
	}, nil
}

func (s *Session) pendingDeviceIdentityReply() (*ikev2.EncryptedPayloadNotify, error) {
	// Withholding the initial identity must not suppress an authenticated request.
	if !s.deviceIdentityRequested || !(s.responderAuthenticated || s.deviceIdentityEAPVerified) {
		return nil, nil
	}
	return s.deviceIdentityReply()
}

func (s *Session) informationalDeviceIdentityReply(payloads []ikev2.Payload) ([]ikev2.Payload, error) {
	requested, err := hasDeviceIdentityRequest(payloads)
	if err != nil || !requested {
		return nil, err
	}
	reply, err := s.deviceIdentityReply()
	if err != nil || reply == nil {
		return nil, err
	}
	return []ikev2.Payload{reply}, nil
}
