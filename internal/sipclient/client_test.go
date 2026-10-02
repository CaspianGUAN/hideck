package sipclient

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/phone"
)

const testPBXSDP = "v=0\r\no=pbx 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
	"m=audio 40000 RTP/AVP 0 101\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:101 telephone-event/8000\r\n"

type fakeMedia struct {
	id      string
	applied chan string
}

func (m *fakeMedia) MediaID() string    { return m.id }
func (m *fakeMedia) MediaLease() string { return "lease-" + m.id }
func (m *fakeMedia) OfferSDP() string   { return testPBXSDP }
func (m *fakeMedia) AnswerOffer(offer string) (string, error) {
	if !strings.Contains(offer, "m=audio") {
		return "", fmt.Errorf("no audio in offer")
	}
	return testPBXSDP, nil
}
func (m *fakeMedia) ApplyAnswer(answer string) error {
	m.applied <- answer
	return nil
}

type fakePhone struct {
	mu       sync.Mutex
	nextID   int
	media    []*fakeMedia
	started  chan phone.StartCallRequest
	answered chan phone.ControlRequest
	hungup   chan string
	stream   chan phone.Event
}

func newFakePhone() *fakePhone {
	return &fakePhone{
		started: make(chan phone.StartCallRequest, 4), answered: make(chan phone.ControlRequest, 4),
		hungup: make(chan string, 4), stream: make(chan phone.Event, 16),
	}
}

func (p *fakePhone) CreateMedia(owner, advertiseIP string, onDTMF func(string)) (MediaLeg, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	media := &fakeMedia{id: "m" + strconv.Itoa(p.nextID), applied: make(chan string, 1)}
	p.media = append(p.media, media)
	return media, nil
}
func (p *fakePhone) ReleaseMedia(MediaLeg) {}
func (p *fakePhone) StartCall(request phone.StartCallRequest) (phone.CallView, error) {
	p.started <- request
	return phone.CallView{CallID: "out-1", DeviceID: request.DeviceID, MediaID: request.MediaID}, nil
}
func (p *fakePhone) Answer(_ context.Context, request phone.ControlRequest) (phone.CallView, error) {
	p.answered <- request
	return phone.CallView{CallID: request.CallID, MediaID: request.MediaID}, nil
}
func (p *fakePhone) Hangup(_ context.Context, _, callID, _ string) error {
	p.hungup <- callID
	return nil
}
func (p *fakePhone) DTMF(string, string, string, string) error { return nil }
func (p *fakePhone) Subscribe(uint64) ([]phone.Event, <-chan phone.Event, func()) {
	return nil, p.stream, func() {}
}

type fakeDevices struct{}

func (fakeDevices) DeviceIDs() []string                       { return []string{"dev1"} }
func (fakeDevices) PrepareCall(context.Context, string) error { return nil }

// fakePBX is a minimal registrar/UA standing in for FreePBX.
type fakePBX struct {
	port      int
	client    *sipgo.Client
	dialogs   *sipgo.DialogClientCache
	serving   *sipgo.DialogServerCache
	registers chan *sip.Request
	invites   chan *sip.Request
	byes      chan *sip.Request
}

