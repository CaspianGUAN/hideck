package imscore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	enginesim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/internal/vowifi/policy"
)

type busyAKAProvider struct{}

func (busyAKAProvider) CalculateAKA(_, _ []byte) (enginesim.AKAResult, error) {
	return enginesim.AKAResult{}, enginesim.ErrAPDUBusy
}

func TestRegisterTransportCandidatesMatchOriginalOrder(t *testing.T) {
	tests := map[string][]string{
		"tcp":     {"tcp", "udp"},
		"udp":     {"udp"},
		"auto":    {"udp", "tcp"},
		"":        {"udp", "tcp"},
		"unknown": {"udp", "tcp"},
	}
	for configured, want := range tests {
		if got := registerTransportCandidates(configured); !reflect.DeepEqual(got, want) {
			t.Errorf("registerTransportCandidates(%q) = %v, want %v", configured, got, want)
		}
	}
}

func TestRegistrationStatusTransitionsMatchOriginalFSM(t *testing.T) {
	service := &Service{}
	if !service.transitionRegStatus(registrationRegistered) {
		t.Fatal("Unregistered -> Registered rejected")
	}
	if service.transitionRegStatus(registrationUnregistered) {
		t.Fatal("Registered -> Unregistered accepted")
	}
	if !service.transitionRegStatus(registrationRejectedTemporary) ||
		!service.transitionRegStatus(registrationStopping) ||
		!service.transitionRegStatus(registrationStopped) {
		t.Fatal("valid shutdown transition rejected")
	}
	if service.transitionRegStatus(registrationRegistered) {
		t.Fatal("Stopped -> Registered accepted")
	}
}

func TestServiceIsRegisteredUsesOriginalFSMDuringRefresh(t *testing.T) {
	service := &Service{regState: regRegistering}
	service.regStatus.Store(registrationRegistered)
	if !service.IsRegistered() {
		t.Fatal("registered FSM state was hidden by compatibility refresh state")
	}
}

func TestRegistrationStatusText(t *testing.T) {
	want := []string{"Unregistered", "Registered", "RejectedTemporary", "RejectedPermanent", "Stopping", "Stopped"}
	for status, text := range want {
		if got := registrationStatusText(int32(status)); got != text {
			t.Errorf("registrationStatusText(%d) = %q, want %q", status, got, text)
		}
	}
	if got := registrationStatusText(99); got != "Unknown" {
		t.Fatalf("registrationStatusText(99) = %q", got)
	}
}

func TestSplitRegistrarCandidatesStableDeduplication(t *testing.T) {
	want := []string{"pcscf-a.example:5060", "pcscf-b.example:5060"}
	got := splitRegistrarCandidates(" pcscf-a.example:5060 ; ; pcscf-b.example:5060;pcscf-a.example:5060 ")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitRegistrarCandidates = %v, want %v", got, want)
	}
}

func TestSelectRegisterAttemptRegistrarNormalizesIndex(t *testing.T) {
	cfg := IMSConfig{Registrar: "pcscf-a.example:5060;pcscf-b.example:5060"}
	selected, candidates, index, err := selectRegisterAttemptRegistrar(cfg, nil, 9)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "pcscf-a.example:5060" || index != 0 || len(candidates) != 2 {
		t.Fatalf("selection = %q, %v, %d", selected, candidates, index)
	}
}

func TestRecoveredRegisterHelpers(t *testing.T) {
	if got := formatAORForSIP("", "user", "ims.example"); got != "sip:user@ims.example" {
		t.Fatalf("formatAORForSIP = %q", got)
	}
	if got := formatAORForSIP("TEL", "+44123", "ignored"); got != "tel:+44123" {
		t.Fatalf("formatAORForSIP tel = %q", got)
	}
	if got := formatHostForSIP("[2001:db8::1]"); got != "[2001:db8::1]" {
		t.Fatalf("formatHostForSIP = %q", got)
	}
	if got := pickAuthRealm("configured", " challenge ", "fallback"); got != "challenge" {
		t.Fatalf("pickAuthRealm = %q", got)
	}
	if got := securityClientMechanismCount("a,,b"); got != 3 {
		t.Fatalf("securityClientMechanismCount = %d", got)
	}
	if got := registerHeaderPortForTemplate(5070, 0, policy.IMSRegisterTemplate{}); got != 5070 {
		t.Fatalf("registerHeaderPortForTemplate = %d", got)
	}
}

