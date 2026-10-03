package imscore

import "strings"

const (
	smsReadyReasonReady             = "IMS SMS receiver ready"
	smsReadyReasonNotRegistered     = "IMS registration is not ready"
	smsReadyReasonProfileNotReady   = "IMS registered identity is not ready"
	smsReadyReasonTransportNotReady = "IMS registered signaling transport is not ready"
	smsReadyReasonReceiverNotReady  = "IMS SMS receiver is not ready"
	smsReadyReasonSMSCNotConfigured = "IMS SMSC is not configured"
)

type smsHealthInput struct {
	readiness             SMSReadiness
	baseReceiverReady     bool
	protectedPushRequired bool
	portS                 portSSessionSnapshot
}

// SMSReadiness returns a consistent snapshot of the SMS prerequisites.
func (s *Service) SMSReadiness() SMSReadiness {
	if s == nil {
		return evaluateSMSReadiness(false, false, false, false, "")
	}
	s.mu.RLock()
	registered := s.regState == regRegistered
	profileReady := registered && s.regSession != nil &&
		strings.TrimSpace(s.regSession.publicID) != "" &&
		strings.TrimSpace(s.regSession.contactUser) != ""
	transportReady := registered && s.registeredSIPTransportReadyLocked()
	baseReceiverReady := s.smsReceiverReady
	receiverReady := baseReceiverReady
	protectedPushRequired := s.protectedSMSPushRequiredLocked()
	if protectedPushRequired {
		receiverReady = receiverReady && s.portSPushReady.Load()
	}
	smsc := ""
	if s.cfg != nil {
		smsc = s.cfg.SMSC
	}
	s.mu.RUnlock()
	readiness := evaluateSMSReadiness(registered, profileReady, transportReady, receiverReady, smsc)
	readiness.HealthReady = readiness.Ready || smsReceiverHealthReady(smsHealthInput{
		readiness: readiness, baseReceiverReady: baseReceiverReady,
		protectedPushRequired: protectedPushRequired, portS: s.capturePortSSession(),
	})
	return readiness
}

func smsReceiverHealthReady(input smsHealthInput) bool {
	return input.readiness.Registered && input.readiness.ProfileReady &&
		input.readiness.TransportReady && input.baseReceiverReady &&
		input.protectedPushRequired && !input.portS.connected &&
		input.portS.lastCloseKind == portSCloseEOF &&
		!input.portS.closedAt.Before(input.portS.openedAt)
}

func (s *Service) protectedSMSPushRequiredLocked() bool {
	return !s.externalTransport && !s.canAwaitOnDemandPortS() && !s.portSTimeoutDownlinkProven() &&
		!s.outboundFlowCarriesDownlinkLocked() &&
		s.regSession != nil &&
		s.regSession.security != nil &&
		strings.TrimSpace(s.regSession.security.verifyHeader) != ""
}

// SetOnSMSReadinessChanged installs the readiness observer and immediately
// publishes a snapshot so callers cannot miss startup transitions.
func (s *Service) SetOnSMSReadinessChanged(fn func(SMSReadiness)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onSMSReadiness = fn
	s.mu.Unlock()
	if fn != nil {
		fn(s.SMSReadiness())
	}
}

// SetOnSMSReady restores the original one-shot SMS readiness callback.
func (s *Service) SetOnSMSReady(fn func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onSMSReady = fn
	s.mu.Unlock()
	s.maybeNotifySMSReady(smsReadyReasonReady)
}

func (s *Service) setSMSReceiverReady(ready bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := s.smsReceiverReady != ready
	s.smsReceiverReady = ready
	callback := s.onSMSReadiness
	s.mu.Unlock()
	if ready {
		s.maybeNotifySMSReady(smsReadyReasonReady)
	}
	if changed && callback != nil {
		callback(s.SMSReadiness())
	}
}

func (s *Service) notifySMSReadiness() {
	if s == nil {
		return
	}
	s.mu.RLock()
	callback := s.onSMSReadiness
	s.mu.RUnlock()
	s.maybeNotifySMSReady(smsReadyReasonReady)
	if callback != nil {
		callback(s.SMSReadiness())
	}
}

func evaluateSMSReadiness(registered, profileReady, transportReady, receiverReady bool, smsc string) SMSReadiness {
	readiness := SMSReadiness{
		Registered:     registered,
		ProfileReady:   profileReady,
		TransportReady: transportReady,
		ReceiverReady:  receiverReady,
		SMSCPresent:    strings.TrimSpace(smsc) != "",
	}
	readiness.MOReady = readiness.Registered && readiness.ProfileReady &&
		readiness.TransportReady && readiness.SMSCPresent
	switch {
	case !readiness.Registered:
		readiness.Reason = smsReadyReasonNotRegistered
	case !readiness.ProfileReady:
		readiness.Reason = smsReadyReasonProfileNotReady
	case !readiness.TransportReady:
		readiness.Reason = smsReadyReasonTransportNotReady
	case !readiness.SMSCPresent && !readiness.ReceiverReady:
		readiness.Reason = smsReadyReasonSMSCNotConfigured
	case !readiness.ReceiverReady:
		readiness.Reason = smsReadyReasonReceiverNotReady
	default:
		readiness.Ready = true
		readiness.HealthReady = true
		if !readiness.SMSCPresent {
			readiness.Reason = smsReadyReasonSMSCNotConfigured
			break
		}
		readiness.MOReady = true
		readiness.Reason = smsReadyReasonReady
	}
	return readiness
}

// outboundFlowCarriesDownlinkLocked reports a registered RFC 5626 outbound
// flow on the protected REGISTER connection. The P-CSCF then sends MT
// requests over that flow and never opens port-s (T-Mobile US), so waiting
// for a port-s push would keep SMS "not ready" while delivery works.
func (s *Service) outboundFlowCarriesDownlinkLocked() bool {
	return s.outboundContactRegistered && s.sipOutbound &&
		s.registrationTCP != nil && s.registrationTCPProtected
}
