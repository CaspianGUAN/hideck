package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/pion/webrtc/v4"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/phone"
)

func TestPhoneRoutesEnforceAuthenticationAndControlLease(t *testing.T) {
	gateway := &phoneRouteGatewayStub{}
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	server := &Server{
		auth:  config.WebConfig{Username: "admin", Password: "secret"},
		phone: service, shutdownCh: make(chan struct{}),
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(server.authMiddleware())
	server.registerPhoneRoutes(api)

	unauthorized := performPhoneRequest(router, http.MethodGet, "/api/phone/history", "", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	token := testSessionToken(t, "secret", time.Now().Add(time.Hour))
	offer, closePeer := browserPhoneOffer(t)
	defer closePeer()
	mediaResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/media", token, "", map[string]string{"sdp": offer})
	if mediaResponse.Code != http.StatusCreated {
		t.Fatalf("media status=%d body=%s", mediaResponse.Code, mediaResponse.Body.String())
	}
	var media struct {
		MediaID string `json:"media_id"`
		Lease   string `json:"lease"`
	}
	if err := json.Unmarshal(mediaResponse.Body.Bytes(), &media); err != nil {
		t.Fatal(err)
	}
	callResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/calls", token, media.Lease, map[string]string{
		"device_id": "dev-1", "callee": "888", "media_id": media.MediaID,
	})
	if callResponse.Code != http.StatusAccepted {
		t.Fatalf("call status=%d body=%s", callResponse.Code, callResponse.Body.String())
	}
	foreign := performPhoneRequest(router, http.MethodPost, "/api/phone/calls/call-api-1/dtmf", token, "foreign", map[string]string{"digit": "5"})
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign lease status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	gateway.emit(voicehost.CallEvent{
		Type: "CallAnswered", DeviceID: "dev-1", CallID: "call-api-1", Time: time.Now(),
	})
	dtmf := performPhoneRequest(router, http.MethodPost, "/api/phone/calls/call-api-1/dtmf", token, media.Lease, map[string]string{"digit": "5"})
	if dtmf.Code != http.StatusNoContent || gateway.dtmf != "call-api-1:5" {
		t.Fatalf("DTMF status=%d forwarded=%q body=%s", dtmf.Code, gateway.dtmf, dtmf.Body.String())
	}
	hold := performPhoneRequest(router, http.MethodPost, "/api/phone/calls/call-api-1/hold", token, media.Lease, map[string]string{})
	if hold.Code != http.StatusNoContent || gateway.dtmf != "call-api-1:hold" {
		t.Fatalf("hold status=%d forwarded=%q body=%s", hold.Code, gateway.dtmf, hold.Body.String())
	}
	resume := performPhoneRequest(router, http.MethodPost, "/api/phone/calls/call-api-1/resume", token, media.Lease, map[string]string{})
	if resume.Code != http.StatusNoContent || gateway.dtmf != "call-api-1:resume" {
		t.Fatalf("resume status=%d forwarded=%q body=%s", resume.Code, gateway.dtmf, resume.Body.String())
	}
	hangup := performPhoneRequest(router, http.MethodDelete, "/api/phone/calls/call-api-1", token, media.Lease, nil)
	if hangup.Code != http.StatusNoContent {
		t.Fatalf("hangup status=%d body=%s", hangup.Code, hangup.Body.String())
	}

	releaseOffer, closeReleasePeer := browserPhoneOffer(t)
	defer closeReleasePeer()
	releaseResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/media", token, "", map[string]string{"sdp": releaseOffer})
	if releaseResponse.Code != http.StatusCreated {
		t.Fatalf("release media status=%d body=%s", releaseResponse.Code, releaseResponse.Body.String())
	}
	var disposable struct {
		MediaID string `json:"media_id"`
		Lease   string `json:"lease"`
	}
	if err := json.Unmarshal(releaseResponse.Body.Bytes(), &disposable); err != nil {
		t.Fatal(err)
	}
	foreignRelease := performPhoneRequest(router, http.MethodDelete, "/api/phone/media/"+disposable.MediaID, token, "foreign", nil)
	if foreignRelease.Code != http.StatusForbidden {
		t.Fatalf("foreign media release status=%d body=%s", foreignRelease.Code, foreignRelease.Body.String())
	}
	ownedRelease := performPhoneRequest(router, http.MethodDelete, "/api/phone/media/"+disposable.MediaID, token, disposable.Lease, nil)
	if ownedRelease.Code != http.StatusNoContent {
		t.Fatalf("owned media release status=%d body=%s", ownedRelease.Code, ownedRelease.Body.String())
	}
}

