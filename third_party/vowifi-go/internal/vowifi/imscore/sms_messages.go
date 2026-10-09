package imscore

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
)

const smsSupportedHeader = "path, 100rel, replaces, gruu"

func (s *Service) buildSMSMESSAGE(remoteURI string, body []byte) (string, error) {
	return s.buildSMSMESSAGEWithOptions(smsMESSAGEOptions{
		RemoteURI: remoteURI,
		Body:      body,
	})
}

type smsMESSAGEOptions struct {
	RemoteURI     string
	Body          []byte
	InReplyTo     string
	ContentType   string
	OmitBinaryCTE bool
	OmitInReplyTo bool
}

func (s *Service) buildSMSMESSAGEWithOptions(options smsMESSAGEOptions) (string, error) {
	if s == nil || s.cfg == nil {
		return "", errors.New("imscore: SMS service is not configured")
	}
	remoteURI := strings.TrimSpace(options.RemoteURI)
	if remoteURI == "" || strings.ContainsAny(remoteURI, "\r\n") {
		return "", errors.New("imscore: invalid SMS remote URI")
	}
	inReplyTo := strings.TrimSpace(options.InReplyTo)
	if options.OmitInReplyTo {
		inReplyTo = ""
	}
	if strings.ContainsAny(inReplyTo, "\r\n") {
		return "", errors.New("imscore: invalid SMS In-Reply-To")
	}
	profile, err := s.reserveRegisteredSIPProfile()
	if err != nil {
		return "", fmt.Errorf("imscore: SMS registered profile: %w", err)
	}
	branch := "z9hG4bK" + newBranch()
	// RFC 3261 8.1.1.4: out-of-dialog request Call-ID is UAC-generated.
	// 24.341 5.3.2.4 d) links to the MT via In-Reply-To, not Call-ID.
	callID := newCallID()
	var request strings.Builder
	fmt.Fprintf(&request, "MESSAGE %s SIP/2.0\r\n", remoteURI)
	fmt.Fprintf(&request, "Via: SIP/2.0/%s %s;rport;branch=%s\r\n", transportUpper(profile.Transport), profile.LocalAddress, branch)
	if profile.ServiceRoute != "" {
		request.WriteString("Route: " + profile.ServiceRoute + "\r\n")
	}
	fmt.Fprintf(&request, "From: <%s>;tag=%s\r\n", profile.LocalURI, newTag())
	fmt.Fprintf(&request, "To: <%s>\r\n", remoteURI)
	fmt.Fprintf(&request, "Call-ID: %s\r\n", callID)
	if inReplyTo != "" {
		request.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
	}
	fmt.Fprintf(&request, "CSeq: %d MESSAGE\r\n", profile.InitialCSeq)
	request.WriteString("Contact: " + profile.ContactHeader + "\r\n")
	request.WriteString("Max-Forwards: 70\r\n")
	request.WriteString("P-Preferred-Identity: <" + profile.LocalURI + ">\r\n")
	supported := smsSupportedHeader
	if profile.SecurityVerify != "" {
		supported += ", sec-agree"
		request.WriteString("Security-Verify: " + profile.SecurityVerify + "\r\n")
	}
	request.WriteString("Supported: " + supported + "\r\n")
	if pani := strings.TrimSpace(profile.PANI); pani != "" {
		request.WriteString("P-Access-Network-Info: " + pani + "\r\n")
	}
	handset := smsHandsetHeaders(s.cfg)
	if handset {
		// AT&T answers 403 to a MESSAGE that names the icsi.sms service in
		// P-Preferred-Service; an iPhone sends only the smsip feature tag.
		request.WriteString("Accept-Contact: *;+g.3gpp.smsip\r\n")
		request.WriteString("Request-Disposition: no-fork\r\n")
	} else {
		request.WriteString("P-Preferred-Service: " + imsSMSPreferredService + "\r\n")
		request.WriteString("Accept-Contact: " + imsSMSAcceptContact + "\r\n")
	}
	if userAgent := strings.TrimSpace(profile.UserAgent); userAgent != "" {
		request.WriteString("User-Agent: " + userAgent + "\r\n")
	}
	contentType := strings.TrimSpace(options.ContentType)
	if contentType == "" {
		contentType = imsSMSContentType
	}
	if strings.ContainsAny(contentType, "\r\n") {
		return "", errors.New("imscore: invalid SMS Content-Type")
	}
	request.WriteString("Content-Type: " + contentType + "\r\n")
	if normalizedContentType(contentType) == imsSMSContentType {
		if !options.OmitBinaryCTE && !handset {
			request.WriteString("Content-Transfer-Encoding: binary\r\n")
		}
	}
	fmt.Fprintf(&request, "Content-Length: %d\r\n\r\n", len(options.Body))
	request.Write(options.Body)
	logging.Info("IMS SMS MESSAGE outbound", "device", s.DeviceID(), "request_uri", remoteURI,
		"from", profile.LocalURI, "route", profile.ServiceRoute, "handset_headers", handset,
		"body_bytes", len(options.Body))
	return request.String(), nil
}