func TestRegisterUsesCarrierFixedPANI(t *testing.T) {
	service, err := New(&IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		IMSRegisterTemplate: policy.IMSRegisterTemplate{ID: "fixed", FixedPANI: "3GPP-NR-FDD;fixed=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	if got := service.GetPAccessNetworkInfo(); got != "3GPP-NR-FDD;fixed=1" {
		t.Fatalf("GetPAccessNetworkInfo = %q", got)
	}
}

func TestParseRecoveredRegisterResponseFields(t *testing.T) {
	response := &sipResponse{Headers: map[string]string{
		"Expires": "3600", "Contact": "<sip:a@example>;expires=120",
		"Retry-After": "9", "Min-Expires": "600",
	}}
	if got, err := registrationExpires(response, "<sip:a@example>", 10*time.Second); err != nil || got != 120*time.Second {
		t.Fatalf("registrationExpires = %s, %v", got, err)
	}
	retryAfter, retryAfterSet, minExpires := parseRegisterRetryHintsFromResponse(response)
	if retryAfter != 9*time.Second || !retryAfterSet || minExpires != 600 {
		t.Fatalf("retry hints = %s, %t, %d", retryAfter, retryAfterSet, minExpires)
	}
	if got := parseRemoteIPFromPath("<sip:user@[2001:db8::10]:5060;lr>"); got != "2001:db8::10" {
		t.Fatalf("parseRemoteIPFromPath = %q", got)
	}
}

func TestRegisterRetryAfterZeroRemainsExplicit(t *testing.T) {
	retryAfter, retryAfterSet, _ := parseRegisterRetryHintsFromResponse(&sipResponse{
		Headers: map[string]string{"Retry-After": "0 (ready)"},
	})
	if retryAfter != 0 || !retryAfterSet {
		t.Fatalf("Retry-After zero = %s, present %t", retryAfter, retryAfterSet)
	}
	now := time.Unix(100, 0)
	outcome := decideRegisterFailureOutcome(now, registerAttemptResult{
		statusCode: 503, retryAfterSet: true,
	}, policy.DefaultIMSRegisterPolicy(), false)
	if outcome.reason != "retry_after" || outcome.nextRegister != now {
		t.Fatalf("Retry-After zero outcome = %+v", outcome)
	}
}

func TestExtractChallengePrefersWWWAuthenticate(t *testing.T) {
	service := &Service{}
	www := strings.TrimPrefix(strings.TrimSpace(digestChallengeHeader(0x11, 0x22)), "WWW-Authenticate: ")
	www = strings.Replace(www, `realm="ims.example"`, `realm="www"`, 1)
	proxy := strings.TrimPrefix(strings.TrimSpace(digestChallengeHeader(0x33, 0x44)), "WWW-Authenticate: ")
	proxy = strings.Replace(proxy, `realm="ims.example"`, `realm="proxy"`, 1)
	response := &sipResponse{StatusCode: 407, Headers: map[string]string{
		"WWW-Authenticate": www, "Proxy-Authenticate": proxy,
	}}
	challenge, err := service.extractChallenge(response, response.StatusCode)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Realm != "www" {
		t.Fatalf("challenge = %+v", challenge)
	}
}

func TestRecoveredRegisterFailurePolicy(t *testing.T) {
	registerPolicy := policy.DefaultIMSRegisterPolicy()
	if !isTemporaryRegisterSIPResponse(registerPolicy, 503) ||
		!isForbiddenRegisterSIPResponse(registerPolicy, 403) {
		t.Fatal("default REGISTER failure policy was not restored")
	}
	if got := temporaryRegisterRetryInterval(registerPolicy); got != time.Minute {
		t.Fatalf("temporaryRegisterRetryInterval = %s", got)
	}
	if got := normalizeRegisterPolicySource("external"); got != "default" {
		t.Fatalf("normalizeRegisterPolicySource = %q", got)
	}
	if got := effectiveRegisterPolicyID(policy.IMSRegisterTemplate{}, " carrier-source "); got != "carrier-source" {
		t.Fatalf("effectiveRegisterPolicyID = %q", got)
	}
	retryPolicy := defaultRegisterRetryPolicy(registerPolicy)
	if retryPolicy.maxAuthChallenges != maxAKAChallenges {
		t.Fatalf("maxAuthChallenges = %d, want %d", retryPolicy.maxAuthChallenges, maxAKAChallenges)
	}
	if !retryPolicy.ShouldRetryDefaultInitial(403, "", "") ||
		retryPolicy.ShouldRetryDefaultInitial(403, "warning", "") {
		t.Fatal("default initial retry decision differs from recovered policy")
	}
}

func TestRegisterRetriesRejectedInitialRequestWithFallbackTemplate(t *testing.T) {
	config := registerTransportTestConfig("udp", "127.0.0.1:5060")
	config.IMSRegisterTemplate = policy.DefaultIMSRegisterTemplate()
	service, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	requests := make(chan string, 2)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		status := 403
		if len(requests) == 2 {
			status = 200
		}
		service.transport.DeliverResponse(registerResponseForRequest(request, status, nil))
		return nil
	})
	if err := service.Register(context.Background()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	first, second := <-requests, <-requests
	if parseCSeq(sipHeaderValue(first, "CSeq")) != 2 || parseCSeq(sipHeaderValue(second, "CSeq")) != 3 {
		t.Fatalf("fallback CSeq sequence = %q, %q", sipHeaderValue(first, "CSeq"), sipHeaderValue(second, "CSeq"))
	}
	service.mu.RLock()
	template := service.regSession.template
	service.mu.RUnlock()
	if template.ID != "minimal_generic" || template.EnableInitialRejectFallback {
		t.Fatalf("fallback template = %+v", template)
	}
	if service.callID == "" || service.fromTag == "" || service.cseq.Load() != 3 ||
		service.lastSIPCode.Load() != 200 {
		t.Fatalf("registration runtime = call-id %q, tag %q, cseq %d, code %d",
			service.callID, service.fromTag, service.cseq.Load(), service.lastSIPCode.Load())
	}
}