func TestPhoneEventStreamDisablesProxyBuffering(t *testing.T) {
	gateway := &phoneRouteGatewayStub{}
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	server := &Server{
		auth:  config.WebConfig{Username: "admin", Password: "secret"},
		phone: service, shutdownCh: make(chan struct{}),
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(server.authMiddleware())
	server.registerPhoneRoutes(api)
	token := testSessionToken(t, "secret", time.Now().Add(time.Hour))

	ts := httptest.NewServer(router)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/phone/events?after_id=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", response.Header.Get("X-Accel-Buffering"))
	}
	connected := make([]byte, len(": connected\n\n"))
	if _, err := io.ReadFull(response.Body, connected); err != nil {
		t.Fatalf("read SSE prelude: %v", err)
	}
	if string(connected) != ": connected\n\n" {
		t.Fatalf("SSE prelude = %q", connected)
	}
}

func TestPhoneEventStreamDeliversCallEndedAfterSilentHangup(t *testing.T) {
	gateway := &phoneRouteGatewayStub{silentHangup: true}
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	server := &Server{
		auth:  config.WebConfig{Username: "admin", Password: "secret"},
		phone: service, shutdownCh: make(chan struct{}),
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(server.authMiddleware())
	server.registerPhoneRoutes(api)
	token := testSessionToken(t, "secret", time.Now().Add(time.Hour))
	offer, closePeer := browserPhoneOffer(t)
	defer closePeer()
	mediaResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/media", token, "", map[string]string{"sdp": offer})
	if mediaResponse.Code != http.StatusCreated {
		t.Fatalf("media status=%d body=%s", mediaResponse.Code, mediaResponse.Body.String())
	}
	var media struct {
		MediaID string `json:"media_id"`
		Lease   string `json:"lease"`
	}
	if err := json.Unmarshal(mediaResponse.Body.Bytes(), &media); err != nil {
		t.Fatal(err)
	}
	callResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/calls", token, media.Lease, map[string]string{
		"device_id": "dev-1", "callee": "888", "media_id": media.MediaID,
	})
	if callResponse.Code != http.StatusAccepted {
		t.Fatalf("call status=%d body=%s", callResponse.Code, callResponse.Body.String())
	}

	ts := httptest.NewServer(router)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/phone/events?after_id=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering = %q", response.Header.Get("X-Accel-Buffering"))
	}

	hangup := performPhoneRequest(router, http.MethodDelete, "/api/phone/calls/call-api-1", token, media.Lease, nil)
	if hangup.Code != http.StatusNoContent {
		t.Fatalf("hangup status=%d body=%s", hangup.Code, hangup.Body.String())
	}

	var got []byte
	buf := make([]byte, 4096)
	for {
		n, readErr := response.Body.Read(buf)
		got = append(got, buf[:n]...)
		if bytes.Contains(got, []byte(`"type":"call_ended"`)) {
			return
		}
		if readErr != nil {
			t.Fatalf("sse read: %v body=%s", readErr, got)
		}
	}
}

type phoneRouteGatewayStub struct {
	incoming     func(voicehost.IncomingCall)
	events       func(voicehost.CallEvent)
	dtmf         string
	silentHangup bool
	holdErr      error
}

func (g *phoneRouteGatewayStub) SubscribeIncomingCalls(handler func(voicehost.IncomingCall)) func() {
	g.incoming = handler
	return func() {}
}

func (g *phoneRouteGatewayStub) SubscribeCallEvents(handler func(voicehost.CallEvent)) func() {
	g.events = handler
	return func() {}
}

func (g *phoneRouteGatewayStub) BeginCall(_ context.Context, _ voicehost.BeginCallRequest) (voicehost.CallSnapshot, error) {
	return voicehost.CallSnapshot{CallID: "call-api-1", DeviceID: "dev-1"}, nil
}

func (g *phoneRouteGatewayStub) ActiveCall(string) *voicehost.CallSnapshot {
	return &voicehost.CallSnapshot{CallID: "call-api-1", DeviceID: "dev-1", ClientSDP: apiPhonePlainSDP}
}

func (g *phoneRouteGatewayStub) AnswerIncomingCall(_ context.Context, request voicehost.AnswerRequest) (voicehost.AnswerResult, error) {
	return voicehost.AnswerResult{CallID: request.CallID}, nil
}

func (g *phoneRouteGatewayStub) RejectIncomingCall(voicehost.RejectRequest) error { return nil }

func (g *phoneRouteGatewayStub) HangupCall(_ context.Context, deviceID, callID string) error {
	if g.silentHangup {
		return nil
	}
	g.emit(voicehost.CallEvent{
		Type: "CallEnded", DeviceID: deviceID, CallID: callID, Reason: "local_hangup", Time: time.Now(),
	})
	return nil
}

