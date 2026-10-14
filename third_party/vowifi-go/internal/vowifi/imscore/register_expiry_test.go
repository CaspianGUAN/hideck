package imscore

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
)

func TestRegistrationExpiresMatchesCurrentBinding(t *testing.T) {
	const own = "<sip:current@192.0.2.10:5060;transport=tcp>"
	const other = "<sip:old@192.0.2.20:5060;transport=tcp>;expires=7200"
	tests := []struct {
		name, contacts, expires string
		want                    time.Duration
		wantError               bool
	}{
		{name: "own overrides global", contacts: own + ";expires=120", expires: "3600", want: 120 * time.Second},
		{name: "other first", contacts: other + "," + own + ";expires=120", want: 120 * time.Second},
		{name: "own first", contacts: own + ";expires=120," + other, want: 120 * time.Second},
		{name: "separate headers", contacts: other + "\r\nContact: " + own + ";expires=120", want: 120 * time.Second},
		{name: "own missing expiry", contacts: other + "," + own, expires: "300", want: 300 * time.Second},
		{name: "no matching binding", contacts: other, expires: "300", wantError: true},
		{name: "no matching binding cannot use configured", contacts: other, wantError: true},
		{name: "missing all headers", wantError: true},
		{name: "own binding without expiration uses configured", contacts: own, want: 10 * time.Minute},
		{name: "quoted comma", contacts: `"Old, phone" ` + other + "," + own + `;+g.3gpp.icsi-ref="urn:a,urn:b";expires=120`, want: 120 * time.Second},
		{name: "parameter name exact", contacts: own + ";x-expires=900;expires=120", want: 120 * time.Second},
		{name: "URI expiry is not header expiry", contacts: strings.TrimSuffix(own, ">") + ";expires=900>", expires: "300", want: 300 * time.Second},
		{name: "parameter case and whitespace", contacts: own + "; ExPiReS = 120", want: 120 * time.Second},
		{name: "own zero is not global", contacts: own + ";expires=0", expires: "3600", wantError: true},
		{name: "invalid own expiry is not global", contacts: own + ";expires=120junk", expires: "3600", wantError: true},
		{name: "negative expiry", contacts: own + ";expires=-1", wantError: true},
		{name: "overflow expiry", contacts: own + ";expires=4294967296", wantError: true},
		{name: "global zero", contacts: own, expires: "0", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := "SIP/2.0 200 OK\r\nContent-Length: 0\r\n"
			if test.contacts != "" {
				wire += "Contact: " + test.contacts + "\r\n"
			}
			if test.expires != "" {
				wire += "Expires: " + test.expires + "\r\n"
			}
			response, err := parseSIPResponse(wire + "\r\n")
			if err != nil {
				t.Fatal(err)
			}
			got, err := registrationExpires(response, own, 10*time.Minute)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("registrationExpires = %s, %v; want %s, error=%t", got, err, test.want, test.wantError)
			}
		})
	}
}

func TestRegisterContactURIMatching(t *testing.T) {
	const own = "<sip:current@ims.example:5060;transport=tcp>"
	tests := []struct {
		name, request, response string
		match                   bool
	}{
		{"case insensitive host and transport", own, "<sip:current@IMS.EXAMPLE:5060;Transport=TCP>", true},
		{"escaped user", own, "<sip:%63urrent@ims.example:5060;transport=tcp>", true},
		{"user case sensitive", own, "<sip:Current@ims.example:5060;transport=tcp>", false},
		{"different user", own, "<sip:old@ims.example:5060;transport=tcp>", false},
		{"different host", own, "<sip:current@other.example:5060;transport=tcp>", false},
		{"different port", own, "<sip:current@ims.example:5061;transport=tcp>", false},
		{"different transport", own, "<sip:current@ims.example:5060;transport=udp>", false},
		{"omitted port", own, "<sip:current@ims.example;transport=tcp>", false},
		{"omitted transport", own, "<sip:current@ims.example:5060>", false},
		{"different scheme", own, "<sips:current@ims.example:5060;transport=tcp>", false},
		{"extra extension", own, "<sip:current@ims.example:5060;transport=tcp;extension=1>", true},
		{"URI headers", own, "<sip:current@ims.example:5060;transport=tcp?subject=test>", false},
		{"IPv6", "<sip:current@[2001:db8::1]:5060;transport=tcp>", "<sip:current@[2001:DB8::1]:5060;transport=tcp>", true},
		{"unbracketed", "sip:current@ims.example", "sip:current@ims.example", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &sipResponse{Headers: map[string]string{"Contact": test.response + ";expires=120"}}
			got := matchingRegisterContact(response, test.request) != ""
			if got != test.match {
				t.Fatalf("matched = %t, want %t", got, test.match)
			}
		})
	}
}

func TestRegisterSchedulesRefreshFromSentContact(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer registrar.Close()
	result := make(chan error, 1)
	go func() { result <- replyWithOwnContactExpiry(registrar) }()
	svc, err := New(&IMSConfig{
		DeviceID: "expiry-test", IMEI: "356938035643809", IMSI: "310260123456789",
		IMPI: "310260123456789@ims.example", IMPU: "sip:310260123456789@ims.example", Domain: "ims.example",
		LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp", Registrar: registrar.LocalAddr().String(),
		IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)), AKAProvider: stubAKAProvider{},
		EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.StopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	before := time.Now()
	if err := svc.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	svc.mu.RLock()
	expires, refresh := svc.regSession.expires, svc.registrationRefreshAt
	svc.mu.RUnlock()
	if expires != 120*time.Second {
		t.Fatalf("registered lifetime = %s, want 120s", expires)
	}
	if refresh.Before(before.Add(time.Minute)) || refresh.After(time.Now().Add(time.Minute)) {
		t.Fatalf("refresh scheduled at %s, want half of own 120s lifetime", refresh)
	}
}

func TestRegisterDoesNotCommitZeroContactLifetime(t *testing.T) {
	const contact = "<sip:current@192.0.2.10:5060;transport=tcp>"
	svc := &Service{cfg: &IMSConfig{Expires: time.Hour}}
	session := &registerSession{requestContact: contact}
	response := &sipResponse{Headers: map[string]string{
		"Contact": contact + ";expires=0", "Expires": "3600",
	}}
	if _, err := svc.commitRegisterSuccess(response, session); err == nil {
		t.Fatal("zero-lifetime binding was accepted as registered")
	}
	if session.expires != 0 || svc.regSession != nil {
		t.Fatal("invalid binding installed a registration lifetime")
	}
}

func replyWithOwnContactExpiry(conn *net.UDPConn) error {
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	buffer := make([]byte, 64*1024)
	n, remote, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return err
	}
	request := string(buffer[:n])
	var uri sip.Uri
	if _, err := sip.ParseAddressValue(sipHeaderValue(request, "Contact"), &uri, nil); err != nil {
		return err
	}
	headers := fmt.Sprintf("Expires: 3600\r\nContact: <sip:old@192.0.2.5:5060>;expires=7200\r\nContact: <%s>;expires=120\r\n", uri.String())
	_, err = conn.WriteToUDP([]byte(registerWireResponse(request, 200, headers)), remote)
	return err
}
