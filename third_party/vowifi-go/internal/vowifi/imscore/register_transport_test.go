package imscore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	enginesim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/internal/vowifi/imsheaders"
)

func TestRegisterUsesConfiguredIMSNetworkTransport(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer registrar.Close()
	requestSeen := make(chan string, 1)
	go serveRegisterStatus(registrar, 200, requestSeen)

	svc, err := New(&IMSConfig{
		DeviceID: "dev-1", IMEI: "356938035643809", IMSI: "310260123456789", IMPI: "310260123456789@ims.example",
		IMPU: "sip:310260123456789@ims.example", Domain: "ims.example",
		LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp", Registrar: registrar.LocalAddr().String(),
		IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)), AKAProvider: stubAKAProvider{},
		EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !svc.IsRegistered() {
		t.Fatal("service did not enter registered state")
	}
	select {
	case request := <-requestSeen:
		if !strings.HasPrefix(request, "REGISTER sip:ims.example SIP/2.0") {
			t.Fatalf("unexpected request: %q", request)
		}
		if strings.Contains(request, "@ims.example@") {
			t.Fatalf("REGISTER contains a duplicated identity domain: %q", request)
		}
		contact := sipHeaderValue(request, "Contact")
		if !strings.Contains(request, "From: <sip:310260123456789@ims.example>") ||
			!strings.Contains(contact, "@127.0.0.1:") || strings.Contains(contact, "sip:310260123456789@") {
			t.Fatalf("REGISTER identity URIs are invalid: %q", request)
		}
		if !strings.Contains(sipHeaderValue(request, "Contact"), `+sip.instance="<urn:gsma:imei:35693803-564380-9>"`) {
			t.Fatalf("REGISTER Contact omitted the IMEI instance URN: %q", sipHeaderValue(request, "Contact"))
		}
		authorization := sipHeaderValue(request, "Authorization")
		for _, field := range []string{`username="310260123456789@ims.example"`, `realm="ims.example"`, `nonce=""`, `uri="sip:ims.example"`, `response=""`} {
			if !strings.Contains(authorization, field) {
				t.Fatalf("initial Authorization omitted %s: %q", field, authorization)
			}
		}
		if got := sipHeaderValue(request, "P-Access-Network-Info"); got != `IEEE-802.11; i-wlan-node-id="dec378667018"` {
			t.Fatalf("REGISTER P-Access-Network-Info = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("registrar did not receive REGISTER")
	}
}

func TestRegisterAutoStartsWithUDP(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer registrar.Close()
	requestSeen := make(chan string, 1)
	go serveRegisterStatus(registrar, 200, requestSeen)

	svc, err := New(registerTransportTestConfig("auto", registrar.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	request := <-requestSeen
	if !strings.HasPrefix(sipHeaderValue(request, "Via"), "SIP/2.0/UDP ") ||
		!strings.Contains(sipHeaderValue(request, "Contact"), ";transport=udp>") {
		t.Fatalf("auto REGISTER did not start with UDP: %q", request)
	}
}

func TestRegisterTCPFallsBackToUDPWhenTCPConnectFails(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer registrar.Close()
	requestSeen := make(chan string, 1)
	go serveRegisterStatus(registrar, 200, requestSeen)

	svc, err := New(registerTransportTestConfig("tcp", registrar.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	request := <-requestSeen
	if !strings.HasPrefix(sipHeaderValue(request, "Via"), "SIP/2.0/UDP ") ||
		!strings.Contains(sipHeaderValue(request, "Contact"), ";transport=udp>") {
		t.Fatalf("tcp REGISTER did not fall back to UDP: %q", request)
	}
}

func TestRegisterTCPRetriesUDPAfter503BeforeAuthentication(t *testing.T) {
	tcpRegistrar, udpRegistrar := listenRegisterTransports(t)
	defer tcpRegistrar.Close()
	defer udpRegistrar.Close()
	tcpSeen := make(chan string, 1)
	udpSeen := make(chan string, 1)
	tcpResult := make(chan error, 1)
	go serveTCPRegisterStatusCode(tcpRegistrar, 503, tcpSeen, tcpResult)
	go serveRegisterStatus(udpRegistrar, 200, udpSeen)

	svc, err := New(registerTransportTestConfig("tcp", tcpRegistrar.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if <-tcpSeen == "" || <-udpSeen == "" {
		t.Fatal("REGISTER did not traverse TCP then UDP")
	}
	if err := <-tcpResult; err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticated503DoesNotRetryTheOtherTransport(t *testing.T) {
	err := registrationResponseError(&sipResponse{StatusCode: 503, Reason: "Service Unavailable"}, true)
	if shouldRetryNextRegisterTransport(registerAttemptResult{statusCode: 503}, err) {
		t.Fatal("a 503 after Digest/sec-agree retried UDP/TCP")
	}
}

func TestInitial503RetriesTheOtherTransport(t *testing.T) {
	err := registrationResponseError(&sipResponse{StatusCode: 503, Reason: "Service Unavailable"}, false)
	if !shouldRetryNextRegisterTransport(registerAttemptResult{statusCode: 503}, err) {
		t.Fatal("a 503 before authentication did not retry the other transport")
	}
}

func TestRegisterDeadlineIsRecoverableWithoutRetryingExpiredContext(t *testing.T) {
	if isRegisterOperationCanceled(context.DeadlineExceeded) {
		t.Fatal("REGISTER deadline was treated as caller cancellation")
	}
	if !isRegisterOperationCanceled(context.Canceled) {
		t.Fatal("caller cancellation was not recognized")
	}
	if shouldRetryNextRegisterTransport(registerAttemptResult{}, context.DeadlineExceeded) {
		t.Fatal("REGISTER retried another transport with an expired context")
	}
}

func TestReplaceRegisterTransportPastEndLeavesCurrentOpen(t *testing.T) {
	service := &Service{cfg: &IMSConfig{LocalIP: net.IPv4(127, 0, 0, 1), Transport: "tcp"}}
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	service.mu.Lock()
	service.registrationTCP = client
	service.registrationTransport = "tcp"
	service.mu.Unlock()

	_, err := service.replaceInitialRegistrationTransport(context.Background(), []string{"tcp"}, 1)
	if err == nil {
		t.Fatal("expected no further REGISTER transport")
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Fatalf("wrapped a nil transport error: %v", err)
	}
	service.mu.RLock()
	still := service.registrationTCP
	service.mu.RUnlock()
	if still != client {
		t.Fatal("closed the working REGISTER flow before a fallback existed")
	}
}

func TestFailedPortSRecoveryKeepsRegistrationWhenFlowIntact(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	service.mu.Lock()
	service.registrationTransport = "tcp"
	service.registrationTCP = client
	service.regSession.expires = time.Hour
	service.lastRegisterOKAt = time.Now().Add(-time.Minute)
	service.nextRegister = time.Now()
	service.mu.Unlock()
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)

	err := registrationResponseError(&sipResponse{StatusCode: 503, Reason: "Service Unavailable"}, true)
	if !service.keepRegistrationAfterFailedRefresh(true, err) {
		t.Fatal("a 503 refresh with the flow still up tore the binding down")
	}
	if !service.restoreRegistrationAfterFailedRefresh(err) {
		t.Fatal("working registration flow was not preserved")
	}
	if service.RegState() != regRegistered {
		t.Fatalf("registration state = %s", service.RegState())
	}
	if service.portSOnDemandObserved.Load() {
		t.Fatal("a rejected recovery was mistaken for an on-demand reconnect")
	}
	if !service.portSReconnectWaiting.Load() {
		t.Fatal("a rejected recovery stopped waiting for the peer")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); !waiting {
		t.Fatal("a rejected recovery did not enter RFC 5626 backoff")
	}
	if service.reRegisterPending.Load() {
		t.Fatal("keeping the binding still scheduled another REGISTER")
	}
	service.mu.RLock()
	next := service.nextRegister
	service.mu.RUnlock()
	if !next.After(time.Now().Add(30 * time.Second)) {
		t.Fatalf("next REGISTER = %s, want the original refresh", next)
	}
}

func TestFailedRefreshCannotRestoreAClosedRegistrationFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	service.mu.Lock()
	service.registrationTransport = "tcp"
	service.registrationTCP = client
	service.mu.Unlock()
	err := registrationResponseError(&sipResponse{StatusCode: 503}, true)
	if !service.keepRegistrationAfterFailedRefresh(true, err) {
		t.Fatal("test setup did not start with a preservable flow")
	}

	service.mu.Lock()
	service.registrationTransport = ""
	service.registrationTCP = nil
	service.mu.Unlock()
	if service.restoreRegistrationAfterFailedRefresh(err) {
		t.Fatal("closed registration flow was restored as registered")
	}
}

func TestRegisterDeadlineRequiresFlowConfirmationDuringAttempt(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	service.mu.Lock()
	service.registrationTransport = "tcp"
	service.registrationTCP = client
	service.lastRegisterAttemptAt = time.Now()
	service.mu.Unlock()
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)

	if service.keepRegistrationAfterFailedRefresh(true, context.DeadlineExceeded) {
		t.Fatal("connection pointer alone preserved a timed-out REGISTER flow")
	}
	service.mu.Lock()
	service.lastPingAt = service.lastRegisterAttemptAt.Add(time.Millisecond)
	service.mu.Unlock()
	service.lastPingOK.Store(true)
	if !service.keepRegistrationAfterFailedRefresh(true, context.DeadlineExceeded) {
		t.Fatal("keepalive-confirmed registration flow was not preserved")
	}
	if !service.restoreRegistrationAfterFailedRefresh(context.DeadlineExceeded) {
		t.Fatal("REGISTER deadline did not preserve the intact registration flow")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); !waiting {
		t.Fatal("REGISTER deadline did not enter RFC 5626 flow-recovery backoff")
	}
}

func TestRegisterTCPDoesNotRetryUDPAfter403(t *testing.T) {
	tcpRegistrar, udpRegistrar := listenRegisterTransports(t)
	defer tcpRegistrar.Close()
	defer udpRegistrar.Close()
	tcpResult := make(chan error, 1)
	go serveTCPRegisterStatusCode(tcpRegistrar, 403, nil, tcpResult)

	svc, err := New(registerTransportTestConfig("tcp", tcpRegistrar.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("Register error = %v", err)
	}
	if err := <-tcpResult; err != nil {
		t.Fatal(err)
	}
	assertNoUDPRegister(t, udpRegistrar)
}

func listenRegisterTransports(t *testing.T) (*net.TCPListener, *net.UDPConn) {
	t.Helper()
	tcpRegistrar, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := tcpRegistrar.Addr().(*net.TCPAddr).Port
	udpRegistrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		tcpRegistrar.Close()
		t.Fatal(err)
	}
	return tcpRegistrar, udpRegistrar
}

func serveTCPRegisterStatusCode(listener *net.TCPListener, status int, seen chan<- string, result chan<- error) {
	conn, err := listener.AcceptTCP()
	if err != nil {
		result <- err
		return
	}
	defer conn.Close()
	request, err := readSIPStreamMessage(bufio.NewReader(conn))
	if err == nil {
		if seen != nil {
			seen <- request
		}
		_, err = conn.Write([]byte(registerWireResponse(request, status, "")))
	}
	result <- err
}

func assertNoUDPRegister(t *testing.T, registrar *net.UDPConn) {
	t.Helper()
	if err := registrar.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, _, err := registrar.ReadFromUDP(buffer); err == nil {
		t.Fatal("unexpected REGISTER retry over UDP")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("UDP read error = %v", err)
	}
}

func registerTransportTestConfig(transport, registrar string) *IMSConfig {
	return &IMSConfig{
		DeviceID: "dev-auto", IMEI: "356938035643809", IMSI: "310260123456789",
		IMPI: "310260123456789@ims.example", IMPU: "sip:310260123456789@ims.example",
		Domain: "ims.example", LocalIP: net.IPv4(127, 0, 0, 1), Transport: transport,
		Registrar: registrar, IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)),
		AKAProvider: stubAKAProvider{}, EnableIPSec3GPP: disabledBoolPointer(),
		RegisterTemplate: IMSRegisterTemplate{
			AccessType: "wlan1", ContactOrder: []string{"access_type"},
		},
	}
}

func serveTCPRegisterStatus(listener *net.TCPListener, seen chan<- string, result chan<- error) {
	conn, err := listener.AcceptTCP()
	if err != nil {
		result <- err
		return
	}
	defer conn.Close()
	request, err := readSIPStreamMessage(bufio.NewReader(conn))
	if err != nil {
		result <- err
		return
	}
	seen <- request
	_, err = conn.Write([]byte(registerWireResponse(request, 200, "")))
	result <- err
}

func TestRegisterContactUsesRecoveredCarrierTemplate(t *testing.T) {
	const icsi = "urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel," +
		"urn%3Aurn-7%3A3gpp-service.ims.icsi.oma.cpm.msg," +
		"urn%3Aurn-7%3A3gpp-service.ims.icsi.oma.cpm.sms"
	config := &IMSConfig{
		IMEI: "356938035643809", IMSI: "234102356143376", Transport: "udp",
		RegisterTemplate: IMSRegisterTemplate{
			AccessType: "wlan1", ICSIRef: icsi,
			ContactOrder: []string{
				"access_type", "sip_instance", "audio", "smsip", "smsip_msisdn_less", "icsi_ref",
				"mid_call", "srvcc_alerting", "ps2cs_srvcc_orig_pre_alerting",
			},
		},
	}
	got := registerContact(imsheaders.ContactOptions{
		ContactID: "234102356143376", LocalAddr: "192.0.2.10",
		LocalPortC: 5060, LocalPortS: 5060,
		AccessType:        config.RegisterTemplate.AccessType,
		ContactParamOrder: config.RegisterTemplate.ContactOrder,
		SIPInstance:       config.IMEI, IcsiRef: config.RegisterTemplate.ICSIRef,
	}, "udp", 3600)
	want := `<sip:234102356143376@192.0.2.10:5060;transport=udp>` +
		`;+g.3gpp.accesstype="wlan1"` +
		`;+sip.instance="<urn:gsma:imei:35693803-564380-9>"` +
		`;audio;+g.3gpp.smsip;+g.3gpp.smsip-msisdn-less` +
		`;+g.3gpp.icsi-ref="` + icsi + `"` +
		`;+g.3gpp.mid-call;+g.3gpp.srvcc-alerting` +
		`;+g.3gpp.ps2cs-srvcc-orig-pre-alerting`
	if got != want {
		t.Fatalf("REGISTER Contact = %q\nwant             = %q", got, want)
	}
}

func TestBuildRegisterAIS52003ContactIsSingleMMTel(t *testing.T) {
	const multiICSI = "urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel," +
		"urn%3Aurn-7%3A3gpp-service.ims.icsi.sms," +
		"urn%3Aurn-7%3A3gpp-service.ims.icsi.oma.cpm.msg," +
		"urn%3Aurn-7%3A3gpp-service.ims.icsi.oma.cpm.sms"
	template := IMSRegisterTemplate{
		AccessType: "wlan1", ICSIRef: multiICSI,
		ContactOrder: []string{
			"access_type", "sip_instance", "audio", "smsip", "smsip_msisdn_less", "icsi_ref",
			"mid_call", "srvcc_alerting", "ps2cs_srvcc_orig_pre_alerting",
		},
	}
	config := &IMSConfig{
		CarrierPresetID: aisTH52003PresetID,
		IMEI:            "356714117697975", IMSI: "520030393351967",
		IMPI:    "520030393351967@ims.mnc003.mcc520.3gppnetwork.org",
		IMPU:    "sip:520030393351967@ims.mnc003.mcc520.3gppnetwork.org",
		Domain:  "ims.mnc003.mcc520.3gppnetwork.org",
		LocalIP: net.IPv4(192, 0, 2, 10), LocalPort: 5060, Transport: "tcp",
		RegisterTemplate: template,
	}
	service := &Service{cfg: config}
	request := service.buildRegister(&registerSession{callID: "call-1", fromTag: "tag-1", cseq: 1}, "")
	contact := sipHeaderValue(request, "Contact")
	want := `<sip:520030393351967@192.0.2.10:5060;transport=tcp>` +
		`;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"` +
		`;+g.3gpp.mid-call;+g.3gpp.ps2cs-srvcc-orig-pre-alerting;+g.3gpp.smsip;+g.3gpp.srvcc-alerting` +
		`;+sip.instance="<urn:gsma:imei:35671411-769797-5>"`
	if contact != want {
		t.Fatalf("REGISTER Contact = %q\nwant             = %q", contact, want)
	}
	if got := sipHeaderValue(request, "Supported"); got != "100rel, path, replaces" {
		t.Fatalf("AIS REGISTER Supported = %q", got)
	}
	for _, absent := range []string{"accesstype", "smsip-msisdn-less", ";audio", "reg-id", "cpm"} {
		if strings.Contains(strings.ToLower(contact), absent) {
			t.Fatalf("REGISTER Contact still has %s: %q", absent, contact)
		}
	}
	if got := sipHeaderValue(request, "Feature-Caps"); got != registerFeatureCapsHeader {
		t.Fatalf("Feature-Caps = %q", got)
	}
	other := &Service{cfg: &IMSConfig{
		CarrierPresetID: "ais_th_52001", IMEI: config.IMEI, IMSI: config.IMSI,
		IMPI: config.IMPI, IMPU: config.IMPU, Domain: config.Domain,
		LocalIP: config.LocalIP, LocalPort: config.LocalPort, Transport: config.Transport,
		RegisterTemplate: template,
	}}
	kept := sipHeaderValue(other.buildRegister(&registerSession{callID: "call-2", fromTag: "tag-2", cseq: 1}, ""), "Contact")
	if !strings.Contains(kept, `+g.3gpp.accesstype="wlan1"`) || !strings.Contains(kept, "cpm.sms") {
		t.Fatalf("non-AIS REGISTER Contact = %q", kept)
	}
}

func TestBuildRegisterUsesRecoveredTemplateHeaders(t *testing.T) {
	const allow = "OPTIONS, REGISTER, SUBSCRIBE, NOTIFY, PUBLISH, INVITE, ACK, BYE, CANCEL, UPDATE, PRACK, REFER, INFO, MESSAGE"
	config := &IMSConfig{
		IMSI: "234102356143376", IMPI: "234102356143376@ims.example",
		IMPU: "sip:234102356143376@ims.example", Domain: "ims.example",
		LocalIP: net.IPv4(192, 0, 2, 10), Transport: "udp",
		UserAgent:             "iOS/18.2.1 iPhone (iPhone15,4)",
		CellularNetworkInfo:   "3GPP-E-UTRAN-TDD;utran-cell-id-3gpp=2340100123456789;cell-info-age=1000",
		PAccessNetworkCountry: "GB",
		RegisterTemplate: IMSRegisterTemplate{
			Expires: 600000 * time.Second, SupportedHeader: "path,sec-agree",
			AllowHeader: allow, ContactMode: "android_default", IncludePANIAuthenticated: true,
		},
	}
	service := &Service{cfg: config}
	request := service.buildRegister(&registerSession{callID: "call-1", fromTag: "tag-1", cseq: 1}, "")
	if got := sipHeaderValue(request, "Expires"); got != "600000" {
		t.Fatalf("Expires = %q", got)
	}
	if got := sipHeaderValue(request, "Supported"); got != "path, sec-agree" {
		t.Fatalf("Supported = %q", got)
	}
	if got := sipHeaderValue(request, "Allow"); got != allow {
		t.Fatalf("Allow = %q", got)
	}
	if got := sipHeaderValue(request, "User-Agent"); got != "iOS/18.2.1 iPhone (iPhone15,4)" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := sipHeaderValue(request, "P-Access-Network-Info"); got != "" {
		t.Fatalf("initial P-Access-Network-Info = %q", got)
	}
	if got := sipHeaderValue(request, "Cellular-Network-Info"); !strings.HasPrefix(got, "3GPP-E-UTRAN-TDD;") {
		t.Fatalf("Cellular-Network-Info = %q", got)
	}
	wantAuthorization := `Digest uri="sip:ims.example",username="234102356143376@ims.example",response="",realm="ims.example",nonce=""`
	if got := sipHeaderValue(request, "Authorization"); got != wantAuthorization {
		t.Fatalf("initial Authorization = %q", got)
	}
	authenticated := service.buildRegister(
		&registerSession{callID: "call-1", fromTag: "tag-1", cseq: 2}, "Digest response",
	)
	if got := sipHeaderValue(authenticated, "P-Access-Network-Info"); !strings.HasSuffix(got, ";country=GB") {
		t.Fatalf("authenticated P-Access-Network-Info = %q", got)
	}
}

func TestRegistrationRefreshesBeforeExpiryAndReportsFailure(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer registrar.Close()
	requests := make(chan string, 2)
	go serveRegistrationSequence(registrar, requests, []int{200, 403})

	svc, err := New(&IMSConfig{
		DeviceID: "dev-refresh", IMSI: "310260123456789", IMPI: "310260123456789@ims.example",
		IMPU: "sip:310260123456789@ims.example", Domain: "ims.example",
		LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp", Registrar: registrar.LocalAddr().String(),
		IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)), AKAProvider: stubAKAProvider{},
		EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}

	seen := make([]string, 0, 2)
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case request := <-requests:
			seen = append(seen, request)
		case <-ctx.Done():
			t.Fatalf("received only %d REGISTER requests before timeout", attempt)
		}
	}
	if sipHeaderValue(seen[0], "Call-ID") != sipHeaderValue(seen[1], "Call-ID") {
		t.Fatal("registration refresh changed Call-ID")
	}
	if sipHeaderValue(seen[0], "CSeq") != "2 REGISTER" || sipHeaderValue(seen[1], "CSeq") != "3 REGISTER" {
		t.Fatalf("refresh CSeq values = %q, %q", sipHeaderValue(seen[0], "CSeq"), sipHeaderValue(seen[1], "CSeq"))
	}
	if sipHeaderValue(seen[1], "Expires") != "3600" {
		t.Fatalf("refresh Expires = %q", sipHeaderValue(seen[1], "Expires"))
	}
	refreshContact := sipHeaderValue(seen[1], "Contact")
	if !strings.Contains(refreshContact, "+sip.instance=") {
		t.Fatalf("refresh Contact omitted sip.instance: %q", refreshContact)
	}
	select {
	case err := <-svc.RegistrationErrors():
		if err == nil || !strings.Contains(err.Error(), "status 403") {
			t.Fatalf("refresh error = %v, want status 403", err)
		}
	case <-ctx.Done():
		t.Fatal("registration refresh failure was not reported")
	}
	if svc.IsRegistered() || svc.RegState() != regFailed {
		t.Fatalf("registration state after refresh failure = %q", svc.RegState())
	}
}

func TestRegistrationExpiresPrefersExpiresHeader(t *testing.T) {
	response := &sipResponse{Headers: map[string]string{
		"Contact": "<sip:user@10.0.0.2>;expires=120", "Expires": "3600",
	}}
	if got := registrationExpires(response, time.Hour); got != time.Hour {
		t.Fatalf("registrationExpires = %s, want 1h", got)
	}
}

type sqnSyncAKAProvider struct {
	calls int
	auts  []byte
}

func (p *sqnSyncAKAProvider) CalculateAKA(_, _ []byte) (AKAResult, error) {
	p.calls++
	if p.calls == 1 {
		return AKAResult{AUTS: append([]byte(nil), p.auts...)}, enginesim.ErrSyncFailure
	}
	return AKAResult{
		RES: bytes.Repeat([]byte{0x33}, 16),
		CK:  bytes.Repeat([]byte{0x11}, 16),
		IK:  bytes.Repeat([]byte{0x22}, 16),
	}, nil
}

func TestRegisterRecoversFromAKASQNSynchronizationFailure(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer registrar.Close()
	auts := bytes.Repeat([]byte{0xa5}, akaAUTSLength)
	serverResult := make(chan error, 1)
	go serveSQNSyncRegistrar(registrar, auts, serverResult)
	provider := &sqnSyncAKAProvider{auts: auts}
	svc, err := New(&IMSConfig{
		DeviceID: "dev-sync", IMSI: "310260123456789", IMPI: "310260123456789@ims.example",
		Domain: "ims.example", LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp",
		Registrar: registrar.LocalAddr().String(), IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)),
		AKAProvider: provider, EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if provider.calls != 2 || !svc.IsRegistered() {
		t.Fatalf("AKA calls=%d registered=%t", provider.calls, svc.IsRegistered())
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestProcessAKAChallengeRejectsInvalidSynchronizationToken(t *testing.T) {
	challenge := digestChallengeForTest(0x11, 0x22)
	provider := &sqnSyncAKAProvider{auts: bytes.Repeat([]byte{0xa5}, akaAUTSLength-1)}
	if _, _, err := ProcessAKAChallengeWithResult(challenge, provider, "user", "REGISTER", "sip:ims.example"); err == nil || !strings.Contains(err.Error(), "AUTS length") {
		t.Fatalf("ProcessAKAChallengeWithResult error = %v", err)
	}
}

func TestRegisterLimitsAKAChallenges(t *testing.T) {
	svc, err := New(&IMSConfig{
		DeviceID: "dev-limit", IMSI: "310260123456789", IMPI: "310260123456789@ims.example",
		Domain: "ims.example", AKAProvider: stubAKAProvider{}, EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()
	svc.transport.SetSendFn(func(request string) error {
		challenge := strings.TrimPrefix(strings.TrimSpace(digestChallengeHeader(0x11, 0x22)), "WWW-Authenticate: ")
		svc.transport.DeliverResponse(registerResponseForRequest(request, 401, map[string]string{
			"WWW-Authenticate": challenge,
		}))
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Register(ctx); err == nil || !strings.Contains(err.Error(), "AKA challenge limit 3 exceeded") {
		t.Fatalf("Register error = %v, want challenge limit", err)
	}
}

func TestReceiveResponseMatchesFullRegisterTransaction(t *testing.T) {
	transport := newSIPTransport()
	svc := &Service{transport: transport}
	session := &registerSession{callID: "call-1", cseq: 2, branch: "z9hG4bK-current"}
	transport.DeliverResponse(&sipResponse{StatusCode: 200, CallID: session.callID, CSeq: "1 REGISTER", Headers: map[string]string{
		"Via": "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-old",
	}})
	transport.DeliverResponse(&sipResponse{StatusCode: 403, CallID: session.callID, CSeq: "2 REGISTER", Headers: map[string]string{
		"Via": "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-wrong",
	}})
	transport.DeliverResponse(&sipResponse{StatusCode: 200, CallID: session.callID, CSeq: "2 REGISTER", Headers: map[string]string{
		"Via": "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bK-current",
	}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := svc.receiveResponse(ctx, session)
	if err != nil {
		t.Fatalf("receiveResponse: %v", err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("matched stale REGISTER response status %d", response.StatusCode)
	}
}

func TestReceiveResponseRejectsMalformedCurrentTransaction(t *testing.T) {
	transport := newSIPTransport()
	svc := &Service{transport: transport}
	session := &registerSession{callID: "call-1", cseq: 2, branch: "z9hG4bK-current"}
	transport.DeliverResponse(&sipResponse{StatusCode: 200, CallID: session.callID})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := svc.receiveResponse(ctx, session); err == nil || !strings.Contains(err.Error(), "invalid REGISTER response CSeq") {
		t.Fatalf("receiveResponse error = %v, want malformed CSeq", err)
	}
}

func TestRegisterPropagatesRegistrarRejection(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer registrar.Close()
	go serveRegisterStatus(registrar, 403, nil)
	svc, err := New(&IMSConfig{
		DeviceID: "dev-1", IMSI: "310260123456789", IMPI: "310260123456789@ims.example",
		Domain: "ims.example", LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp",
		Registrar: registrar.LocalAddr().String(), IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)),
		EnableIPSec3GPP: enabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Register(ctx); err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("Register error = %v, want 403", err)
	}
	if svc.IsRegistered() || svc.RegState() != regFailed {
		t.Fatalf("registration state = %q", svc.RegState())
	}
}

func TestRegistrationResponseErrorIncludesRegistrarDiagnostic(t *testing.T) {
	err := registrationResponseError(&sipResponse{
		StatusCode: 400,
		Reason:     "Bad Request",
		Headers:    map[string]string{"Warning": `399 pcscf.example "Malformed Contact"`},
	}, true)
	if got := err.Error(); !strings.Contains(got, "authenticated REGISTER") || !strings.Contains(got, "400 (Bad Request") || !strings.Contains(got, "Malformed Contact") {
		t.Fatalf("registrationResponseError = %q", got)
	}
}

func serveRegisterStatus(conn *net.UDPConn, status int, seen chan<- string) {
	serveRegisterStatusWithHeaders(conn, status, "", seen)
}

func serveRegisterStatusWithHeaders(conn *net.UDPConn, status int, extraHeaders string, seen chan<- string) {
	buffer := make([]byte, 64*1024)
	n, remote, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return
	}
	request := string(buffer[:n])
	if seen != nil {
		seen <- request
	}
	response := registerWireResponse(request, status, extraHeaders)
	_, _ = conn.WriteToUDP([]byte(response), remote)
}

func serveRegistrationSequence(conn *net.UDPConn, seen chan<- string, statuses []int) {
	buffer := make([]byte, 64*1024)
	for _, status := range statuses {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		request := string(buffer[:n])
		seen <- request
		response := registerWireResponse(request, status, "Expires: 1\r\n")
		_, _ = conn.WriteToUDP([]byte(response), remote)
	}
}

func registerWireResponse(request string, status int, extraHeaders string) string {
	return fmt.Sprintf(
		"SIP/2.0 %d Test\r\nVia: %s\r\nCall-ID: %s\r\nCSeq: %s\r\n%sContent-Length: 0\r\n\r\n",
		status,
		sipHeaderValue(request, "Via"),
		sipHeaderValue(request, "Call-ID"),
		sipHeaderValue(request, "CSeq"),
		extraHeaders,
	)
}

func serveSQNSyncRegistrar(conn *net.UDPConn, auts []byte, result chan<- error) {
	buffer := make([]byte, 64*1024)
	for attempt := 0; attempt < 3; attempt++ {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			result <- err
			return
		}
		request := string(buffer[:n])
		authorization := sipHeaderValue(request, "Authorization")
		if err := validateSQNSyncAuthorization(attempt, authorization, auts); err != nil {
			result <- err
			return
		}
		status, headers := 401, digestChallengeHeader(byte(0x11+attempt), byte(0x22+attempt))
		if attempt == 2 {
			status, headers = 200, ""
		}
		response := registerWireResponse(request, status, headers)
		if _, err := conn.WriteToUDP([]byte(response), remote); err != nil {
			result <- err
			return
		}
	}
	result <- nil
}

func validateSQNSyncAuthorization(attempt int, authorization string, auts []byte) error {
	switch attempt {
	case 0:
		for _, field := range []string{`username="310260123456789@ims.example"`, `nonce=""`, `response=""`} {
			if !strings.Contains(authorization, field) {
				return fmt.Errorf("initial REGISTER Authorization = %q, missing %s", authorization, field)
			}
		}
	case 1:
		want := `auts="` + base64.StdEncoding.EncodeToString(auts) + `"`
		if !strings.Contains(authorization, want) {
			return fmt.Errorf("synchronization REGISTER Authorization = %q, missing %s", authorization, want)
		}
	case 2:
		if authorization == "" || strings.Contains(authorization, "auts=") {
			return fmt.Errorf("fresh challenge Authorization = %q", authorization)
		}
	}
	return nil
}

func digestChallengeHeader(randByte, autnByte byte) string {
	nonce := base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte{randByte}, 16), bytes.Repeat([]byte{autnByte}, 16)...))
	return fmt.Sprintf("WWW-Authenticate: Digest realm=\"ims.example\", nonce=\"%s\", algorithm=AKAv1-MD5, qop=\"auth\"\r\n", nonce)
}

func digestChallengeForTest(randByte, autnByte byte) *DigestChallenge {
	nonce := base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte{randByte}, 16), bytes.Repeat([]byte{autnByte}, 16)...))
	challenge, _ := ParseDigestChallenge(fmt.Sprintf(`Digest realm="ims.example", nonce="%s", algorithm=AKAv1-MD5, qop="auth"`, nonce))
	return challenge
}

func sipHeaderValue(message, name string) string {
	for _, line := range strings.Split(message, "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func waitForPortSCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the port-s flow condition")
		}
		time.Sleep(time.Millisecond)
	}
}

