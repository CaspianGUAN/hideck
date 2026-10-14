package qdc507

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestModuleScriptsParse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX scripts execute on the module")
	}
	for name, script := range map[string]string{"compatibility": compatibilityScript, "install": installationStateScript, "load": loadScript, "calibrate": calibrationScript, "start": startRouteScript, "stop": stopRouteScript, "shutdown": stopCalibrationScript} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-n")
			cmd.Stdin = strings.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
}

func TestStaleProcessRecordDoesNotSignalReusedPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("module ownership uses Linux procfs")
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	path := filepath.Join(t.TempDir(), "owner")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d 1\n", child.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	script := ownershipFunctions + stopOwnedFunction + "\nrecord=" + quoteShell(path) + "\nprogram=sleep\nstop_owned\nkill -0 " + fmt.Sprint(child.Process.Pid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("stale PID was signalled: %v %s", err, out)
	}
}