func startFakePBX(t *testing.T) *fakePBX {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("pbx"), sipgo.WithUserAgentHostname("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	contactURI := sip.Uri{Scheme: "sip", User: "pbx", Host: "127.0.0.1", Port: port, UriParams: sip.NewParams()}
	contactURI.UriParams.Add("transport", "tcp")
	contact := sip.ContactHeader{Address: contactURI}
	pbx := &fakePBX{
		port: port, client: client,
		dialogs: sipgo.NewDialogClientCache(client, contact), serving: sipgo.NewDialogServerCache(client, contact),
		registers: make(chan *sip.Request, 8), invites: make(chan *sip.Request, 4), byes: make(chan *sip.Request, 4),
	}
	server.OnRegister(func(request *sip.Request, tx sip.ServerTransaction) {
		if request.GetHeader("Authorization") == nil {
			response := sip.NewResponseFromRequest(request, sip.StatusUnauthorized, "Unauthorized", nil)
			response.AppendHeader(sip.NewHeader("WWW-Authenticate", `Digest realm="asterisk", nonce="abc123", algorithm=MD5`))
			_ = tx.Respond(response)
			return
		}
		pbx.registers <- request
		response := sip.NewResponseFromRequest(request, sip.StatusOK, "OK", nil)
		response.AppendHeader(sip.NewHeader("Expires", "120"))
		_ = tx.Respond(response)
	})
	server.OnInvite(func(request *sip.Request, tx sip.ServerTransaction) {
		dialog, err := pbx.serving.ReadInvite(request, tx)
		if err != nil {
			return
		}
		pbx.invites <- request
		_ = dialog.Respond(sip.StatusRinging, "Ringing", nil)
		_ = dialog.RespondSDP([]byte(testPBXSDP))
	})
	server.OnAck(func(request *sip.Request, tx sip.ServerTransaction) { _ = pbx.serving.ReadAck(request, tx) })
	server.OnBye(func(request *sip.Request, tx sip.ServerTransaction) {
		pbx.byes <- request
		if err := pbx.serving.ReadBye(request, tx); err != nil {
			_ = pbx.dialogs.ReadBye(request, tx)
		}
	})
	go func() { _ = server.ServeTCP(listener) }()
	t.Cleanup(func() { _ = ua.Close() })
	return pbx
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func startTestClient(t *testing.T, pbx *fakePBX, fake *fakePhone) (*Client, int) {
	t.Helper()
	localPort := freeTCPPort(t)
	settings, err := SettingsFromConfigForTest(pbx.port, localPort)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(settings, fake, fakeDevices{})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(client.Stop)
	select {
	case request := <-pbx.registers:
		if user := request.From().Address.User; user != "9001" {
			t.Fatalf("REGISTER From user = %q", user)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client did not complete digest registration")
	}
	waitFor(t, client.isRegistered)
	return client, localPort
}

func SettingsFromConfigForTest(pbxPort, localPort int) (Settings, error) {
	settings := Settings{
		ServerHost: "127.0.0.1", ServerPort: pbxPort, Transport: "tcp",
		Username: "9001", AuthUsername: "9001", Password: "secret",
		LocalPort: localPort, Expires: 120 * time.Second, InboundTo: "1001",
		RingTimeout: 5 * time.Second,
	}
	return settings, nil
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPBXCallIsPlacedOnModemAndBridged(t *testing.T) {
	pbx := startFakePBX(t)
	fake := newFakePhone()
	_, localPort := startTestClient(t, pbx, fake)

	target := sip.Uri{Scheme: "sip", User: "+12135550100", Host: "127.0.0.1", Port: localPort, UriParams: sip.NewParams()}
	target.UriParams.Add("transport", "tcp")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dialog, err := pbx.dialogs.Invite(ctx, target, []byte(testPBXSDP), sip.NewHeader("Content-Type", "application/sdp"))
	if err != nil {
		t.Fatal(err)
	}
	var started phone.StartCallRequest
	select {
	case started = <-fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("PBX INVITE did not start a modem call")
	}
	if started.Callee != "+12135550100" || started.DeviceID != "dev1" || started.Owner != "sip:9001" {
		t.Fatalf("StartCall request = %+v", started)
	}
	fake.stream <- phone.Event{Type: "call_ringing", Call: phone.CallView{CallID: "out-1", MediaID: started.MediaID}}
	fake.stream <- phone.Event{Type: "call_answered", Call: phone.CallView{CallID: "out-1", MediaID: started.MediaID}}
	if err := dialog.WaitAnswer(ctx, sipgo.AnswerOptions{}); err != nil {
		t.Fatalf("PBX did not get 200 OK: %v", err)
	}
	if body := string(dialog.InviteResponse.Body()); !strings.Contains(body, "m=audio") {
		t.Fatalf("200 OK SDP = %q", body)
	}
	if err := dialog.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dialog.Bye(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case callID := <-fake.hungup:
		if callID != "out-1" {
			t.Fatalf("hung up %q", callID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PBX BYE did not hang up the modem call")
	}
}

func TestModemCallRingsPBXAndPBXAnswerTakesIt(t *testing.T) {
	pbx := startFakePBX(t)
	fake := newFakePhone()
	startTestClient(t, pbx, fake)

	fake.stream <- phone.Event{Type: "incoming_call", Call: phone.CallView{
		CallID: "in-1", DeviceID: "dev1", Direction: "inbound", Peer: "+12135550199", Status: phone.StatusRinging,
	}}
	var invite *sip.Request
	select {
	case invite = <-pbx.invites:
	case <-time.After(5 * time.Second):
		t.Fatal("incoming modem call did not ring the PBX")
	}
	if invite.Recipient.User != "1001" {
		t.Fatalf("INVITE target = %q", invite.Recipient.User)
	}
	if from := invite.From(); from.DisplayName != "+12135550199" || from.Address.User != "9001" {
		t.Fatalf("INVITE From = %q <%s>", from.DisplayName, from.Address.User)
	}
	if pai := invite.GetHeader("P-Asserted-Identity"); pai == nil || !strings.Contains(pai.Value(), "+12135550199") {
		t.Fatalf("P-Asserted-Identity = %v", pai)
	}
	select {
	case request := <-fake.answered:
		if request.CallID != "in-1" || request.Owner != "sip:9001" {
			t.Fatalf("Answer request = %+v", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PBX answer did not answer the modem call")
	}
	fake.mu.Lock()
	media := fake.media[0]
	fake.mu.Unlock()
	select {
	case answer := <-media.applied:
		if !strings.Contains(answer, "m=audio") {
			t.Fatalf("applied answer = %q", answer)
		}
	default:
		t.Fatal("PBX SDP answer was not applied to the media leg")
	}
	fake.stream <- phone.Event{Type: "call_ended", Call: phone.CallView{CallID: "in-1", MediaID: media.id}}
	select {
	case <-pbx.byes:
	case <-time.After(5 * time.Second):
		t.Fatal("modem hangup did not BYE the PBX")
	}
}

func TestDialedNumberNormalization(t *testing.T) {
	cases := map[string]string{
		"+1 (213) 555-0100": "+12135550100",
		"12135550100":       "12135550100",
		"1001abc":           "",
		"++1":               "",
	}
	for input, want := range cases {
		if got := normalizeNumber(input); got != want {
			t.Errorf("normalizeNumber(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEndStatusMapping(t *testing.T) {
	cases := []struct {
		call phone.CallView
		want int
	}{
		{phone.CallView{Status: phone.StatusBusy}, sip.StatusBusyHere},
		{phone.CallView{EndReason: "remote busy"}, sip.StatusBusyHere},
		{phone.CallView{EndReason: "603 Decline"}, sip.StatusGlobalDecline},
		{phone.CallView{EndReason: "local_hangup"}, sip.StatusRequestTerminated},
		{phone.CallView{EndReason: "network"}, sip.StatusTemporarilyUnavailable},
	}
	for _, testCase := range cases {
		if got := endStatus(testCase.call); got != testCase.want {
			t.Errorf("endStatus(%+v) = %d, want %d", testCase.call, got, testCase.want)
		}
	}
}

func TestSettingsDefaults(t *testing.T) {
	settings, err := SettingsFromConfig(configForTest("192.168.8.244:50600"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.ServerHost != "192.168.8.244" || settings.ServerPort != 50600 || settings.Transport != "tcp" ||
		settings.LocalPort != defaultLocalPort || settings.AuthUsername != "9001" {
		t.Fatalf("settings = %+v", settings)
	}
	if _, err := SettingsFromConfig(configForTest("")); err == nil {
		t.Fatal("empty server accepted")
	}
}

func configForTest(server string) config.SIPClientConfig {
	return config.SIPClientConfig{Enabled: true, Server: server, Username: "9001", Password: "secret"}
}
