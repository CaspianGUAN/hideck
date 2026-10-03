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
	// stream fans out to every subscriber, like the phone service does.
	stream      chan phone.Event
	subscribers []chan phone.Event
}

func newFakePhone() *fakePhone {
	p := &fakePhone{
		started: make(chan phone.StartCallRequest, 4), answered: make(chan phone.ControlRequest, 4),
		hungup: make(chan string, 4), stream: make(chan phone.Event, 16),
	}
	go func() {
		for event := range p.stream {
			var subscribers []chan phone.Event
			for len(subscribers) == 0 { // hold events until a line subscribes
				p.mu.Lock()
				subscribers = append(subscribers, p.subscribers...)
				p.mu.Unlock()
				if len(subscribers) == 0 {
					time.Sleep(5 * time.Millisecond)
				}
			}
			for _, subscriber := range subscribers {
				subscriber <- event
			}
		}
	}()
	return p
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
	subscriber := make(chan phone.Event, 16)
	p.mu.Lock()
	p.subscribers = append(p.subscribers, subscriber)
	p.mu.Unlock()
	return nil, subscriber, func() {}
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
	answered  chan *sipgo.DialogServerSession
}

func startFakePBX(t *testing.T) *fakePBX {
	t.Helper()
	return startFakePBXAdvertising(t, "127.0.0.1")
}

