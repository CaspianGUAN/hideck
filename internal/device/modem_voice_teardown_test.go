package device

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/cardpolicy"
	modemhost "github.com/yibaiba/hideck/internal/modemvoice/host"
)

func testModemVoicePort(t *testing.T) (*Pool, *modemVoicePort) {
	t.Helper()
	p, w := atTestPool(t)
	w.Config.PhoneMode, w.Config.VoWiFiEnabled = PhoneModeModemVoice, true
	w.state.Identity.ICCID, w.state.Identity.IMSI = "test-sim", "460011234567890"
	return p, &modemVoicePort{pool: p, worker: w, iccid: "test-sim"}
}

func TestModemVoiceTeardownCommandsSurvivePolicyChange(t *testing.T) {
	p, port := testModemVoicePort(t)
	port.worker.Config.PhoneMode, port.worker.Config.VoWiFiEnabled = PhoneModeWiFi, false
	port.worker.Config.AirplaneEnabled = true
	var commands atomic.Int32
	p.openATSession = func(string) (atSerialSession, error) {
		return &controlledATSession{execute: func(string, time.Duration) (string, error) {
			commands.Add(1)
			return "OK", nil
		}}, nil
	}
	for _, command := range []string{"AT+CLCC", "AT+CHUP"} {
		if _, err := port.ExecuteATContext(context.Background(), command, time.Second); err != nil {
			t.Fatal(command, err)
		}
	}
	for _, command := range []string{"ATD10010;", "ATA", "AT+CFUN=1"} {
		if _, err := port.ExecuteATContext(context.Background(), command, time.Second); err == nil {
			t.Fatal("policy bypass", command)
		}
	}
	if commands.Load() != 2 {
		t.Fatal("unexpected modem writes", commands.Load())
	}
}

func TestModemVoiceCleanupCannotTouchReplacementSIM(t *testing.T) {
	p, port := testModemVoicePort(t)
	port.worker.state.Identity.ICCID = "replacement"
	p.openATSession = func(string) (atSerialSession, error) {
		t.Fatal("opened port for replacement SIM")
		return nil, errors.New("unexpected open")
	}
	for _, command := range []string{"AT+CLCC", "AT+CHUP", "ATA"} {
		if _, err := port.ExecuteATContext(context.Background(), command, time.Second); err == nil {
			t.Fatal("SIM guard bypass", command)
		}
	}
}

func TestModemVoiceFailedStopDoesNotProjectNewPolicy(t *testing.T) {
	p, port := testModemVoicePort(t)
	var hangupFailed, ended atomic.Bool
	hangupFailed.Store(true)
	p.openATSession = func(string) (atSerialSession, error) {
		return &controlledATSession{execute: func(command string, _ time.Duration) (string, error) {
			switch command {
			case "AT+CLCC":
				if ended.Load() {
					return "OK", nil
				}
				return "+CLCC: 3,1,4,0,0,\"10010\",129\r\nOK", nil
			case "AT+CHUP":
				if hangupFailed.Load() {
					return "", context.DeadlineExceeded
				}
				ended.Store(true)
				return "OK", nil
			default:
				return "", errors.New("unexpected command")
			}
		}}, nil
	}
	p.modemVoiceCtl = modemhost.New(modemhost.Options{
		Listen: func() (net.PacketConn, error) { return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) },
		Prepare: func(context.Context, string) (*modemhost.Resources, error) {
			return &modemhost.Resources{Port: port, Check: port.checkIdentity, CanCall: port.check}, nil
		},
	})
	p.modemVoiceCtl.Enable(p.Context(), port.worker.ID)
	t.Cleanup(func() {
		hangupFailed.Store(false)
		if err := p.stopModemVoice(port.worker.ID); err != nil {
			t.Error(err)
		}
	})
	deadline := time.After(2 * time.Second)
	for p.modemVoiceCtl.ActiveCall(port.worker.ID) == nil {
		select {
		case <-deadline:
			t.Fatal("incoming call unavailable")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	p.SetPolicyResolver(&stubPolicyResolver{pol: cardpolicy.Policy{ICCID: port.iccid, PhoneMode: PhoneModeWiFi}})
	result := p.resolveAndApplyPolicy(port.worker, "test_mode_change")
	if result.Applied || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("result: %+v", result)
	}
	if port.worker.Config.PhoneMode != PhoneModeModemVoice || !port.worker.Config.VoWiFiEnabled || p.modemVoiceCtl.ActiveCall(port.worker.ID) == nil {
		t.Fatal("failed hangup lost the old policy or call")
	}
}
