package runtimehost

import (
	"context"
	"strings"
	"sync"

	"github.com/iniwex5/vowifi-go/internal/vowifi/runtimecore"
)

type instanceObserver struct {
	inst               *Instance
	deviceID           string
	ready              chan struct{}
	readyOnce          sync.Once
	smsReadinessMu     sync.Mutex
	latestSMSReadiness SMSReadiness
	hasSMSReadiness    bool
}

func (observer *instanceObserver) OnRuntimeEvent(
	ctx context.Context,
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
) {
	if observer == nil || observer.inst == nil {
		return
	}
	kind := recoveredEventKind(event.Kind)
	state := observer.applyRuntimeEventState(kind, event)
	observer.inst.publish(ctx, Event{
		Kind: kind, DeviceID: state.DeviceID, TraceID: event.TraceID,
		Reason: event.Reason, Attempt: event.Attempt, RetryDelay: event.RetryDelay,
		RedirectEPDG: event.RedirectEPDG, State: state,
		Type: kind, Detail: event.Reason, Session: observer.inst,
	})
}

func (observer *instanceObserver) applyRuntimeEventState(
	kind string,
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
) State {
	observer.applyEventHandles(kind, event)
	return observer.inst.mutateState(func(state *State) {
		observer.applyEventMetadata(event, state)
		observer.applyEvent(kind, event, state)
		state.LastEvent = kind
		if kind == "ims_registered" {
			observer.applyLatestSMSReadiness(state)
		}
	})
}

func (observer *instanceObserver) applyEventMetadata(
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
	state *State,
) {
	if state.DeviceID == "" {
		state.DeviceID = observer.deviceID
	}
	if strings.TrimSpace(event.DeviceID) != "" {
		state.DeviceID = strings.TrimSpace(event.DeviceID)
	}
	if strings.TrimSpace(event.RedirectEPDG) != "" {
		state.LastRedirectEPDG = strings.TrimSpace(event.RedirectEPDG)
	}
}

func (observer *instanceObserver) updateSMSReadiness(readiness SMSReadiness) {
	observer.smsReadinessMu.Lock()
	observer.latestSMSReadiness = readiness
	observer.hasSMSReadiness = true
	observer.smsReadinessMu.Unlock()
	observer.inst.updateSMSReadiness(readiness)
}

func (observer *instanceObserver) applyLatestSMSReadiness(state *State) {
	observer.smsReadinessMu.Lock()
	readiness := observer.latestSMSReadiness
	ok := observer.hasSMSReadiness
	observer.smsReadinessMu.Unlock()
	if ok {
		applySMSReadiness(state, readiness)
	}
	// Registration can be published before its new readiness snapshot. Do not
	// turn a pre-registration snapshot into an observed SMS outage.
	if !ok || !readiness.Registered {
		state.SMSRXPending = true
		state.SMSReadyReason = "IMS SMS readiness has not been reported"
	}
}

func (observer *instanceObserver) applyEventHandles(
	kind string,
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
) {
	switch kind {
	case "ipsec_up":
		observer.installSession(event)
	case "ims_registered":
		observer.installService(event)
	case "retrying", "error", "terminal_error", "stopped":
		observer.clearRuntimeHandles()
	}
}

func (observer *instanceObserver) applyEvent(
	kind string,
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
	state *State,
) {
	switch kind {
	case "prepared":
		state.Phase = PhaseSIMReady
		state.SIMReady = true
		state.AccessReady = false
	case "connecting":
		state.Phase = PhaseAccessReady
		state.SIMReady = true
		state.AccessReady = true
	case "ipsec_up":
		markIMSUnavailable(state, "registering")
		state.Phase = readyPhase(*state)
		state.SessionState = "established"
		state.TunnelReady = event.Snapshot.Established || event.Handle != nil
		state.DataPlaneUp = state.TunnelReady
		clearRuntimeError(state)
		if observer.ready != nil {
			observer.readyOnce.Do(func() { close(observer.ready) })
		}
	case "ims_registered":
		state.Phase = "ims_ready"
		state.IMSState = "registered"
		state.IMSReady = true
		state.RegStatus = 1
		state.RegStatusText = "registered"
		clearRuntimeError(state)
	case "sms_ready":
		state.Phase = "sms_ready"
		state.SMSReady = true
		state.SMSRXPending = false
		state.SMSMOReady = true
		state.SMSHealthReady = true
		clearRecoveredFailure(state)
	case "interrupted":
		state.Phase = "interrupted"
		state.SessionState = "interrupted"
		state.TunnelReady = false
		state.DataPlaneUp = false
		markIMSUnavailable(state, "restarting")
		state.LastReason = strings.TrimSpace(event.Reason)
		state.LastRedirectEPDG = strings.TrimSpace(event.RedirectEPDG)
	case "retrying":
		applyRetryingState(state, event.Reason)
	case "error":
		applyRetryingState(state, event.Reason)
		state.LastErrorClass = "runtime"
		state.LastError = firstNonEmptyString(event.Message, event.Reason)
		state.Error = state.LastError
	case "terminal_error":
		state.Phase = "error"
		state.SessionState = "error"
		state.TunnelReady = false
		state.DataPlaneUp = false
		markIMSUnavailable(state, "failed")
		state.LastErrorClass = "runtime"
		state.LastError = firstNonEmptyString(event.Message, event.Reason)
		state.Error = state.LastError
	case "stopped":
		state.Phase = "stopped"
		state.SessionState = "stopped"
		state.TunnelReady = false
		state.DataPlaneUp = false
		markIMSUnavailable(state, "stopped")
	}
}

func applyRetryingState(state *State, reason string) {
	state.Phase = "retrying"
	state.SessionState = "retrying"
	state.TunnelReady = false
	state.DataPlaneUp = false
	markIMSUnavailable(state, "retrying")
	if reason = strings.TrimSpace(reason); reason != "" {
		state.LastReason = reason
	}
}

func markIMSUnavailable(state *State, status string) {
	state.IMSState = status
	state.IMSReady = false
	state.SMSReady = false
	state.SMSRXPending = false
	state.SMSMOReady = false
	state.SMSHealthReady = false
	state.SMSReadyReason = ""
	state.RegStatus = 0
	state.RegStatusText = status
}

func clearRuntimeError(state *State) {
	if state == nil {
		return
	}
	state.LastError = ""
	state.LastErrorClass = ""
	state.Error = ""
}

func clearRecoveredFailure(state *State) {
	if state == nil {
		return
	}
	clearRuntimeError(state)
	state.LastReason = ""
}

func readyPhase(state State) string {
	if state.SMSReady {
		return "sms_ready"
	}
	if state.IMSReady {
		return "ims_ready"
	}
	return "ipsec_up"
}

func (observer *instanceObserver) installSession(
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
) {
	observer.inst.setSession(event.Handle)
	observer.installService(event)
}

func (observer *instanceObserver) clearRuntimeHandles() {
	observer.inst.setService(nil)
	observer.inst.setSession(nil)
}

func (observer *instanceObserver) installService(
	event runtimecore.RuntimeEvent[*runtimecore.SessionResult],
) {
	service := event.Service
	if service == nil && event.Handle != nil {
		service = event.Handle.IMSService
	}
	if service != nil {
		observer.inst.setService(newServiceAdapter(service))
	}
}

func recoveredEventKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "established":
		return "ipsec_up"
	case "retry":
		return "retrying"
	default:
		return strings.TrimSpace(kind)
	}
}
