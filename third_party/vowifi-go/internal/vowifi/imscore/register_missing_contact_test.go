package imscore

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRegisterMissingOwnContactNeverConfirmsRegistration(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		for _, contact := range []string{"", "<sip:another@192.0.2.20:5060>;expires=3600"} {
			t.Run(testMissingContactName(refresh, contact), func(t *testing.T) {
				service, err := New(registerTransportTestConfig("udp", "127.0.0.1:5060"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(service.StopCurrent)
				if refresh {
					client, server := net.Pipe()
					t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
					service.registrationTCP = client
					service.registrationTransport = "tcp"
					service.regState = regRegistered
					service.transitionRegStatus(registrationRegistered)
				}
				service.transport.SetSendFn(func(request string) error {
					service.mu.Lock()
					service.lastPingAt = time.Now()
					service.mu.Unlock()
					service.lastPingOK.Store(true)
					service.transport.DeliverResponse(registerResponseForRequest(request, 200,
						map[string]string{"Contact": contact, "Expires": "3600"}))
					return nil
				})
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := service.Register(ctx); !errors.Is(err, errRegisterContactMissing) {
					t.Fatalf("Register = %v, want missing Contact error", err)
				}
				if service.IsRegistered() || service.RegState() != regFailed || !service.lastRegisterOKAt.IsZero() {
					t.Fatal("response without own Contact confirmed registration")
				}
			})
		}
	}
}

func testMissingContactName(refresh bool, contact string) string {
	name := "initial"
	if refresh {
		name = "refresh_with_keepalive"
	}
	if contact == "" {
		return name + "/empty"
	}
	return name + "/other_binding"
}

func TestOutboundRefreshMissingContactDoesNotKeepInitialBinding(t *testing.T) {
	service, err := New(registerTransportTestConfig("tcp", "127.0.0.1:5060"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.StopCurrent)
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	service.registrationTCP = client
	service.registrationTransport = "tcp"
	attempts := 0
	service.transport.SetSendFn(func(request string) error {
		attempts++
		headers := map[string]string{"Expires": "3600", "Path": "<sip:pcscf.example;lr;ob>"}
		if attempts > 1 {
			headers["Contact"] = ""
		}
		service.transport.DeliverResponse(registerResponseForRequest(request, 200, headers))
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Register(ctx); !errors.Is(err, errRegisterContactMissing) {
		t.Fatalf("Register = %v, want missing Contact error", err)
	}
	if attempts != 2 || service.IsRegistered() || service.RegState() != regFailed {
		t.Fatalf("optional outbound refresh kept invalid binding: attempts=%d, state=%s", attempts, service.RegState())
	}
}