func TestDecideRegisterFailureOutcome(t *testing.T) {
	now := time.Unix(100, 0)
	registerPolicy := policy.DefaultIMSRegisterPolicy()
	temporary := decideRegisterFailureOutcome(now, registerAttemptResult{statusCode: 503}, registerPolicy, false)
	if temporary.kind != registrationRejectedTemporary || temporary.reason != "temporary" ||
		temporary.nextRegister != now.Add(time.Minute) {
		t.Fatalf("temporary outcome = %+v", temporary)
	}
	forbidden := decideRegisterFailureOutcome(now, registerAttemptResult{statusCode: 403}, registerPolicy, false)
	if forbidden.kind != registrationRejectedPermanent || forbidden.reason != "forbidden" ||
		forbidden.nextRegister != now.Add(5*time.Minute) {
		t.Fatalf("forbidden outcome = %+v", forbidden)
	}
	retryAfter := decideRegisterFailureOutcome(now, registerAttemptResult{
		statusCode: 503, retryAfter: 17 * time.Second, retryAfterSet: true,
	}, registerPolicy, false)
	if retryAfter.reason != "retry_after" || retryAfter.nextRegister != now.Add(17*time.Second) {
		t.Fatalf("retry-after outcome = %+v", retryAfter)
	}
	useProxy := decideRegisterFailureOutcome(now, registerAttemptResult{statusCode: 305}, registerPolicy, false)
	if useProxy.kind != registrationRejectedTemporary || useProxy.reason != "use_proxy" {
		t.Fatalf("305 outcome = %+v", useProxy)
	}
}

// A REGISTER that follows an AKA challenge and never gets answered must retry.
// Classifying it from the challenge's 401 rejected the registrar as permanent
// for 10 minutes and rebuilt the whole session instead.
func TestUnansweredRegisterAfterChallengeRetriesAsTransportFailure(t *testing.T) {
	service, err := New(registerTransportTestConfig("tcp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()

	challenged := registerAttemptResult{statusCode: 401, challengeCount: 1, secAgree: true}
	for _, lost := range []error{sip.ErrTransactionTimeout, sip.ErrTransactionTransport} {
		attemptErr := &registerAttemptError{
			result: challenged,
			err:    fmt.Errorf("imscore: REGISTER CSeq 6 transaction: %w", lost),
		}
		service.reRegisterPending.Store(false)
		service.applyRegistrationFailureStatus(attemptErr)

		if !service.reRegisterPending.Load() {
			t.Fatalf("%v: registration was not retried", lost)
		}
		if status := service.StatusCurrent().RegStatus; status != "RejectedTemporary" {
			t.Fatalf("%v: reg status = %q, want RejectedTemporary", lost, status)
		}
		service.mu.Lock()
		next := service.nextRegister
		service.mu.Unlock()
		if wait := time.Until(next); wait > time.Minute {
			t.Fatalf("%v: next REGISTER in %s, want a short transport retry", lost, wait)
		}
	}

	// A real rejection must still back off instead of retrying at once.
	rejected := &registerAttemptError{
		result: registerAttemptResult{statusCode: 401, challengeCount: 1},
		err:    &registerResponseError{statusCode: 403},
	}
	service.reRegisterPending.Store(true)
	service.applyRegistrationFailureStatus(rejected)
	if service.reRegisterPending.Load() {
		t.Fatal("a 403 rejection was retried like a lost transaction")
	}
	service.mu.Lock()
	next := service.nextRegister
	service.mu.Unlock()
	if wait := time.Until(next); wait < time.Minute {
		t.Fatalf("next REGISTER after 403 in %s, want a long back-off", wait)
	}
}

func TestParseUseProxyContact(t *testing.T) {
	tests := []struct {
		contact, want string
	}{
		{`<sip:pcscf2.ims.example:5060;lr>`, "pcscf2.ims.example:5060"},
		{`sip:192.0.2.10`, "192.0.2.10:5060"},
		{`<sip:user@127.0.0.1:4060>`, "127.0.0.1:4060"},
		{"", ""},
	}
	for _, test := range tests {
		if got := parseUseProxyContact(test.contact); got != test.want {
			t.Fatalf("parseUseProxyContact(%q) = %q, want %q", test.contact, got, test.want)
		}
	}
}

func TestRegisterRetries305UseProxy(t *testing.T) {
	first, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstSeen := make(chan string, 1)
	secondSeen := make(chan string, 1)
	go serveRegisterStatusWithHeaders(first, 305, "Contact: <sip:"+second.LocalAddr().String()+">\r\n", firstSeen)
	go serveRegisterStatus(second, 200, secondSeen)
	service, err := New(registerTransportTestConfig("udp", first.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.Register(ctx); err != nil {
		t.Fatalf("Register after 305: %v", err)
	}
	if <-firstSeen == "" || <-secondSeen == "" {
		t.Fatal("REGISTER did not follow 305 Use Proxy Contact")
	}
	if got := service.registrar; got != second.LocalAddr().String() {
		t.Fatalf("registrar after 305 = %q", got)
	}
}

func TestRegisterTransportDeadlineUsesEarlierContextDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()
	deadline := registerTransportDeadline(ctx, time.Minute)
	contextDeadline, _ := ctx.Deadline()
	if !deadline.Equal(contextDeadline) {
		t.Fatalf("deadline = %s, want %s", deadline, contextDeadline)
	}
}

func TestRefreshRegisterFallsBackToLearnedPath(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	request := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 3,
		path: "<sip:pcscf.ims.example;lr;ob>",
	}, "Digest response")
	if got := sipHeaderValue(request, "Route"); got != "<sip:pcscf.ims.example;lr;ob>" {
		t.Fatalf("Path fallback Route = %q", got)
	}
}

func TestRefreshRegisterPrefersServiceRouteOverPath(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	request := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 3,
		serviceRoute: "<sip:scscf.ims.example;lr>",
		path:         "<sip:pcscf.ims.example;lr;ob>",
	}, "Digest response")
	if got := sipHeaderValue(request, "Route"); got != "<sip:scscf.ims.example;lr>" {
		t.Fatalf("Service-Route = %q", got)
	}
	if strings.Count(request, "\r\nRoute:") != 1 {
		t.Fatalf("stacked Route headers:\n%s", request)
	}
}