// port-s carries every MT SMS and nothing else reports it gone, so a closure
// the P-CSCF does not replace within the grace has to fall back to RFC 5626
// flow recovery. Measured on Vodafone UK: 4m with an MT SMS queued and 12m35s
// idle both passed without the peer reopening the flow on its own.
func TestProtectedServerPushClosureRecoversTheFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectGrace = time.Millisecond
	service.mu.Lock()
	service.registrationRefreshAt = time.Now().Add(time.Hour)
	service.mu.Unlock()

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	service.untrackProtectedConnection(client)
	service.handleProtectedServerPushClosed()
	waitForPortSCondition(t, func() bool { return service.reRegisterPending.Load() })
	if !service.portSRecoveryPending.Load() {
		t.Fatal("recovery REGISTER was not marked for outcome tracking")
	}
	if service.RegState() != regRegistered {
		t.Fatalf("outbound registration dropped: %s", service.RegState())
	}
}

// 2degrees reopens port-s when downlink traffic arrives even after rejecting
// the proactive REGISTER. That observed reconnect, not the 503 by itself,
// allows future clean EOF closures to wait for the peer, not transport errors.
func TestPeerReconnectAfterRejectedRecoveryEnablesOnDemandMode(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectGrace = time.Millisecond
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)
	service.completePortSRecovery(
		registrationResponseError(&sipResponse{StatusCode: 503, Reason: "Service Unavailable"}, true),
		true,
	)
	if service.portSOnDemandObserved.Load() {
		t.Fatal("503 alone enabled on-demand mode")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); !waiting {
		t.Fatal("503 did not back off repeated recovery for this binding")
	}

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	if !service.portSOnDemandObserved.Load() {
		t.Fatal("peer reconnect after failed REGISTER did not enable on-demand mode")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); waiting {
		t.Fatal("observed on-demand reconnect kept recovery backoff")
	}
	service.recordPortSOpened(client, time.Now())
	service.recordPortSClosed(client, io.EOF, time.Now())
	service.untrackProtectedConnection(client)
	service.handleProtectedServerPushClosed()
	time.Sleep(20 * time.Millisecond)
	if service.reRegisterPending.Load() {
		t.Fatal("proven on-demand flow scheduled re-REGISTER")
	}
}

