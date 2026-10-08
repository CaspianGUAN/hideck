package imscore

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestAKAIPPortConflictDetection(t *testing.T) {
	err := registrationResponseError(&sipResponse{
		StatusCode: 500,
		Reason:     "Server Internal Error",
		Headers: map[string]string{
			"Warning": `399 03024.02637.A.005 "AKA IP+Port conflict"`,
		},
	}, true)
	if !akaIPPortConflict(err) {
		t.Fatalf("conflict error = %v", err)
	}
	plain := registrationResponseError(&sipResponse{StatusCode: 500, Reason: "Server Internal Error"}, true)
	if akaIPPortConflict(plain) || akaIPPortConflict(nil) {
		t.Fatal("unrelated 500 was treated as an IP+port conflict")
	}
}

func TestAKAIPPortConflictReplacesSecurityAssociation(t *testing.T) {
	registrar, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer registrar.Close()
	serverResult := make(chan error, 1)
	go serveAKAIPPortConflictRegistrar(registrar, serverResult)

	svc, err := New(&IMSConfig{
		DeviceID: "dev-aka-port", IMSI: "520030393351967", IMPI: "520030393351967@ims.example",
		Domain: "ims.example", LocalIP: net.IPv4(127, 0, 0, 1), Transport: "udp",
		Registrar: registrar.LocalAddr().String(), IMSNetwork: NewSystemIMSNetwork(net.IPv4(127, 0, 0, 1)),
		AKAProvider: stubAKAProvider{}, EnableIPSec3GPP: disabledBoolPointer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.StopCurrent()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("initial Register: %v", err)
	}
	svc.mu.RLock()
	firstCallID := svc.callID
	svc.mu.RUnlock()
	if firstCallID == "" || !svc.IsRegistered() {
		t.Fatalf("initial registration call-id %q registered %t", firstCallID, svc.IsRegistered())
	}
	if err := svc.Register(ctx); err != nil {
		t.Fatalf("conflict Register: %v", err)
	}
	svc.mu.RLock()
	secondCallID := svc.callID
	svc.mu.RUnlock()
	if !svc.IsRegistered() || secondCallID == "" || secondCallID == firstCallID {
		t.Fatalf("replacement call-id %q previous %q registered %t", secondCallID, firstCallID, svc.IsRegistered())
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func serveAKAIPPortConflictRegistrar(conn *net.UDPConn, result chan<- error) {
	buffer := make([]byte, 64*1024)
	var established string
	conflictSent := false
	replaced := false
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if replaced {
				result <- nil
				return
			}
			result <- err
			return
		}
		request := string(buffer[:n])
		if !strings.HasPrefix(request, "REGISTER ") {
			continue
		}
		callID := sipHeaderValue(request, "Call-ID")
		if established != "" && callID == established {
			if conflictSent {
				result <- errors.New("refreshed the conflicting Call-ID again")
				return
			}
			conflictSent = true
			response := registerWireResponse(request, 500, "Warning: 399 pcscf.example \"AKA IP+Port conflict\"\r\n")
			if _, err := conn.WriteToUDP([]byte(response), remote); err != nil {
				result <- err
				return
			}
			continue
		}
		headers := digestChallengeHeader(0x11, 0x22)
		status := 401
		if !strings.Contains(sipHeaderValue(request, "Authorization"), `response=""`) {
			status = 200
			headers = "Expires: 1200\r\n"
			if established == "" {
				established = callID
			} else {
				replaced = true
			}
		}
		response := registerWireResponse(request, status, headers)
		if _, err := conn.WriteToUDP([]byte(response), remote); err != nil {
			result <- err
			return
		}
		if replaced {
			result <- nil
			return
		}
	}
}
