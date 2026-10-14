package volte

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

const nativeDTMFToneDuration = 150 * time.Millisecond

type dtmfTarget struct {
	deviceID  string
	call      nativeCall
	transport DTMFTransport
}

func (c *Controller) SendCallDTMF(deviceID, id, digit string) error {
	if !validNativeDTMF(digit) {
		return errors.New("volte: DTMF must be one of 0-9, * or #")
	}
	deviceID = strings.TrimSpace(deviceID)
	// Drain the complete tone before retiring or handing off its device session.
	return c.withDevice(deviceID, func() error {
		call, ok := c.lookup(deviceID, id)
		if !ok {
			return fmt.Errorf("volte: call %s not found", id)
		}
		transport, err := c.host.BindVoiceDTMF(deviceID)
		if err != nil {
			return fmt.Errorf("volte: bind DTMF transport: %w", err)
		}
		target := dtmfTarget{deviceID: deviceID, call: call, transport: transport}
		return c.sendBoundDTMF(target, digit)
	})
}

func (c *Controller) sendBoundDTMF(target dtmfTarget, digit string) error {
	if target.call.Mode == qmi.VoiceCallModeCDMA {
		return c.sendBurstDTMF(target, digit)
	}
	started, err := c.sendContinuousDTMF(target, digit[0])
	if err == nil {
		return nil
	}
	if started || !unknownDTMFMode(target.call.Mode) || !continuousDTMFUnsupported(err) {
		return err
	}
	if burstErr := c.sendBurstDTMF(target, digit); burstErr != nil {
		return fmt.Errorf("volte: continuous DTMF unsupported and burst fallback failed: %w",
			errors.Join(err, burstErr))
	}
	return nil
}

func (c *Controller) validateDTMFCall(target dtmfTarget) error {
	voice := c.sessionVoice(target.deviceID)
	if voice == nil {
		return fmt.Errorf("volte: DTMF session for %s is no longer current", target.deviceID)
	}
	call, ok := voice.getByQMI(target.call.QMI)
	if !ok || call.ID != target.call.ID || call.State == "completed" {
		return fmt.Errorf("volte: DTMF call %s is no longer current", target.call.ID)
	}
	return nil
}

func (c *Controller) sendContinuousDTMF(target dtmfTarget, digit uint8) (bool, error) {
	if err := c.validateDTMFCall(target); err != nil {
		return false, err
	}
	ctx := context.Background()
	if _, err := target.transport.VOICEStartContinuousDTMF(ctx, target.call.QMI, digit); err != nil {
		return false, fmt.Errorf("volte: start continuous DTMF: %w", err)
	}
	time.Sleep(nativeDTMFToneDuration)
	// A remote hangup can retire the call while its device remains installed.
	if err := c.validateDTMFCall(target); err != nil {
		return true, err
	}
	if _, err := target.transport.VOICEStopContinuousDTMF(ctx, target.call.QMI); err != nil {
		return true, fmt.Errorf("volte: stop continuous DTMF: %w", err)
	}
	return true, nil
}

func (c *Controller) sendBurstDTMF(target dtmfTarget, digit string) error {
	if err := c.validateDTMFCall(target); err != nil {
		return err
	}
	if _, err := target.transport.VOICEBurstDTMF(context.Background(), target.call.QMI, digit); err != nil {
		return fmt.Errorf("volte: burst DTMF: %w", err)
	}
	return nil
}

func validNativeDTMF(digit string) bool {
	return len(digit) == 1 && strings.Contains("0123456789*#", digit)
}

func unknownDTMFMode(mode qmi.VoiceCallMode) bool {
	return mode == qmi.VoiceCallModeNoService || mode == qmi.VoiceCallModeUnknown || mode > qmi.VoiceCallModeWLAN
}

func continuousDTMFUnsupported(err error) bool {
	qmiErr := qmi.GetQMIError(err)
	if qmiErr == nil {
		return false
	}
	switch qmiErr.ErrorCode {
	case qmi.QMIErrOpNetworkUnsupported, qmi.QMIErrOpDeviceUnsupported,
		qmi.QMIErrInvalidQmiCmd, qmi.QMIErrNotSupported:
		return true
	default:
		return false
	}
}
