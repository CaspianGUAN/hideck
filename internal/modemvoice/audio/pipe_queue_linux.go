package audio

import (
	"golang.org/x/sys/unix"
	"os"
)

func queuedPipeBytes(pipe *os.File) (int, error) {
	var n int
	var queryErr error
	conn, err := pipe.SyscallConn()
	if err != nil {
		return 0, err
	}
	err = conn.Control(func(fd uintptr) {
		n, queryErr = unix.IoctlGetInt(int(fd), unix.TIOCINQ)
	})
	if err != nil {
		return 0, err
	}
	return n, queryErr
}
