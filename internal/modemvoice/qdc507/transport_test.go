package qdc507

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSelectTransportUsesUSBWithMissingSerial(t *testing.T) {
	out := "List of devices attached\n(no serial number) device product:qdc usb:3-2.1 transport_id:9\n(no serial number) device usb:3-2.2 transport_id:10\n"
	target, err := selectTransport(out, "3-2.1")
	if err != nil || target.id != "9" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	for name, output := range map[string]string{
		"offline":    "(no serial number) offline usb:3-2.1 transport_id:9",
		"ambiguous":  out + "other device usb:3-2.1 transport_id:11\n",
		"missing ID": "serial device usb:3-2.1",
		"wrong USB":  "serial device usb:3-2.2 transport_id:9",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := selectTransport(output, "3-2.1"); err == nil {
				t.Fatal("accepted invalid transport")
			}
		})
	}
}

func TestSelectTransportExplainsMissingAndAmbiguousUSB(t *testing.T) {
	_, err := selectTransport("serial device usb:3-2.1 transport_id:1", "3-2.2")
	if err == nil || !strings.Contains(err.Error(), "USB 3-2.2 未找到 ADB 连接") {
		t.Fatalf("missing target diagnostic: %v", err)
	}
	output := "serial device usb:3-2.2 transport_id:1\nother device usb:3-2.2 transport_id:2"
	_, err = selectTransport(output, "3-2.2")
	if err == nil || !strings.Contains(err.Error(), "匹配到 2 个 ADB 连接") {
		t.Fatalf("ambiguous target diagnostic: %v", err)
	}
}

func TestShellStatusRequiresExactFinalStatus(t *testing.T) {
	for _, output := range []string{"OK", "\n" + shellStatus + "0\nextra", "\n" + shellStatus + "-1", "\n" + shellStatus + "256"} {
		if _, err := parseShell(output); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
	result, err := parseShell("insmod failed\r\n" + shellStatus + "1\r\n")
	if err != nil || result.Status != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := checked(result, nil); err == nil {
		t.Fatal("remote failure became success")
	}
}

type execFunc func(context.Context, Invocation) (string, error)

func (f execFunc) Execute(ctx context.Context, in Invocation) (string, error) { return f(ctx, in) }

func TestADBResolvesTransportAgainAndGuardsBoot(t *testing.T) {
	const boot = "11111111-2222-3333-4444-555555555555"
	id := "9"
	var commands [][]string
	exec := execFunc(func(_ context.Context, in Invocation) (string, error) {
		commands = append(commands, in.Args)
		if in.Args[0] == "devices" {
			return "(no serial number) device usb:3-2.1 transport_id:" + id, nil
		}
		return boot + "\n" + shellStatus + "0\n", nil
	})
	a, _ := NewADB(exec)
	target, err := a.Bind(context.Background(), "3-2.1")
	if err != nil {
		t.Fatal(err)
	}
	id = "15"
	_, err = a.Shell(context.Background(), target, "id -u")
	if err != nil {
		t.Fatal(err)
	}
	last := commands[len(commands)-1]
	if last[1] != "15" || !strings.Contains(last[3], boot) || !strings.Contains(last[3], "exit 75") {
		t.Fatalf("unsafe command %q", last)
	}
}

func TestADBDoesNotAcceptStatusAfterTransportFailure(t *testing.T) {
	a, _ := NewADB(execFunc(func(context.Context, Invocation) (string, error) {
		return "\n" + shellStatus + "0\n", errors.New("transport lost")
	}))
	if _, err := a.shell(context.Background(), transport{id: "2"}, "id"); err == nil {
		t.Fatal("ignored transport failure")
	}
}

func TestADBDeviceAdditionAndReenumerationNeverSelectAnotherUSB(t *testing.T) {
	const boot = "11111111-2222-3333-4444-555555555555"
	listing := "(no serial number) device usb:3-2.1 transport_id:9"
	var selected []string
	a, _ := NewADB(execFunc(func(_ context.Context, in Invocation) (string, error) {
		if in.Args[0] == "devices" {
			return listing, nil
		}
		selected = append(selected, in.Args[1])
		return boot + "\n" + shellStatus + "0\n", nil
	}))
	target, err := a.Bind(context.Background(), "3-2.1")
	if err != nil {
		t.Fatal(err)
	}
	// The other device appears first; both modules have the same empty serial.
	listing = "(no serial number) device usb:3-2.2 transport_id:10\n(no serial number) device usb:3-2.1 transport_id:15"
	if _, err := a.Shell(context.Background(), target, "id -u"); err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[1] != "15" {
		t.Fatalf("wrong transport selected: %v", selected)
	}
	listing = "(no serial number) device usb:3-2.2 transport_id:10"
	if _, err := a.Shell(context.Background(), target, "id -u"); err == nil {
		t.Fatal("missing USB silently used another module")
	}
	if len(selected) != 2 {
		t.Fatal("sent a command after original USB disappeared")
	}
}
