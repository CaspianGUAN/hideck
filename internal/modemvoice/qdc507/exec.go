package qdc507

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ExecOptions struct{ Program, StateDirectory string }
type Exec struct {
	program, directory string
	environment        []string
}

// NewExec is an infrastructure constructor, called only when this optional
// runtime is selected. It does not start an ADB daemon or contact a module.
func NewExec(options ExecOptions) (*Exec, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("qdc507: module voice audio requires Linux")
	}
	if !filepath.IsAbs(options.Program) || !filepath.IsAbs(options.StateDirectory) {
		return nil, fmt.Errorf("qdc507: absolute ADB and state paths are required")
	}
	program, err := exec.LookPath(options.Program)
	if err != nil {
		return nil, fmt.Errorf("qdc507: locate adb: %w", err)
	}
	if err := os.MkdirAll(options.StateDirectory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(options.StateDirectory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("qdc507: ADB state must be a private directory with mode 0700")
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "ADB_SERVER_SOCKET" || key == "ADB_LIBUSB" || key == "ANDROID_ADB_SERVER_PORT" {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "ADB_LIBUSB=0", "ADB_SERVER_SOCKET=localfilesystem:"+filepath.Join(options.StateDirectory, "adb.sock"))
	return &Exec{program: program, directory: options.StateDirectory, environment: environment}, nil
}

func (e *Exec) Execute(ctx context.Context, request Invocation) (string, error) {
	args := append([]string(nil), request.Args...)
	if request.File != nil {
		file, err := e.stage(request.File.Data)
		if err != nil {
			return "", err
		}
		defer os.Remove(file)
		args = append(args, file, request.File.Remote)
	}
	cmd := exec.CommandContext(ctx, e.program, args...)
	cmd.Env = e.environment
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return output.String(), err
}

func (e *Exec) stage(data []byte) (string, error) {
	f, err := os.CreateTemp(e.directory, "upload-")
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("qdc507: stage upload: %w", errors.Join(writeErr, closeErr))
	}
	return f.Name(), nil
}
