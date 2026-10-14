//go:build !linux

package audio

import "os"

// ALSA.Open is Linux-only. Preserve the process adapter's portable test helper.
func queuedPipeBytes(pipe *os.File) (int, error) { return 0, nil }