func TestSuccessfulRecoveryDoesNotEnableOnDemandMode(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectGrace = time.Hour
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)
	service.recordPortSRecoveryFailure(context.DeadlineExceeded, time.Now())
	service.completePortSRecovery(nil, true)
	if _, waiting := service.portSRecoveryDeadline(time.Now()); waiting {
		t.Fatal("successful REGISTER kept recovery backoff")
	}

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	if service.portSOnDemandObserved.Load() {
		t.Fatal("port-s opened after successful REGISTER enabled on-demand mode")
	}
	if service.portSRecoveryAwaitingFlow.Load() || service.portSReconnectWaiting.Load() {
		t.Fatal("reopened port-s kept successful recovery pending")
	}
}

func TestAbortedRecoveryClearsPendingState(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)
	service.recordPortSRecoveryFailure(context.DeadlineExceeded, time.Now())

	service.completePortSRecovery(context.Canceled, false)

	if service.portSReconnectWaiting.Load() || service.portSRecoveryPending.Load() ||
		service.portSRecoveryAwaitingFlow.Load() || service.portSOnDemandObserved.Load() {
		t.Fatal("aborted recovery kept stale port-s state")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); waiting {
		t.Fatal("aborted recovery kept stale port-s backoff")
	}
}

func TestConnectionDuringFailedRecoveryDoesNotEnableOnDemandMode(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	service.completePortSRecovery(
		registrationResponseError(&sipResponse{StatusCode: 503, Reason: "Service Unavailable"}, true),
		true,
	)
	if service.portSOnDemandObserved.Load() {
		t.Fatal("connection opened during failed REGISTER enabled on-demand mode")
	}
	if service.portSReconnectWaiting.Load() {
		t.Fatal("completed recovery kept a stale reconnect wait")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); !waiting {
		t.Fatal("failed recovery did not back off another REGISTER for this binding")
	}

	service.untrackProtectedConnection(client)
	service.handleProtectedServerPushClosed()
	time.Sleep(20 * time.Millisecond)
	if service.reRegisterPending.Load() {
		t.Fatal("another port-s closure repeated a rejected REGISTER recovery")
	}
}

