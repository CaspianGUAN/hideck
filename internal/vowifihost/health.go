package vowifihost

import (
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
)

const healthTimelineLimit = 24

const healthEventLimit = 32

type WiFiCallingHealthSegment struct {
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Reason    string    `json:"reason,omitempty"`
	Current   bool      `json:"current,omitempty"`
}

type WiFiCallingHealthEvent struct {
	Kind   string    `json:"kind"`
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

type WiFiCallingHealthSnapshot struct {
	State                      string                     `json:"state"`
	Active                     bool                       `json:"active"`
	Measured                   bool                       `json:"measured"`
	SessionStartedAt           time.Time                  `json:"session_started_at,omitempty"`
	StableSince                time.Time                  `json:"stable_since,omitempty"`
	UpdatedAt                  time.Time                  `json:"updated_at,omitempty"`
	LastInterruptionAt         time.Time                  `json:"last_interruption_at,omitempty"`
	SessionSeconds             int64                      `json:"session_seconds"`
	HealthySeconds             int64                      `json:"healthy_seconds"`
	InterruptedSeconds         int64                      `json:"interrupted_seconds"`
	StableSeconds              int64                      `json:"stable_seconds"`
	LongestInterruptionSeconds int64                      `json:"longest_interruption_seconds"`
	InterruptionCount          int                        `json:"interruption_count"`
	Availability               float64                    `json:"availability"`
	LastReason                 string                     `json:"last_reason,omitempty"`
	Timeline                   []WiFiCallingHealthSegment `json:"timeline,omitempty"`
	Events                     []WiFiCallingHealthEvent   `json:"events,omitempty"`
}

type wifiCallingHealthSession struct {
	active                bool
	startedAt             time.Time
	endedAt               time.Time
	stableSince           time.Time
	lastObservedAt        time.Time
	lastInterruptionAt    time.Time
	currentState          string
	currentReason         string
	currentStartedAt      time.Time
	currentInterruptionAt time.Time
	healthyDuration       time.Duration
	interruptedDuration   time.Duration
	longestInterruption   time.Duration
	interruptionCount     int
	completedSegments     []WiFiCallingHealthSegment
	events                []WiFiCallingHealthEvent
}

type wifiCallingHealthStore struct {
	mu       sync.Mutex
	sessions map[string]*wifiCallingHealthSession
}

func newWiFiCallingHealthStore() *wifiCallingHealthStore {
	return &wifiCallingHealthStore{sessions: make(map[string]*wifiCallingHealthSession)}
}

func (s *wifiCallingHealthStore) Begin(deviceID string, at time.Time) {
	deviceID = strings.TrimSpace(deviceID)
	if s == nil || deviceID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.sessions[deviceID]
	if previous != nil && previous.active {
		return
	}
	events := retainedTerminalHealthEvent(previous)
	s.sessions[deviceID] = &wifiCallingHealthSession{
		active:         true,
		lastObservedAt: at,
		currentState:   "checking",
		events:         events,
	}
}

func retainedTerminalHealthEvent(session *wifiCallingHealthSession) []WiFiCallingHealthEvent {
	if session == nil {
		return nil
	}
	for index := len(session.events) - 1; index >= 0; index-- {
		event := session.events[index]
		if event.Kind == "stopped" || event.Kind == "failed" {
			return []WiFiCallingHealthEvent{event}
		}
	}
	return nil
}

func (s *wifiCallingHealthStore) Observe(deviceID string, state runtimehost.State) {
	deviceID = strings.TrimSpace(deviceID)
	if s == nil || deviceID == "" {
		return
	}
	at := state.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[deviceID]
	if session == nil {
		session = &wifiCallingHealthSession{active: true}
		s.sessions[deviceID] = session
	}
	if !session.active {
		return
	}
	if !session.lastObservedAt.IsZero() && at.Before(session.lastObservedAt) {
		return
	}
	session.lastObservedAt = at
	if session.startedAt.IsZero() {
		if session.currentState == "pending" && healthState(state) == "unavailable" {
			session.start(at, "unavailable", healthReason(state))
			return
		}
		if state.SMSRXPending && state.IMSReady {
			session.currentState = "pending"
			session.currentReason = healthReason(state)
			return
		}
		if !wifiCallingMeasurable(state) {
			session.currentState = "checking"
			if healthState(state) == "unavailable" {
				session.currentState = "unavailable"
			}
			session.currentReason = healthReason(state)
			return
		}
		session.start(at, healthState(state), healthStartReason(state))
		return
	}
	session.transition(healthState(state), at, healthReason(state))
}

func (s *wifiCallingHealthStore) End(deviceID, reason string, at time.Time) {
	deviceID = strings.TrimSpace(deviceID)
	if s == nil || deviceID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[deviceID]
	if session == nil || !session.active {
		return
	}
	session.finish(at, strings.TrimSpace(reason))
}

func (s *wifiCallingHealthStore) FailStart(deviceID, reason string, at time.Time) {
	deviceID = strings.TrimSpace(deviceID)
	if s == nil || deviceID == "" {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[deviceID]
	if session == nil || !session.active || !session.startedAt.IsZero() {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "VoWiFi startup failed"
	}
	session.active = false
	session.endedAt = at
	session.lastObservedAt = at
	session.currentState = "unavailable"
	session.currentReason = reason
	session.appendEvent("failed", "unavailable", at, reason)
}

func (s *wifiCallingHealthStore) Snapshot(deviceID string, now time.Time) (WiFiCallingHealthSnapshot, bool) {
	deviceID = strings.TrimSpace(deviceID)
	if s == nil || deviceID == "" {
		return WiFiCallingHealthSnapshot{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[deviceID]
	if session == nil {
		return WiFiCallingHealthSnapshot{}, false
	}
	return session.snapshot(now), true
}

func (s *wifiCallingHealthSession) start(at time.Time, state, reason string) {
	if state == "" {
		state = "recovering"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		if state == "healthy" {
			reason = "WiFi Calling ready"
		} else {
			reason = "WiFi Calling service is not fully ready"
		}
	}
	s.active = true
	s.startedAt = at
	s.currentState = state
	s.currentStartedAt = at
	if state == "healthy" {
		s.stableSince = at
	} else {
		s.currentReason = reason
		s.currentInterruptionAt = at
	}
	s.appendEvent("started", state, at, reason)
}

func (s *wifiCallingHealthSession) transition(next string, at time.Time, reason string) {
	if next == "" {
		next = "recovering"
	}
	if s.currentStartedAt.IsZero() {
		s.currentState = next
		s.currentReason = reason
		s.currentStartedAt = at
		return
	}
	if next == s.currentState {
		if reason != "" {
			s.currentReason = reason
		}
		return
	}
	s.closeCurrent(at)
	if s.currentState == "healthy" && next != "healthy" {
		s.interruptionCount++
		s.lastInterruptionAt = at
		s.currentInterruptionAt = at
		s.stableSince = time.Time{}
		s.appendEvent("interrupted", next, at, reason)
	}
	if s.currentState != "healthy" && next == "healthy" {
		s.completeInterruption(at)
		s.stableSince = at
		s.appendEvent("recovered", next, at, reason)
	}
	s.currentState = next
	s.currentReason = reason
	s.currentStartedAt = at
}

func (s *wifiCallingHealthSession) closeCurrent(at time.Time) {
	duration := nonNegativeDuration(s.currentStartedAt, at)
	if s.currentState == "healthy" {
		s.healthyDuration += duration
	} else {
		s.interruptedDuration += duration
	}
	s.completedSegments = append(s.completedSegments, WiFiCallingHealthSegment{
		State: s.currentState, StartedAt: s.currentStartedAt, EndedAt: at, Reason: s.currentReason,
	})
	if overflow := len(s.completedSegments) - healthTimelineLimit; overflow > 0 {
		s.completedSegments = append([]WiFiCallingHealthSegment(nil), s.completedSegments[overflow:]...)
	}
}

func (s *wifiCallingHealthSession) completeInterruption(at time.Time) {
	duration := nonNegativeDuration(s.currentInterruptionAt, at)
	if duration > s.longestInterruption {
		s.longestInterruption = duration
	}
	s.currentInterruptionAt = time.Time{}
}

func (s *wifiCallingHealthSession) finish(at time.Time, reason string) {
	if !s.startedAt.IsZero() && !s.currentStartedAt.IsZero() {
		s.closeCurrent(at)
		if !s.currentInterruptionAt.IsZero() {
			s.completeInterruption(at)
		}
	}
	s.active = false
	s.endedAt = at
	s.lastObservedAt = at
	s.currentState = "stopped"
	s.currentStartedAt = time.Time{}
	if reason != "" {
		s.currentReason = reason
	}
	s.appendEvent("stopped", "stopped", at, reason)
}

func (s *wifiCallingHealthSession) snapshot(now time.Time) WiFiCallingHealthSnapshot {
	if s.startedAt.IsZero() {
		state := strings.TrimSpace(s.currentState)
		if state == "" {
			state = "checking"
		}
		return WiFiCallingHealthSnapshot{
			State: state, Active: s.active, Measured: false,
			UpdatedAt: s.lastObservedAt, LastReason: s.currentReason,
			Events: append([]WiFiCallingHealthEvent(nil), s.events...),
		}
	}
	end := now
	if !s.active && !s.endedAt.IsZero() {
		end = s.endedAt
	}
	healthy := s.healthyDuration
	interrupted := s.interruptedDuration
	segments := append([]WiFiCallingHealthSegment(nil), s.completedSegments...)
	if s.active && !s.currentStartedAt.IsZero() {
		duration := nonNegativeDuration(s.currentStartedAt, end)
		if s.currentState == "healthy" {
			healthy += duration
		} else {
			interrupted += duration
		}
		segments = append(segments, WiFiCallingHealthSegment{
			State: s.currentState, StartedAt: s.currentStartedAt, EndedAt: end,
			Reason: s.currentReason, Current: true,
		})
	}
	total := healthy + interrupted
	availability := 0.0
	if total > 0 {
		availability = 100 * float64(healthy) / float64(total)
	}
	longest := s.longestInterruption
	if current := nonNegativeDuration(s.currentInterruptionAt, end); current > longest {
		longest = current
	}
	stable := time.Duration(0)
	if s.currentState == "healthy" {
		stable = nonNegativeDuration(s.stableSince, end)
	}
	return WiFiCallingHealthSnapshot{
		State: s.currentState, Active: s.active, Measured: true,
		SessionStartedAt: s.startedAt, StableSince: s.stableSince,
		UpdatedAt: s.lastObservedAt, LastInterruptionAt: s.lastInterruptionAt,
		SessionSeconds: seconds(total), HealthySeconds: seconds(healthy),
		InterruptedSeconds: seconds(interrupted), StableSeconds: seconds(stable),
		LongestInterruptionSeconds: seconds(longest), InterruptionCount: s.interruptionCount,
		Availability: availability, LastReason: s.currentReason, Timeline: segments,
		Events: append([]WiFiCallingHealthEvent(nil), s.events...),
	}
}

func (s *wifiCallingHealthSession) appendEvent(kind, state string, at time.Time, reason string) {
	s.events = append(s.events, WiFiCallingHealthEvent{
		Kind: kind, State: state, At: at, Reason: strings.TrimSpace(reason),
	})
	if overflow := len(s.events) - healthEventLimit; overflow > 0 {
		s.events = append([]WiFiCallingHealthEvent(nil), s.events[overflow:]...)
	}
}

func healthState(state runtimehost.State) string {
	if wifiCallingHealthy(state) {
		return "healthy"
	}
	switch strings.TrimSpace(state.Phase) {
	case "error", "interrupted", "stopped":
		return "unavailable"
	default:
		return "recovering"
	}
}

func wifiCallingMeasurable(state runtimehost.State) bool {
	return state.IMSReady || state.SMSHealthReady
}

func wifiCallingHealthy(state runtimehost.State) bool {
	return state.SMSHealthReady || (state.IMSReady && state.SMSReady)
}

func healthStartReason(state runtimehost.State) string {
	if !wifiCallingHealthy(state) {
		return healthReason(state)
	}
	if state.IMSReady {
		return "IMS registered"
	}
	if state.SMSHealthReady {
		return "IMS runtime health ready"
	}
	return ""
}

func healthReason(state runtimehost.State) string {
	if wifiCallingHealthy(state) {
		return ""
	}
	values := []string{state.LastError, state.LastReason}
	if state.IMSReady || state.SMSHealthReady {
		values = []string{state.SMSReadyReason, state.LastError, state.LastReason}
	}
	values = append(values, state.Phase)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func seconds(value time.Duration) int64 {
	return int64(value / time.Second)
}