// startFakePBXAdvertising puts contactHost in the PBX's Contact, like FreePBX
// advertising its external address to a peer outside local_net.
func startFakePBXAdvertising(t *testing.T, contactHost string) *fakePBX {
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
	contactURI := sip.Uri{Scheme: "sip", User: "pbx", Host: contactHost, Port: port, UriParams: sip.NewParams()}
	contactURI.UriParams.Add("transport", "tcp")
	contact := sip.ContactHeader{Address: contactURI}
	pbx := &fakePBX{
		port: port, client: client,
		dialogs: sipgo.NewDialogClientCache(client, contact), serving: sipgo.NewDialogServerCache(client, contact),
		registers: make(chan *sip.Request, 8), invites: make(chan *sip.Request, 4), byes: make(chan *sip.Request, 4),
		answered: make(chan *sipgo.DialogServerSession, 4),
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
		if dialog.RespondSDP([]byte(testPBXSDP)) == nil {
			pbx.answered <- dialog
		}
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
	reInvite := func(sdp string) int {
		t.Helper()
		request := sip.NewRequest(sip.INVITE, dialog.InviteResponse.Contact().Address)
		request.SetBody([]byte(sdp))
		request.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
		response, err := dialog.Do(ctx, request)
		if err != nil {
			t.Fatalf("re-INVITE: %v", err)
		}
		if response.IsSuccess() {
			ack := sip.NewRequest(sip.ACK, dialog.InviteResponse.Contact().Address)
			_ = dialog.WriteRequest(ack)
		}
		return response.StatusCode
	}
	if status := reInvite(testPBXSDP); status != sip.StatusOK {
		t.Fatalf("PBX-anchored re-INVITE answered %d, want 200", status)
	}
	directMedia := strings.ReplaceAll(testPBXSDP, "IN IP4 127.0.0.1", "IN IP4 10.9.9.9")
	if status := reInvite(directMedia); status != sip.StatusNotAcceptableHere {
		t.Fatalf("direct-media re-INVITE answered %d, want 488", status)
	}
	select {
	case extra := <-fake.started:
		t.Fatalf("re-INVITE started another modem call: %+v", extra)
	default:
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
	if settings.Mode != ModeRegister || settings.ServerHost != "192.168.8.244" || settings.ServerPort != 50600 || settings.Transport != "tcp" ||
		settings.LocalPort != defaultLocalPort || settings.AuthUsername != "9001" {
		t.Fatalf("settings = %+v", settings)
	}
	if _, err := SettingsFromConfig(configForTest("")); err == nil {
		t.Fatal("empty server accepted")
	}
	trunk := configForTest("127.0.0.1:5060")
	trunk.Mode, trunk.Username = "trunk", ""
	if settings, err := SettingsFromConfig(trunk); err != nil || settings.Mode != ModeTrunk {
		t.Fatalf("trunk settings = %+v, %v", settings, err)
	}
	invalid := configForTest("127.0.0.1:5060")
	invalid.Mode = "peer"
	if _, err := SettingsFromConfig(invalid); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func configForTest(server string) config.SIPClientConfig {
	return config.SIPClientConfig{Enabled: true, Server: server, Username: "9001", Password: "secret"}
}

func TestTrunkModeSkipsRegistrationAndPresentsCaller(t *testing.T) {
	pbx := startFakePBX(t)
	fake := newFakePhone()
	settings, _ := SettingsFromConfigForTest(pbx.port, freeTCPPort(t))
	settings.Mode, settings.Password = ModeTrunk, ""
	client, err := New(settings, fake, fakeDevices{})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(client.Stop)
	if !client.isRegistered() {
		t.Fatal("trunk mode should be ready without registering")
	}

	fake.stream <- phone.Event{Type: "incoming_call", Call: phone.CallView{
		CallID: "in-2", DeviceID: "dev1", Direction: "inbound", Peer: "+12135550199", Status: phone.StatusRinging,
	}}
	select {
	case invite := <-pbx.invites:
		if user := invite.From().Address.User; user != "+12135550199" {
			t.Fatalf("trunk INVITE From user = %q, want caller", user)
		}
		if invite.Recipient.User != "1001" {
			t.Fatalf("trunk INVITE target = %q", invite.Recipient.User)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("incoming modem call did not reach the trunk")
	}
	select {
	case request := <-pbx.registers:
		t.Fatalf("trunk mode sent REGISTER: %s", request.StartLine())
	case <-time.After(200 * time.Millisecond):
	}
}

func TestFromPBXAcceptsOnlyThePBXAddress(t *testing.T) {
	client := &Client{pbxIPs: []net.IP{net.ParseIP("192.168.8.244")}}
	check := func(source string) bool {
		request := sip.NewRequest(sip.INVITE, sip.Uri{Host: "127.0.0.1"})
		request.SetSource(source)
		return client.fromPBX(request)
	}
	if !check("192.168.8.244:50600") {
		t.Fatal("PBX address rejected")
	}
	if check("192.168.8.50:5060") {
		t.Fatal("non-PBX address accepted")
	}
	loopback := &Client{pbxIPs: []net.IP{net.ParseIP("127.0.0.1")}}
	request := sip.NewRequest(sip.INVITE, sip.Uri{Host: "127.0.0.1"})
	request.SetSource("[::1]:5060")
	if !loopback.fromPBX(request) {
		t.Fatal("loopback PBX rejected over ::1")
	}
}

// The PBX hanging up first (an IVR ending its prompt) must end the modem call
// right away; the caller otherwise sits in silence.
func TestPBXHangupEndsAnsweredModemCall(t *testing.T) {
	// 192.0.2.1 (TEST-NET) is unreachable: ACK and BYE must follow the PBX's
	// source address, not its advertised Contact.
	pbx := startFakePBXAdvertising(t, "192.0.2.1")
	fake := newFakePhone()
	startTestClient(t, pbx, fake)

	fake.stream <- phone.Event{Type: "incoming_call", Call: phone.CallView{
		CallID: "in-2", DeviceID: "dev1", Direction: "inbound", Peer: "+12135550199", Status: phone.StatusRinging,
	}}
	var dialog *sipgo.DialogServerSession
	select {
	case dialog = <-pbx.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("PBX did not answer")
	}
	select {
	case <-fake.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("PBX answer did not answer the modem call")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := dialog.Bye(ctx); err != nil {
		t.Fatalf("PBX BYE: %v", err)
	}
	select {
	case callID := <-fake.hungup:
		if callID != "in-2" {
			t.Fatalf("hung up %q", callID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PBX BYE did not hang up the modem call")
	}
}

func TestModemHangupReachesPBXBehindExternalContact(t *testing.T) {
	pbx := startFakePBXAdvertising(t, "192.0.2.1")
	fake := newFakePhone()
	startTestClient(t, pbx, fake)

	fake.stream <- phone.Event{Type: "incoming_call", Call: phone.CallView{
		CallID: "in-3", DeviceID: "dev1", Direction: "inbound", Peer: "+12135550199", Status: phone.StatusRinging,
	}}
	select {
	case <-fake.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("PBX answer did not answer the modem call")
	}
	fake.mu.Lock()
	media := fake.media[len(fake.media)-1]
	fake.mu.Unlock()
	fake.stream <- phone.Event{Type: "call_ended", Call: phone.CallView{CallID: "in-3", MediaID: media.id}}
	select {
	case <-pbx.byes:
	case <-time.After(3 * time.Second):
		t.Fatal("modem hangup BYE went to the advertised Contact instead of the PBX")
	}
}

func TestCheckLinesNeedsDistinctCardsAndPorts(t *testing.T) {
	line := func(device string, port int) Settings { return Settings{DeviceID: device, LocalPort: port} }
	if err := CheckLines([]Settings{line("", 5070)}); err != nil {
		t.Fatalf("single unbound line rejected: %v", err)
	}
	if err := CheckLines([]Settings{line("a", 5070), line("b", 5071)}); err != nil {
		t.Fatalf("distinct lines rejected: %v", err)
	}
	for name, lines := range map[string][]Settings{
		"unbound":   {line("a", 5070), line("", 5071)},
		"same card": {line("a", 5070), line("a", 5071)},
		"same port": {line("a", 5070), line("b", 5070)},
	} {
		if err := CheckLines(lines); err == nil {
			t.Errorf("%s: CheckLines accepted %+v", name, lines)
		}
	}
}

// Two lines on one HiDeck: a call on each card must ring the PBX only from
// the line bound to that card.
func TestEachLineRingsOnlyForItsOwnCard(t *testing.T) {
	pbx := startFakePBX(t)
	fake := newFakePhone()
	start := func(device string) int {
		localPort := freeTCPPort(t)
		settings, _ := SettingsFromConfigForTest(pbx.port, localPort)
		settings.DeviceID = device
		client, err := New(settings, fake, fakeDevices{})
		if err != nil {
			t.Fatal(err)
		}
		client.Start()
		t.Cleanup(client.Stop)
		select {
		case <-pbx.registers:
		case <-time.After(5 * time.Second):
			t.Fatal("line did not register")
		}
		waitFor(t, client.isRegistered)
		return localPort
	}
	portA, portB := start("dev-a"), start("dev-b")
	if settingsA, settingsB := (Settings{Username: "9001", DeviceID: "dev-a"}), (Settings{Username: "9001", DeviceID: "dev-b"}); settingsA.owner() == settingsB.owner() {
		t.Fatal("lines sharing a username share an owner")
	}

	for _, tc := range []struct {
		device string
		port   int
	}{{"dev-b", portB}, {"dev-a", portA}} {
		fake.stream <- phone.Event{Type: "incoming_call", Call: phone.CallView{
			CallID: "in-" + tc.device, DeviceID: tc.device, Direction: "inbound", Peer: "+12135550199", Status: phone.StatusRinging,
		}}
		select {
		case invite := <-pbx.invites:
			if got := invite.Contact().Address.Port; got != tc.port {
				t.Fatalf("%s call rang the PBX from port %d, want %d", tc.device, got, tc.port)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s call did not ring the PBX", tc.device)
		}
		select {
		case invite := <-pbx.invites:
			t.Fatalf("%s call rang the PBX twice (from port %d)", tc.device, invite.Contact().Address.Port)
		case <-time.After(300 * time.Millisecond):
		}
	}
	for range 2 {
		select {
		case <-fake.answered:
		case <-time.After(5 * time.Second):
			t.Fatal("PBX answer did not reach the modem call")
		}
	}
	fake.mu.Lock()
	media := append([]*fakeMedia(nil), fake.media...)
	fake.mu.Unlock()
	for _, leg := range media {
		fake.stream <- phone.Event{Type: "call_ended", Call: phone.CallView{MediaID: leg.id}}
	}
	for range media {
		select {
		case <-pbx.byes:
		case <-time.After(5 * time.Second):
			t.Fatal("ending the modem calls did not BYE the PBX")
		}
	}
}