func TestPortSRecoveryBacksOffAllRecoverableFailures(t *testing.T) {
	for name, recoveryErr := range map[string]error{
		"transaction timeout":       sip.ErrTransactionTimeout,
		"REGISTER context deadline": context.DeadlineExceeded,
		"TCP EOF":                   io.EOF,
		"SIP 502": registrationResponseError(
			&sipResponse{StatusCode: 502, Reason: "Bad Gateway"}, true,
		),
	} {
		t.Run(name, func(t *testing.T) {
			service := newProtectedKeepaliveTestService(t)
			service.portSReconnectGrace = time.Millisecond
			service.portSReconnectWaiting.Store(true)
			service.portSRecoveryPending.Store(true)

			service.completePortSRecovery(recoveryErr, true)
			if _, waiting := service.portSRecoveryDeadline(time.Now()); !waiting {
				t.Fatal("recoverable failure did not enter RFC 5626 backoff")
			}

			service.handleProtectedServerPushClosed()
			time.Sleep(20 * time.Millisecond)
			if service.reRegisterPending.Load() {
				t.Fatal("port-s closure bypassed the active recovery backoff")
			}
		})
	}
}

func TestConnectionDuringPeriodicRegisterDoesNotEnableOnDemandMode(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectWaiting.Store(true)
	service.mu.Lock()
	service.regState = regRegistering
	service.mu.Unlock()

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	if service.portSOnDemandObserved.Load() {
		t.Fatal("connection opened during periodic REGISTER enabled on-demand mode")
	}
}

