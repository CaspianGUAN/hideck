package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func addPCM(t *testing.T, root string, card, device int, dirs string) string {
	t.Helper()
	path := filepath.Join(root, filepath.Base(root)+":1.7", "sound", fmt.Sprintf("card%d", card))
	for _, direction := range dirs {
		if err := os.MkdirAll(filepath.Join(path, fmt.Sprintf("pcmC%dD%d%c", card, device, direction)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestUSBPCMResolvesRenumberingAndRequiresDuplex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "3-2.1")
	old := addPCM(t, root, 2, 0, "p")
	if _, err := ResolveUSBPCM(root); err == nil {
		t.Fatal("capture-less card accepted")
	}
	addPCM(t, root, 2, 0, "c")
	first, err := ResolveUSBPCM(root)
	if err != nil || first.ALSADevice() != "hw:2,0" {
		t.Fatalf("%+v %v", first, err)
	}
	if err := os.Rename(old, filepath.Join(t.TempDir(), "old-card")); err != nil {
		t.Fatal(err)
	}
	addPCM(t, root, 7, 1, "pc")
	next, err := ResolveUSBPCM(root)
	if err != nil || next.ALSADevice() != "hw:7,1" {
		t.Fatalf("stale card: %+v %v", next, err)
	}
	addPCM(t, root, 8, 0, "pc")
	if _, err := ResolveUSBPCM(root); err == nil {
		t.Fatal("ambiguous card silently selected")
	}
}

func TestUSBPCMRejectsCardFromAnotherDevice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "3-2.1")
	other := filepath.Join(t.TempDir(), "3-2.2")
	card := addPCM(t, other, 2, 0, "pc")
	sound := filepath.Join(root, filepath.Base(root)+":1.7", "sound")
	if err := os.MkdirAll(sound, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(card, filepath.Join(sound, "card2")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveUSBPCM(root); err == nil {
		t.Fatal("cross-device audio accepted")
	}
}
