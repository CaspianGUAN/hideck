package runtimecore

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/engine/swu"
)

func TestOverlappingReauthKeepsOldSessionUntilSuccessorIsUp(t *testing.T) {
	var (
		starts    atomic.Int32
		stops     []string
		stopMu    sync.Mutex
		successor SessionConfig
		oldLive   = make(chan *swu.Session, 1)
	)
	req := baseRuntimeRequest(&eventRecorder{})
	req.Reconnect = true
	req.fastReauth.Capture()("reauth@example", []byte{9}, []byte{8}, []byte{7})
	req.Dataplane.TUNName = "tun-ims"
	req.StopSession = func(_ context.Context, result *SessionResult) {
		stopMu.Lock()
		if result != nil && result.Session != nil {
			stops = append(stops, result.Session.State())
			result.Session.Shutdown()
		} else {
			stops = append(stops, "nil")
		}
		stopMu.Unlock()
	}
	req.SessionStarter = func(_ context.Context, config SessionConfig) (*SessionResult, error) {
		n := starts.Add(1)
		session := swu.NewSession(&swu.Config{
			FastReauthID:       config.FastReauthID,
			OmitInitialContact: config.OmitInitialContact,
			TUNName:            config.TUNName,
		})
		result := &SessionResult{
			DeviceID: "dev-1", Session: session,
			Snapshot: swu.SessionSnapshot{Established: true, IPv4: []byte{10, 0, 0, byte(n)}},
		}
		if n == 1 {
			oldLive <- session
			return result, nil
		}
		stopMu.Lock()
		stoppedEarly := len(stops) != 0
		stopMu.Unlock()
		if stoppedEarly {
			t.Error("old SA was deleted before the successor IKE runtime started")
		}
		if !config.OmitInitialContact {
			t.Error("successor IKE_AUTH still advertised INITIAL_CONTACT")
		}
		if config.FastReauthID != "" {
			t.Errorf("successor replayed fast reauth identity %q", config.FastReauthID)
		}
		if config.TUNName != "tun-ims-reauth" {
			t.Errorf("successor TUNName = %q", config.TUNName)
		}
		successor = config
		return result, nil
	}
	req.Hooks.OnInterruptReady = func(context.Context) {
		if starts.Load() != 1 {
			return
		}
		select {
		case session := <-oldLive:
			go session.OnReauthNeeded()
		default:
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (Runtime{}).Start(ctx, req)
		done <- err
	}()
	deadline := time.After(time.Second)
	for starts.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("successor IKE runtime did not start, starts=%d", starts.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	waitFor := time.After(time.Second)
	for {
		stopMu.Lock()
		n := len(stops)
		stopMu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-waitFor:
			t.Fatal("old SA was not deleted after the successor came up")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not return after cancel")
	}
	if successor.FastReauthID != "" || !successor.OmitInitialContact {
		t.Fatalf("successor config = %+v", successor)
	}
}

func TestOverlappingReauthKeepsOldSessionWhenSuccessorFails(t *testing.T) {
	var (
		starts  atomic.Int32
		stopped atomic.Bool
		oldLive = make(chan *swu.Session, 1)
	)
	recorder := &eventRecorder{}
	req := baseRuntimeRequest(recorder)
	req.Reconnect = true
	req.StopSession = func(_ context.Context, result *SessionResult) {
		stopped.Store(true)
		if result != nil && result.Session != nil {
			result.Session.Shutdown()
		}
	}
	req.SessionStarter = func(_ context.Context, config SessionConfig) (*SessionResult, error) {
		n := starts.Add(1)
		if n == 1 {
			session := swu.NewSession(&swu.Config{})
			oldLive <- session
			return &SessionResult{
				DeviceID: "dev-1", Session: session,
				Snapshot: swu.SessionSnapshot{Established: true, IPv4: []byte{10, 0, 0, 1}},
			}, nil
		}
		if !config.OmitInitialContact {
			t.Error("failed successor still sent INITIAL_CONTACT")
		}
		return nil, errors.New("ePDG rejected overlapping IKE_AUTH")
	}
	req.Hooks.OnInterruptReady = func(context.Context) {
		if starts.Load() != 1 {
			return
		}
		select {
		case session := <-oldLive:
			go session.OnReauthNeeded()
		default:
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (Runtime{}).Start(ctx, req)
		done <- err
	}()
	deadline := time.After(time.Second)
	for starts.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("successor attempt did not run")
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(30 * time.Millisecond)
	if stopped.Load() {
		t.Fatal("old SA was deleted after the successor failed")
	}
	for _, kind := range recorder.kinds() {
		if kind == "interrupted" || kind == "error" {
			t.Fatalf("failed reauth incorrectly marked live runtime %s", kind)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not return after cancel")
	}
}

func TestStartOverlappingReauthOmitsInitialContactAndUsesFullAuth(t *testing.T) {
	req := baseRuntimeRequest(&eventRecorder{})
	req.fastReauth.Capture()("reauth@example", []byte{1}, []byte{2}, []byte{3})
	var got SessionConfig
	req.SessionStarter = func(_ context.Context, config SessionConfig) (*SessionResult, error) {
		got = config
		return &SessionResult{
			DeviceID: "dev-1",
			Snapshot: swu.SessionSnapshot{Established: true, IPv4: []byte{10, 0, 0, 2}},
		}, nil
	}
	session, err := startOverlappingReauth(context.Background(), &req, nil)
	if err != nil {
		t.Fatalf("startOverlappingReauth: %v", err)
	}
	if session == nil {
		t.Fatal("startOverlappingReauth returned nil session")
	}
	if !got.OmitInitialContact {
		t.Fatal("overlapping reauth sent INITIAL_CONTACT")
	}
	if got.FastReauthID != "" {
		t.Fatalf("overlapping reauth replayed fast reauth identity %q", got.FastReauthID)
	}
	if req.omitInitialContact {
		t.Fatal("request OmitInitialContact leaked onto the original runtime")
	}
}