func TestPCSCFFailoverClearsOnDemandPortSKnowledge(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectWaiting.Store(true)
	service.portSRecoveryPending.Store(true)
	service.recordPortSRecoveryFailure(context.DeadlineExceeded, time.Now())
	service.portSOnDemandObserved.Store(true)

	service.resetRegistrationTransportForRegistrarRetry()

	if service.portSReconnectWaiting.Load() || service.portSRecoveryPending.Load() ||
		service.portSRecoveryAwaitingFlow.Load() || service.portSOnDemandObserved.Load() {
		t.Fatal("P-CSCF failover kept port-s recovery knowledge")
	}
	if _, waiting := service.portSRecoveryDeadline(time.Now()); waiting {
		t.Fatal("P-CSCF failover kept port-s recovery backoff")
	}
}

// port-s and the registration stream share tcp_socket_reads, so the push flow
// needs its own read clock for a reset to be told apart from plain idling.
func TestPortSReadClockTracksOnlyThePushFlow(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)

	service.resetPortSReadClock()
	if silence := service.portSSinceLastRead(); silence > time.Minute {
		t.Fatalf("since_last_read = %s, want a fresh clock", silence)
	}

	service.portSLastReadAt.Store(time.Now().Add(-10 * time.Minute).UnixNano())
	if silence := service.portSSinceLastRead(); silence < 9*time.Minute {
		t.Fatalf("since_last_read = %s, want the backdated age", silence)
	}

	// Registration-stream traffic must not refresh the push flow's clock.
	service.handleTCPTraffic()
	if silence := service.portSSinceLastRead(); silence < 9*time.Minute {
		t.Fatalf("since_last_read = %s, want registration traffic ignored", silence)
	}

	service.handlePortSTraffic()
	if silence := service.portSSinceLastRead(); silence > time.Minute {
		t.Fatalf("since_last_read after push inbound = %s, want the clock to advance", silence)
	}
}