func TestProtectedRegisterViaKeepsConfiguredSentByNotPortC(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	request := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 2,
		security: &securityAgreement{
			verifyHeader: "ipsec-3gpp;alg=hmac-sha-1-96",
			client:       securityMechanism{PortC: 50309, PortS: 48554},
		},
	}, "Digest response")
	via := sipHeaderValue(request, "Via")
	if !strings.HasPrefix(via, "SIP/2.0/TCP ") {
		t.Fatalf("protected REGISTER transport = %q, want TCP after SA", via)
	}
	if sipViaSentBy(via) != "192.0.2.10:5060" {
		t.Fatalf("TCP REGISTER Via sent-by = %q, want configured local port", sipViaSentBy(via))
	}
	if sipSentByPort(via) == 50309 || sipSentByPort(via) == 48554 {
		t.Fatalf("TCP REGISTER Via used an IPsec port: %q", via)
	}
	contact := sipHeaderValue(request, "Contact")
	if !strings.Contains(contact, ":48554") {
		t.Fatalf("REGISTER Contact = %q, want port-s", contact)
	}
}

func TestProtectedUDPRegisterViaUsesPortS(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	session := &registerSession{
		security: &securityAgreement{
			verifyHeader: "ipsec-3gpp;alg=hmac-sha-1-96",
			client:       securityMechanism{PortC: 50309, PortS: 48554},
		},
	}
	if got := service.registerLocalAddress(session, "udp"); got != "192.0.2.10:48554" {
		t.Fatalf("UDP protected REGISTER Via = %q, want port-s", got)
	}
	if got := service.registerLocalAddress(session, "tcp"); got != "192.0.2.10:5060" {
		t.Fatalf("TCP protected REGISTER Via = %q, want configured local port", got)
	}
}

func TestSipSentByPortParsesIPv4AndIPv6(t *testing.T) {
	if got := sipSentByPort("SIP/2.0/TCP 192.0.2.10:5060;rport;branch=z9hG4bK"); got != 5060 {
		t.Fatalf("IPv4 via port = %d", got)
	}
	if got := sipSentByPort("SIP/2.0/TCP [2001:db8::10]:50309;rport;branch=z9hG4bK"); got != 50309 {
		t.Fatalf("IPv6 via port = %d", got)
	}
}

func TestInitialRegisterOmitsOutboundContactUntilNegotiated(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
		RegisterTemplate: IMSRegisterTemplate{
			ContactOrder:    []string{"access_type", "sip_instance", "audio", "icsi_ref"},
			AccessType:      "wlan1",
			SupportedHeader: "path,sec-agree,outbound",
		},
		IMEI: "356938035643809",
	}}
	before := sipHeaderValue(service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 1,
	}, ""), "Contact")
	if strings.Contains(before, ";ob") || strings.Contains(before, "reg-id=") {
		t.Fatalf("pre-negotiation Contact = %q", before)
	}
	if !strings.Contains(before, `+sip.instance="<urn:gsma:imei:35693803-564380-9>"`) {
		t.Fatalf("initial Contact omitted sip.instance = %q", before)
	}
}

func TestInitialRegisterOmitsOutboundContactWithoutSupportedOutbound(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
		RegisterTemplate: IMSRegisterTemplate{
			ContactOrder:    []string{"access_type", "sip_instance", "audio", "icsi_ref"},
			AccessType:      "wlan1",
			SupportedHeader: "path,sec-agree",
		},
		IMEI: "356938035643809",
	}}
	before := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 1,
	}, "")
	if strings.Contains(sipHeaderValue(before, "Contact"), ";ob") ||
		strings.Contains(sipHeaderValue(before, "Contact"), "reg-id=") {
		t.Fatalf("pre-negotiation Contact = %q", sipHeaderValue(before, "Contact"))
	}
	service.logRegisterFlowNegotiation(&sipResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Path": "<sip:pcscf.example;lr;ob>"},
	})
	after := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 3,
	}, "Digest response")
	contact := sipHeaderValue(after, "Contact")
	if !strings.Contains(contact, ";ob") || !strings.Contains(contact, "reg-id=1") {
		t.Fatalf("post-negotiation Contact = %q", contact)
	}
}

func TestSupportedOutboundDoesNotRequireFollowUpRegister(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	service.logRegisterFlowNegotiation(&sipResponse{
		StatusCode: 200,
		Headers: map[string]string{
			"Supported": "path, outbound",
			"Path":      "<sip:pcscf.example;lr>",
		},
	})
	if service.needsOutboundBindingRefresh() {
		t.Fatal("Supported: outbound without Path;ob or Require must not send a follow-up REGISTER")
	}
	if service.outboundBindingRequired() {
		t.Fatal("Supported: outbound is not Require: outbound")
	}
}

