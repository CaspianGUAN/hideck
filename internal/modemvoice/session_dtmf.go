package modemvoice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EC25/EC21 AT manual §12.4: VTS duration is measured in tenths of a second.
// Specify 300 ms per key without modifying the modem's shared VTD setting.
const dtmfDurationTenths = 3

const dtmfCommandTimeout = 5 * time.Second

func (s *Session) DTMF(ctx context.Context, id, digit string) (Update, error) {
	if len(digit) != 1 || !strings.Contains("0123456789*#", digit) {
		return Update{}, errors.New("modem voice: DTMF must be one of 0-9, * or #")
	}
	opCtx, release, err := s.enter(ctx)
	if err != nil {
		return Update{}, err
	}
	defer release()
	if !containsCallID(s.tracker.Calls(), id) {
		return Update{}, ErrCallChanged
	}
	update, err := s.refresh(opCtx)
	if err != nil {
		return update, err
	}
	calls := s.voiceControlCalls()
	if !containsCallID(calls, id) {
		return update, ErrCallChanged
	}
	if len(calls) != 1 || calls[0].Call.Mode != 0 || calls[0].Call.Multiparty || calls[0].Call.State != Active {
		return update, errors.New("modem voice: DTMF requires a single active voice call")
	}
	// Never retry: losing the response does not mean the remote IVR missed it.
	command := sessionCommand{text: fmt.Sprintf("AT+VTS=\"%s\",%d", digit, dtmfDurationTenths), timeout: dtmfCommandTimeout}
	return s.executeCommand(opCtx, command, update)
}