func TestProtectedServerPushClosureKeepsOtherInboundFlows(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.mu.Lock()
	service.registrationRefreshAt = time.Now().Add(time.Hour)
	service.mu.Unlock()

	closed, closedPeer := net.Pipe()
	alive, alivePeer := net.Pipe()
	t.Cleanup(func() {
		_ = closed.Close()
		_ = closedPeer.Close()
		_ = alive.Close()
		_ = alivePeer.Close()
	})
	if !service.trackProtectedConnection(closed) || !service.trackProtectedConnection(alive) {
		t.Fatal("trackProtectedConnection")
	}
	service.untrackProtectedConnection(closed)
	service.handleProtectedServerPushClosed()
	if service.reRegisterPending.Load() {
		t.Fatal("still-open port-s flow scheduled re-REGISTER")
	}
}

func TestServeProtectedSIPConnectionRecoversTheFlowOnReset(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectGrace = time.Millisecond
	service.mu.Lock()
	service.registrationRefreshAt = time.Now().Add(time.Hour)
	service.mu.Unlock()

	client, server := net.Pipe()
	defer server.Close()
	if !service.trackProtectedConnection(client) {
		t.Fatal("trackProtectedConnection")
	}
	service.recordPortSOpened(client, time.Now())
	service.networkDone.Add(1)
	done := make(chan struct{})
	go func() {
		service.serveProtectedSIPConnection(client)
		close(done)
	}()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveProtectedSIPConnection did not return after reset")
	}
	waitForPortSCondition(t, func() bool { return service.reRegisterPending.Load() })
	if service.RegState() != regRegistered {
		t.Fatalf("outbound registration dropped: %s", service.RegState())
	}
}

func TestProtectedServerPushClosureSkipsReRegisterWhenReplaced(t *testing.T) {
	service := newProtectedKeepaliveTestService(t)
	service.portSReconnectGrace = time.Hour
	service.mu.Lock()
	service.registrationRefreshAt = time.Now().Add(time.Hour)
	service.mu.Unlock()

	closed, closedPeer := net.Pipe()
	alive, alivePeer := net.Pipe()
	t.Cleanup(func() {
		_ = closed.Close()
		_ = closedPeer.Close()
		_ = alive.Close()
		_ = alivePeer.Close()
	})
	if !service.trackProtectedConnection(closed) {
		t.Fatal("trackProtectedConnection")
	}
	service.untrackProtectedConnection(closed)
	service.handleProtectedServerPushClosed()
	if !service.trackProtectedConnection(alive) {
		t.Fatal("replacement trackProtectedConnection")
	}
	if service.reRegisterPending.Load() {
		t.Fatal("replaced port-s still scheduled re-REGISTER")
	}
}