// Swallowing a failed outbound-binding REGISTER is only safe while the flow
// it ran over survived. Measured on 2026-09-03: a 503 on that REGISTER
// detached the flow, registration was still reported successful with an empty
// transport, and nothing noticed until three keepalives had failed 90s later
// and the runtime rebuilt from scratch.
func TestFailedOutboundRefreshThatDetachesTheFlowFailsRegistration(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "tcp",
	}}
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	service.mu.Lock()
	service.registrationTransport = "tcp"
	service.registrationTCP = client
	service.mu.Unlock()
	if !service.keepRegistrationAfterFailedOutboundRefresh() {
		t.Fatal("an optional refresh that left the flow up must keep the registration")
	}

	// What detachDeadSignaling leaves behind.
	service.mu.Lock()
	service.registrationTransport = ""
	service.registrationTCP = nil
	service.registrationIO = nil
	service.mu.Unlock()
	if service.registrationFlowIntact() {
		t.Fatal("a detached flow still reported itself intact")
	}
	if service.keepRegistrationAfterFailedOutboundRefresh() {
		t.Fatal("registration was kept with no flow left to carry it")
	}

	// Require: outbound stays fatal regardless of what the flow did.
	service.mu.Lock()
	service.registrationTransport = "tcp"
	service.registrationTCP = client
	service.sipOutboundRequired = true
	service.mu.Unlock()
	if service.keepRegistrationAfterFailedOutboundRefresh() {
		t.Fatal("Require: outbound must fail registration when the refresh fails")
	}
}

func TestRequireOutboundRequiresFollowUpRegister(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	service.logRegisterFlowNegotiation(&sipResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Require": "outbound"},
	})
	if !service.needsOutboundBindingRefresh() {
		t.Fatal("Require: outbound must trigger a follow-up REGISTER with reg-id")
	}
	if !service.outboundBindingRequired() {
		t.Fatal("Require: outbound must fail registration if the follow-up REGISTER fails")
	}
}

func TestPathOBRequiresFollowUpRegisterWithRegID(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	if service.needsOutboundBindingRefresh() {
		t.Fatal("outbound refresh before Path ob")
	}
	service.logRegisterFlowNegotiation(&sipResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Path": "<sip:pcscf.example;lr;ob>"},
	})
	if !service.needsOutboundBindingRefresh() {
		t.Fatal("Path ob must trigger a follow-up REGISTER with reg-id before SMS is treated as reachable")
	}
	service.mu.Lock()
	service.outboundContactOffered = true
	service.mu.Unlock()
	service.logRegisterFlowNegotiation(&sipResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Path": "<sip:pcscf.example;lr;ob>"},
	})
	if service.needsOutboundBindingRefresh() {
		t.Fatal("outbound REGISTER already completed")
	}
}

func TestWithOutboundContactParamsKeepsSipInstance(t *testing.T) {
	got := withOutboundContactParams(nil)
	if !containsContactParamName(got, "sip_instance") ||
		!containsContactParamName(got, "reg_id") ||
		!containsContactParamName(got, "ob") {
		t.Fatalf("order = %v", got)
	}
}

func TestRefreshRegisterCarriesLearnedServiceRoute(t *testing.T) {
	service := &Service{cfg: &IMSConfig{
		IMPI: "user@ims.example", IMPU: "sip:user@ims.example", Domain: "ims.example",
		LocalIP: net.ParseIP("192.0.2.10"), LocalPort: 5060, Transport: "udp",
	}}
	request := service.buildRegister(&registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1", cseq: 3,
		serviceRoute: "<sip:pcscf.ims.example;lr>",
	}, "Digest response")
	if got := sipHeaderValue(request, "Route"); got != "<sip:pcscf.ims.example;lr>" {
		t.Fatalf("refresh REGISTER Route = %q", got)
	}
}

func TestClearRegistrationBindingsUsesAuthenticatedWildcardRegister(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 1)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, nil))
		return nil
	})

	if err := service.ClearRegistrationBindings(context.Background()); err != nil {
		t.Fatalf("ClearRegistrationBindings: %v", err)
	}
	request := <-requests
	if got := sipHeaderValue(request, "Contact"); got != "*" {
		t.Fatalf("Contact = %q, want wildcard", got)
	}
	if got := sipHeaderValue(request, "Expires"); got != "0" {
		t.Fatalf("Expires = %q, want 0", got)
	}
	if got := sipHeaderValue(request, "Authorization"); got != "Digest username=\"user\"" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := parseCSeq(sipHeaderValue(request, "CSeq")); got != 8 {
		t.Fatalf("CSeq = %d, want 8", got)
	}
	if service.regSession.cseq != 8 || service.regSession.expires != time.Hour {
		t.Fatalf("registration session = %+v", service.regSession)
	}
}