// smsHandsetHeaders reports a carrier whose MESSAGE must carry an iPhone's
// SMS headers (TS 24.341: the +g.3gpp.smsip feature tag, no ICSI).
func smsHandsetHeaders(cfg *IMSConfig) bool {
	return cfg != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.CarrierPresetID)), "att_")
}

func (s *Service) buildOutboundMESSAGE(remoteURI string, body []byte) (*sip.Request, error) {
	return s.buildOutboundMESSAGEWithOptions(smsMESSAGEOptions{RemoteURI: remoteURI, Body: body})
}

func (s *Service) buildOutboundMESSAGEWithOptions(options smsMESSAGEOptions) (*sip.Request, error) {
	raw, err := s.buildSMSMESSAGEWithOptions(options)
	if err != nil {
		return nil, err
	}
	message, err := parseSIPMessage(raw)
	if err != nil {
		return nil, fmt.Errorf("parse outbound MESSAGE: %w", err)
	}
	request, ok := message.(*sip.Request)
	if !ok {
		return nil, errors.New("outbound MESSAGE builder returned a non-request")
	}
	return request, nil
}

func (s *Service) buildRPAckMESSAGE(inbound string, body []byte, remoteURI, contentType string, omitBinaryCTE, omitInReplyTo bool) (*sip.Request, error) {
	raw, err := s.buildInboundSMSControlRequest(inbound, body, remoteURI, contentType, omitBinaryCTE, omitInReplyTo)
	if err != nil {
		return nil, err
	}
	message, err := parseSIPMessage(raw)
	if err != nil {
		return nil, fmt.Errorf("parse RP-ACK MESSAGE: %w", err)
	}
	request, ok := message.(*sip.Request)
	if !ok {
		return nil, errors.New("RP-ACK builder returned a non-request")
	}
	return request, nil
}

type registeredSIPRoute struct {
	clientAddress  string
	serverAddress  string
	remoteAddress  string
	serviceRoute   string
	securityVerify string
	transport      string
	live           bool
}

func (s *Service) registeredSIPRouteLocked() registeredSIPRoute {
	clientPort := s.protectedClientPort
	serverPort := s.protectedServerPort
	transport := "tcp"
	if s.registrationTCP == nil {
		transport = strings.ToLower(strings.TrimSpace(s.cfg.Transport))
		if transport == "" {
			transport = "udp"
		}
		// TCP/TLS after SA keep the configured local ports when the
		// protected TCP socket is not up. UDP IPsec keeps port-s so Via
		// sent-by can follow 24.229 5.1.2A.1.1 / 5.1.1.2.2 (c).
		if !sipTransportIsUDP(transport) || serverPort < 1 {
			clientPort, serverPort = s.cfg.LocalPort, s.cfg.LocalPort
		}
	}
	route := registeredSIPRoute{transport: transport}
	if clientPort > 0 {
		viaPort := clientPort
		if sipTransportIsUDP(transport) {
			viaPort = protectedViaSentByPort(transport, serverPort, clientPort)
		}
		route.clientAddress = net.JoinHostPort(s.cfg.LocalIP.String(), fmt.Sprint(viaPort))
	}
	if serverPort > 0 {
		route.serverAddress = net.JoinHostPort(s.cfg.LocalIP.String(), fmt.Sprint(serverPort))
	}
	route.remoteAddress = s.registeredRemoteAddressLocked()
	if s.regSession != nil {
		route.serviceRoute = s.originatingRouteSetLocked(s.regSession.serviceRoute)
		if s.regSession.security != nil {
			route.securityVerify = strings.TrimSpace(s.regSession.security.verifyHeader)
		}
	} else if path := strings.TrimSpace(s.path); path != "" {
		route.serviceRoute = path
	}
	route.live = s.registeredSIPTransportReadyLocked()
	return route
}

func (s *Service) registeredRemoteAddressLocked() string {
	if s.registrationTCP != nil {
		if address := validSIPRemoteAddress(s.registrationTCP.RemoteAddr()); address != "" {
			return address
		}
	}
	if s.registrationRemote == nil || s.registrationRemote.IP == nil {
		return ""
	}
	port := s.registrationRemote.Port
	if s.regSession != nil && s.regSession.security != nil && s.regSession.security.server != nil {
		if negotiated := int(s.regSession.security.server.PortS); negotiated > 0 {
			port = negotiated
		}
	}
	if port < 1 {
		return ""
	}
	return net.JoinHostPort(s.registrationRemote.IP.String(), fmt.Sprint(port))
}

func validSIPRemoteAddress(address net.Addr) string {
	if address == nil {
		return ""
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(address.String()))
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return ""
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func (s *Service) registeredSIPTransportReadyLocked() bool {
	if s.externalTransport {
		return true
	}
	if s.regSession != nil && s.regSession.security != nil &&
		strings.TrimSpace(s.regSession.security.verifyHeader) != "" {
		return s.registrationTCP != nil && s.registrationTCPProtected
	}
	return s.registrationTCP != nil || s.registrationIO != nil
}

func (s *Service) smsMessageRoute() (clientAddress, serverAddress, route, securityVerify, transport string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := s.registeredSIPRouteLocked()
	return snapshot.clientAddress, snapshot.serverAddress, snapshot.serviceRoute,
		snapshot.securityVerify, snapshot.transport
}