func (g *phoneRouteGatewayStub) SendCallDTMF(_, callID, digit string) error {
	g.dtmf = callID + ":" + digit
	return nil
}

func (g *phoneRouteGatewayStub) HoldCall(_ context.Context, _, callID string) error {
	if g.holdErr != nil {
		return g.holdErr
	}
	g.dtmf = callID + ":hold"
	g.emit(voicehost.CallEvent{
		Type: "CallMediaUpdated", DeviceID: "dev-1", CallID: callID, Held: true, Time: time.Now(),
	})
	return nil
}

func (g *phoneRouteGatewayStub) SwitchCall(_, callID string) error {
	g.dtmf = callID + ":switch"
	return nil
}

func (g *phoneRouteGatewayStub) ResumeCall(_ context.Context, _, callID string) error {
	g.dtmf = callID + ":resume"
	g.emit(voicehost.CallEvent{
		Type: "CallMediaUpdated", DeviceID: "dev-1", CallID: callID, Held: false, Time: time.Now(),
	})
	return nil
}

func (g *phoneRouteGatewayStub) StartCallCapture(_, _, _ string) error { return nil }

func (g *phoneRouteGatewayStub) emit(event voicehost.CallEvent) {
	if g.events != nil {
		g.events(event)
	}
}

func performPhoneRequest(router http.Handler, method, path, token, lease string, body interface{}) *httptest.ResponseRecorder {
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if lease != "" {
		request.Header.Set("X-Phone-Lease", lease)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func browserPhoneOffer(t *testing.T) (string, func()) {
	t.Helper()
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		_ = peer.Close()
		t.Fatal(err)
	}
	gathering := webrtc.GatheringCompletePromise(peer)
	offer, err := peer.CreateOffer(nil)
	if err == nil {
		err = peer.SetLocalDescription(offer)
	}
	if err != nil {
		_ = peer.Close()
		t.Fatal(err)
	}
	select {
	case <-gathering:
	case <-time.After(time.Second):
		_ = peer.Close()
		t.Fatal("ICE gathering timed out")
	}
	return peer.LocalDescription().SDP, func() { _ = peer.Close() }
}

const apiPhonePlainSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=phone\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0\r\n"

func TestPhoneHoldReturnsChineseWhenNotAligned(t *testing.T) {
	gateway := &phoneRouteGatewayStub{holdErr: voicehost.ErrHoldNotAligned}
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	server := &Server{
		auth:  config.WebConfig{Username: "admin", Password: "secret"},
		phone: service, shutdownCh: make(chan struct{}),
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(server.authMiddleware())
	server.registerPhoneRoutes(api)
	token := testSessionToken(t, "secret", time.Now().Add(time.Hour))
	offer, closePeer := browserPhoneOffer(t)
	defer closePeer()
	mediaResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/media", token, "", map[string]string{"sdp": offer})
	if mediaResponse.Code != http.StatusCreated {
		t.Fatalf("media status=%d body=%s", mediaResponse.Code, mediaResponse.Body.String())
	}
	var media struct {
		MediaID string `json:"media_id"`
		Lease   string `json:"lease"`
	}
	if err := json.Unmarshal(mediaResponse.Body.Bytes(), &media); err != nil {
		t.Fatal(err)
	}
	callResponse := performPhoneRequest(router, http.MethodPost, "/api/phone/calls", token, media.Lease, map[string]string{
		"device_id": "dev-1", "callee": "888", "media_id": media.MediaID,
	})
	if callResponse.Code != http.StatusAccepted {
		t.Fatalf("call status=%d body=%s", callResponse.Code, callResponse.Body.String())
	}
	gateway.emit(voicehost.CallEvent{
		Type: "CallAnswered", DeviceID: "dev-1", CallID: "call-api-1", Time: time.Now(),
	})
	hold := performPhoneRequest(router, http.MethodPost, "/api/phone/calls/call-api-1/hold", token, media.Lease, map[string]string{})
	if hold.Code != http.StatusServiceUnavailable || !strings.Contains(hold.Body.String(), "保持未对齐，暂不可用") {
		t.Fatalf("hold status=%d body=%s", hold.Code, hold.Body.String())
	}
	hangup := performPhoneRequest(router, http.MethodDelete, "/api/phone/calls/call-api-1", token, media.Lease, nil)
	if hangup.Code != http.StatusNoContent {
		t.Fatalf("hangup status=%d body=%s", hangup.Code, hangup.Body.String())
	}
}