func TestUnregisterRemovesOnlyCurrentAuthenticatedContact(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regState = regRegistered
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 1)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, nil))
		return nil
	})

	if err := service.Unregister(context.Background()); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	request := <-requests
	if got := sipHeaderValue(request, "Contact"); got == "*" || !strings.Contains(got, "expires=0") {
		t.Fatalf("Contact = %q, want current Contact with expires=0", got)
	}
	if got := sipHeaderValue(request, "Expires"); got != "0" {
		t.Fatalf("Expires = %q, want 0", got)
	}
	if got := sipHeaderValue(request, "Authorization"); got != "Digest username=\"user\"" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := parseCSeq(sipHeaderValue(request, "CSeq")); got != 8 {
		t.Fatalf("CSeq = %d, want 8", got)
	}
	if service.regState != regUnregister || service.regSession.cseq != 8 {
		t.Fatalf("registration state/session = %s/%+v", service.regState, service.regSession)
	}
}

func TestUnregisterFallsBackToWildcardAfterContactReject(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regState = regRegistered
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 2)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		status := 200
		if sipHeaderValue(request, "Contact") != "*" {
			status = 500
		}
		service.transport.DeliverResponse(registerResponseForRequest(request, status, nil))
		return nil
	})

	if err := service.Unregister(context.Background()); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	first := <-requests
	second := <-requests
	if got := sipHeaderValue(first, "Contact"); got == "*" || !strings.Contains(got, "expires=0") {
		t.Fatalf("first Contact = %q, want current Contact with expires=0", got)
	}
	if got := sipHeaderValue(second, "Contact"); got != "*" {
		t.Fatalf("second Contact = %q, want wildcard", got)
	}
	if parseCSeq(sipHeaderValue(first, "CSeq")) != 8 || parseCSeq(sipHeaderValue(second, "CSeq")) != 9 {
		t.Fatalf("CSeq = %q / %q", sipHeaderValue(first, "CSeq"), sipHeaderValue(second, "CSeq"))
	}
	if service.regState != regUnregister || service.regSession.cseq != 9 {
		t.Fatalf("registration state/session = %s/%+v", service.regState, service.regSession)
	}
}

func TestUnregisterAllSendsFallbackAfterAttemptTimeout(t *testing.T) {
	previous := shutdownDeregisterAttemptTimeout
	shutdownDeregisterAttemptTimeout = 40 * time.Millisecond
	t.Cleanup(func() { shutdownDeregisterAttemptTimeout = previous })

	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regState = regRegistered
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 2)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		if sipHeaderValue(request, "Contact") == "*" {
			return nil
		}
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, nil))
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.UnregisterAll(ctx); err != nil {
		t.Fatalf("UnregisterAll: %v", err)
	}
	first := <-requests
	second := <-requests
	if sipHeaderValue(first, "Contact") != "*" {
		t.Fatalf("first Contact = %q, want wildcard", sipHeaderValue(first, "Contact"))
	}
	if got := sipHeaderValue(second, "Contact"); got == "*" || !strings.Contains(got, "expires=0") {
		t.Fatalf("fallback Contact = %q, want current Contact with expires=0", got)
	}
}

func TestUnregisterDoesNotWildcardAfterContextDeadline(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regState = regRegistered
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 2)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := service.Unregister(ctx); err == nil {
		t.Fatal("Unregister succeeded without a response")
	}
	select {
	case request := <-requests:
		if got := sipHeaderValue(request, "Contact"); got == "*" {
			t.Fatal("deadline exceeded still sent wildcard REGISTER")
		}
	case <-time.After(time.Second):
		t.Fatal("contact deregistration was not sent")
	}
	select {
	case request := <-requests:
		t.Fatalf("unexpected extra deregistration Contact = %q", sipHeaderValue(request, "Contact"))
	default:
	}
}

// Shutdown leaves the registrar binding alone. Measured on 2026-09-03, 3 of 11
// shutdown de-registrations timed out on the Contact expires=0 and escalated to
// Contact:*, which third-party de-registers the IP-SM-GW and stopped Vodafone
// pushing MT MESSAGE even after a clean re-REGISTER. The binding it would have
// removed expires on its own and is replaced by the next registration.
func TestStopLeavesTheRegistrarBindingInPlace(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	service.regState = regRegistered
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 3, authHeader: "Digest username=\"user\"", expires: time.Hour,
	}
	requests := make(chan string, 4)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, nil))
		return nil
	})

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	close(requests)
	for request := range requests {
		if strings.EqualFold(sipRequestMethod(request), "REGISTER") {
			t.Fatalf("shutdown sent a REGISTER with Contact = %q",
				sipHeaderValue(request, "Contact"))
		}
	}
}

// A binding left by an earlier run carries its own Contact URI, so it cannot be
// replaced by registering again. Clearing it took a wildcard de-registration,
// which dropped this registration and its SA with it.
func TestRegisterKeepsItsBindingWhenStaleContactsAreAdvertised(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 1, authHeader: "Digest username=\"user\"", publicID: service.cfg.IMPU,
		expires: time.Hour,
	}
	requests := make(chan string, 3)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, map[string]string{
			"Contact": sipHeaderValue(request, "Contact") + `, <sip:stale@old.example>`,
		}))
		return nil
	})
	if err := service.Register(context.Background()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	first := <-requests
	if sipHeaderValue(first, "Contact") == "*" {
		t.Fatal("initial REGISTER used a wildcard Contact")
	}
	select {
	case extra := <-requests:
		t.Fatalf("the stale binding triggered another REGISTER with Contact=%q Expires=%q",
			sipHeaderValue(extra, "Contact"), sipHeaderValue(extra, "Expires"))
	case <-time.After(200 * time.Millisecond):
	}
	if status := service.StatusCurrent().RegStatus; status != "Registered" {
		t.Fatalf("reg status = %q, want the registration kept", status)
	}
}

