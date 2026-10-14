package qdc507

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestADBStateDirectoryMustNotExposeRootSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ADB infrastructure")
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "public-state")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExec(ExecOptions{Program: program, StateDirectory: dir}); err == nil {
		t.Fatal("public ADB socket directory accepted")
	}
}
