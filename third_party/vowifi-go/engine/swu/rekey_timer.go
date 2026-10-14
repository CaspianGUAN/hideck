package swu

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/iniwex5/vowifi-go/engine/ikev2"
	"go.uber.org/zap"
)

const (
	// IR.51 4.4: 18-hour fallback when the ePDG omits AUTH_LIFETIME.
	defaultIKERekeyInterval   = 64800 * time.Second
	defaultChildRekeyInterval = 1800 * time.Second
	childRekeyStartOffset     = 30 * time.Second
	rekeyMaxFailures          = 2
	rekeyRetryInterval        = 60 * time.Second
)

type rekeyTimerSpec struct {
	name          string
	interval      time.Duration
	reset         <-chan struct{}
	target        **time.Timer
	action        func() error
	immediateFail func(error) bool
	// declined keeps the current SA in service and asks again next interval.
	declined func(error) bool
	// refused reports a rejection that leaves the current SA usable once
	// retries are exhausted; the timer stops instead of failing the session.
	refused       func(error) bool
	retryInterval time.Duration
}

func (s *Session) rekeyIntervals() (time.Duration, time.Duration) {
	s.mu.RLock()
	lifetime := s.authLifetime
	s.mu.RUnlock()
	var ikeInterval, childInterval time.Duration
	if s.cfg != nil {
		ikeInterval = s.cfg.RekeyIKESeconds
		childInterval = s.cfg.RekeyChildSeconds
	}
	if ikeInterval <= 0 {
		ikeInterval = defaultIKERekeyInterval
		if lifetime > 0 {
			ikeInterval = time.Duration(lifetime) * time.Second * 4 / 5
		}
	}
	if childInterval > 0 {
		return ikeInterval, childInterval
	}
	childInterval = defaultChildRekeyInterval
	if lifetime > 0 {
		childInterval = time.Duration(lifetime) * time.Second * 7 / 8
	}
	return ikeInterval, childInterval + childRekeyStartOffset
}

// startIKESARekeyTimer restores the original interval-taking timer API.
func (s *Session) startIKESARekeyTimer(interval time.Duration) {
	reset := make(chan struct{}, 1)
	s.mu.Lock()
	s.rekeyResetCh = reset
	s.mu.Unlock()
	s.startRekeyTimer(rekeyTimerSpec{
		name: "IKE SA", interval: interval,
		reset: reset, target: &s.ikeRekeyTimer, action: s.RekeyIKESA,
	})
}

// startChildSARekeyTimer restores the original interval-taking timer API.
func (s *Session) startChildSARekeyTimer(interval time.Duration) {
	reset := make(chan struct{}, 1)
	s.mu.Lock()
	s.childRekeyResetCh = reset
	s.mu.Unlock()
	s.startRekeyTimer(rekeyTimerSpec{
		name: "CHILD_SA", interval: interval,
		reset: reset, target: &s.childRekeyTimer, action: s.RekeyChildSA,
		immediateFail: isChildSANotFoundError, declined: s.childRekeyDeclined(),
		// Some ePDGs (T-Mobile US) refuse every UE-initiated CHILD_SA rekey
		// with NO_PROPOSAL_CHOSEN, with or without PFS, while the SA stays
		// valid. Keep it and leave rekeying to the ePDG.
		refused: isNoProposalChosenError,
	})
}

// childRekeyDeclined treats NO_ADDITIONAL_SAS as a decline only where the
// carrier preset opts in; elsewhere it stays a rekey failure.
func (s *Session) childRekeyDeclined() func(error) bool {
	if s.cfg != nil && s.cfg.KeepChildSAOnRekeyDecline {
		return isNoAdditionalSAsError
	}
	return nil
}

// isNoAdditionalSAsError reports a peer that refuses a UE-initiated CHILD_SA
// rekey. RFC 7296 2.8 keeps the old SA valid after a failed rekey.
func isNoAdditionalSAsError(err error) bool {
	var rejection *createChildSARejectError
	return errors.As(err, &rejection) && rejection.NotifyType == ikev2.NO_ADDITIONAL_SAS
}

func isChildSANotFoundError(err error) bool {
	var rejection *createChildSARejectError
	return errors.As(err, &rejection) && rejection.NotifyType == ikev2.CHILD_SA_NOT_FOUND
}

func (s *Session) startRekeyTimer(spec rekeyTimerSpec) {
	if spec.interval <= 0 || spec.action == nil || spec.target == nil || s.ctx.Err() != nil {
		return
	}
	timer := time.NewTimer(rekeyDelay(spec.interval))
	s.timersMu.Lock()
	if previous := *spec.target; previous != nil {
		previous.Stop()
	}
	*spec.target = timer
	s.timersMu.Unlock()
	s.rekeyTimerWG.Add(1)
	go func() {
		defer s.rekeyTimerWG.Done()
		s.runRekeyTimer(timer, spec)
	}()
}

func (s *Session) runRekeyTimer(timer *time.Timer, spec rekeyTimerSpec) {
	failures := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-spec.reset:
			failures = 0
			if !s.resetRekeyTimer(timer, rekeyDelay(spec.interval)) {
				return
			}
		case <-timer.C:
			err := spec.action()
			if err == nil {
				failures = 0
				if !s.resetRekeyTimer(timer, rekeyDelay(spec.interval)) {
					return
				}
				continue
			}
			if spec.declined != nil && spec.declined(err) {
				s.Logger.Warn("peer declined "+spec.name+" rekey; keeping current SA", zap.Error(err))
				failures = 0
				if !s.resetRekeyTimer(timer, rekeyDelay(spec.interval)) {
					return
				}
				continue
			}
			failures++
			s.Logger.Warn(spec.name+" rekey attempt failed", zap.Int("attempt", failures),
				zap.Int("max_attempts", rekeyMaxFailures), zap.Error(err))
			if failures >= rekeyMaxFailures && spec.refused != nil && spec.refused(err) {
				s.Logger.Warn("ePDG declined UE-initiated rekey; keeping the current SA and leaving rekey to the ePDG",
					zap.String("sa", spec.name), zap.Error(err))
				return
			}
			if failures >= rekeyMaxFailures || spec.immediateFail != nil && spec.immediateFail(err) {
				s.failEstablishedControl(fmt.Errorf("swu: %s rekey failed: %w", spec.name, err))
				return
			}
			retryInterval := spec.retryInterval
			if retryInterval <= 0 {
				retryInterval = rekeyRetryInterval
			}
			if !s.resetRekeyTimer(timer, retryInterval) {
				return
			}
		}
	}
}

func (s *Session) resetRekeyTimer(timer *time.Timer, delay time.Duration) bool {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if s.ctx.Err() != nil {
		return false
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
	return true
}

// rekeyDelay returns the next rekey wait in the IR.51 4.4 band of 75%–125%
// of the mean interval. Absolute one-sided jitter is not used.
func rekeyDelay(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	minimum := interval * 3 / 4
	maximum := interval * 5 / 4
	span := maximum - minimum
	if span <= 0 {
		return interval
	}
	return minimum + time.Duration(rand.Int63n(int64(span)+1))
}