func TestRegisterContactBindingCountSplitsContactList(t *testing.T) {
	if got := registerContactBindingCount(&sipResponse{Headers: map[string]string{
		"Contact": `<sip:one@example>, <sip:two@example>`,
	}}); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	if got := registerContactBindingCount(&sipResponse{Headers: map[string]string{
		"Contact": `<sip:one@example>;+sip.instance="<urn:uuid:x>";+g.3gpp.icsi-ref="urn:a,urn:b,urn:c,urn:d"`,
	}}); got != 1 {
		t.Fatalf("quoted ICSI commas counted as extra Contacts: %d", got)
	}
	if got := registerContactBindingCount(&sipResponse{Headers: map[string]string{
		"Contact": `<sip:one@example>;+sip.instance="<urn:uuid:x>";reg-id=1, <sip:user@ims.example;gr=urn:uuid:gruu>`,
	}}); got != 1 {
		t.Fatalf("GRUU Contact counted as a registrar flow: %d", got)
	}
	if got := registerContactBindingCount(&sipResponse{Headers: map[string]string{"Contact": "*"}}); got != 0 {
		t.Fatalf("wildcard count = %d, want 0", got)
	}
}

func TestRegisterClearsProvenDuplicateBindingsBeforeRegistering(t *testing.T) {
	config := registerTransportTestConfig("udp", "127.0.0.1:5060")
	config.DeviceID = t.Name()
	config.CarrierPresetID = giffgaffCarrierPresetID
	service, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.regSession = &registerSession{
		callID: "call-1", fromTag: "tag-1", contactUser: "contact-1",
		cseq: 7, authHeader: "Digest username=\"user\"", publicID: config.IMPU,
		expires: time.Hour,
	}
	isolateRegistrationCleanupAttempt(t, service)
	document, err := parseReginfoXML([]byte(`<reginfo><registration aor="sip:user@example">` +
		`<contact id="contact-1" state="active"><uri>sip:contact-1@new.example</uri></contact>` +
		`<contact id="stale" state="active"><uri>sip:stale@old.example</uri></contact>` +
		`</registration></reginfo>`))
	if err != nil {
		t.Fatal(err)
	}
	if !service.requestRegistrationBindingCleanup(document) {
		t.Fatal("duplicate binding did not request cleanup")
	}

	requests := make(chan string, 2)
	service.transport.SetSendFn(func(request string) error {
		requests <- request
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, nil))
		return nil
	})

	if err := service.Register(context.Background()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	wildcard, registeredAgain := <-requests, <-requests
	if sipHeaderValue(wildcard, "Contact") != "*" || sipHeaderValue(wildcard, "Expires") != "0" {
		t.Fatalf("cleanup REGISTER Contact=%q Expires=%q",
			sipHeaderValue(wildcard, "Contact"), sipHeaderValue(wildcard, "Expires"))
	}
	if sipHeaderValue(registeredAgain, "Contact") == "*" {
		t.Fatal("registration after cleanup retained wildcard Contact")
	}
	wantCSeq := []int{8, 9}
	for index, request := range []string{wildcard, registeredAgain} {
		if got := parseCSeq(sipHeaderValue(request, "CSeq")); got != wantCSeq[index] {
			t.Fatalf("request %d CSeq = %d, want %d", index+1, got, wantCSeq[index])
		}
	}
}

func TestServiceStatusRestoresRegistrationDiagnostics(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.registrar = "pcscf.example:5060"
	service.registrarCandidates = []string{"pcscf.example:5060", "pcscf2.example:5060"}
	service.registrarSource = "registrar"
	service.lastSIPCode.Store(503)
	service.lastSIPText = "Service Unavailable"
	service.transitionRegStatus(registrationRejectedTemporary)
	status := service.StatusCurrent()
	if status.RegStatus != "RejectedTemporary" || status.IMPU != service.cfg.IMPU ||
		status.LastSIPCode != 503 || status.RegistrarSource != "registrar" {
		t.Fatalf("status = %+v", status)
	}
	status.RegStatus = "Registered"
	if !status.IsRegistered() {
		t.Fatal("ServiceStatus.IsRegistered ignored recovered RegStatus")
	}
	if got := status.ToMap()["registrar_candidates"]; !reflect.DeepEqual(got, status.RegistrarCandidates) {
		t.Fatalf("ToMap registrar_candidates = %v", got)
	}
}

func TestRegisterAPDUBusySchedulesThreeSecondRetry(t *testing.T) {
	config := registerTransportTestConfig("udp", "127.0.0.1:5060")
	config.AKAProvider = busyAKAProvider{}
	service, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.transport.SetSendFn(func(request string) error {
		challenge := strings.TrimPrefix(strings.TrimSpace(digestChallengeHeader(0x11, 0x22)), "WWW-Authenticate: ")
		service.transport.DeliverResponse(registerResponseForRequest(request, 401, map[string]string{
			"WWW-Authenticate": challenge,
		}))
		return nil
	})
	started := time.Now()
	err = service.Register(context.Background())
	if !errors.Is(err, enginesim.ErrAPDUBusy) {
		t.Fatalf("Register error = %v", err)
	}
	service.mu.RLock()
	next := service.nextRegister
	service.mu.RUnlock()
	if next.Before(started.Add(2900*time.Millisecond)) || next.After(started.Add(3100*time.Millisecond)) {
		t.Fatalf("next register = %s, started = %s", next, started)
	}
}

func TestRegisterFailureTriggersOneReconnectAtATime(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	service.OnReconnectNeeded = func() {
		entered <- struct{}{}
		<-release
	}
	service.triggerRegisterReconnect()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reconnect callback was not invoked")
	}
	service.triggerRegisterReconnect()
	if len(entered) != 0 {
		t.Fatal("concurrent reconnect callback was invoked")
	}
	close(release)
}

func TestCanceledRegisterDoesNotTriggerFailureFSMOrReconnect(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.transport.SetSendFn(func(string) error { return nil })
	reconnect := make(chan struct{}, 1)
	service.OnReconnectNeeded = func() { reconnect <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Register(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Register error = %v", err)
	}
	if got := service.regStatus.Load(); got != registrationUnregistered {
		t.Fatalf("registration status = %s", registrationStatusText(got))
	}
	select {
	case <-reconnect:
		t.Fatal("canceled REGISTER triggered reconnect")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRegisterTCPDoesNotRetryUDPAfterAuthenticationChallenge(t *testing.T) {
	tcpRegistrar, udpRegistrar := listenRegisterTransports(t)
	defer tcpRegistrar.Close()
	defer udpRegistrar.Close()
	serverResult := make(chan error, 1)
	go serveChallengedTCPRegister(tcpRegistrar, serverResult)

	service, err := New(registerTransportTestConfig("tcp", tcpRegistrar.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = service.Register(ctx)
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("Register error = %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	assertNoUDPRegister(t, udpRegistrar)
}

func TestRegisterRetries423WithMinExpiresOnSameTransport(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer registrar.Close()
	requests := make(chan string, 2)
	go serveMinExpiresSequence(registrar, requests)
	service, err := New(registerTransportTestConfig("udp", registrar.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	<-requests
	second := <-requests
	if got := sipHeaderValue(second, "Expires"); got != "7200" {
		t.Fatalf("second REGISTER Expires = %q", got)
	}
}

func TestTemporaryFailureAdvancesRegistrarForNextRegister(t *testing.T) {
	first, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstSeen := make(chan string, 1)
	secondSeen := make(chan string, 1)
	go serveRegisterStatus(first, 503, firstSeen)
	go serveRegisterStatus(second, 200, secondSeen)
	registrars := first.LocalAddr().String() + ";" + second.LocalAddr().String()
	service, err := New(registerTransportTestConfig("udp", registrars))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.Register(ctx); err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("first Register error = %v", err)
	}
	if err := service.Register(ctx); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if <-firstSeen == "" || <-secondSeen == "" {
		t.Fatal("REGISTER requests did not reach both registrar candidates")
	}
}

func TestRegisterNetworkFailureAdvancesRegistrar(t *testing.T) {
	service, err := New(registerTransportTestConfig("udp", "pcscf-a.example:5060;pcscf-b.example:5060"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.StopCurrent()
	service.mu.Lock()
	service.registrar = "pcscf-a.example:5060"
	service.registrarCandidates = []string{"pcscf-a.example:5060", "pcscf-b.example:5060"}
	service.registrarSource = "registrar"
	service.mu.Unlock()
	service.transport.SetSendFn(func(string) error { return errors.New("network unavailable") })
	if err := service.Register(context.Background()); err == nil || !strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("Register error = %v", err)
	}
	service.mu.RLock()
	registrar, index := service.registrar, service.registrarIndex
	service.mu.RUnlock()
	if registrar != "pcscf-b.example:5060" || index != 1 {
		t.Fatalf("registrar after network failure = %q index %d", registrar, index)
	}
}

func serveMinExpiresSequence(registrar *net.UDPConn, requests chan<- string) {
	buffer := make([]byte, 64*1024)
	for attempt := 0; attempt < 2; attempt++ {
		n, remote, err := registrar.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		request := string(buffer[:n])
		requests <- request
		status, headers := 423, "Min-Expires: 7200\r\n"
		if attempt == 1 {
			status, headers = 200, "Expires: 7200\r\n"
		}
		_, _ = registrar.WriteToUDP([]byte(registerWireResponse(request, status, headers)), remote)
	}
}

func serveChallengedTCPRegister(listener *net.TCPListener, result chan<- error) {
	conn, err := listener.AcceptTCP()
	if err != nil {
		result <- err
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	initial, err := readSIPStreamMessage(reader)
	if err != nil {
		result <- err
		return
	}
	challenge := strings.TrimPrefix(strings.TrimSpace(digestChallengeHeader(0x11, 0x22)), "WWW-Authenticate: ")
	if _, err = conn.Write([]byte(registerWireResponse(initial, 401, "WWW-Authenticate: "+challenge+"\r\n"))); err != nil {
		result <- err
		return
	}
	authenticated, err := readSIPStreamMessage(reader)
	if err == nil {
		_, err = conn.Write([]byte(registerWireResponse(authenticated, 503, "")))
	}
	result <- err
}
