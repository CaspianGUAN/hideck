package modem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	fileUploadIdleTimeout = 10 * time.Second
	fileUploadChunkBytes  = 1024
	fileUploadBaud        = 115200
)

var ErrUploadSynchronization = errors.New("文件传输后未确认 AT 命令模式，需要恢复控制通道")

type uploadIO struct {
	write       func([]byte) (int, error)
	line        func(time.Time) (string, error)
	unsolicited func(string)
}

func writeUploadBytes(write func([]byte) (int, error), data []byte) error {
	for len(data) > 0 {
		chunk := data
		if len(chunk) > fileUploadChunkBytes {
			chunk = chunk[:fileUploadChunkBytes]
		}
		n, err := write(chunk)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// The queue owns CONNECT through the terminal result. Cancellation after the
// command is sent cannot release the port into the middle of transparent mode.
func uploadFile(wire uploadIO, name string, data []byte) error {
	if !strings.HasPrefix(name, "RAM:hideck-") || strings.ContainsAny(name, "\"\\\r\n\x00") || len(data) == 0 {
		return errors.New("无效文件上传请求")
	}
	deadline := time.Now().Add(time.Duration(len(data))*20*time.Second/fileUploadBaud + 30*time.Second)
	command := fmt.Sprintf("AT+QFUPL=\"%s\",%d,%d\r\n", name, len(data), int(fileUploadIdleTimeout/time.Second))
	if err := writeUploadBytes(wire.write, []byte(command)); err != nil {
		return errors.Join(ErrUploadSynchronization, err)
	}
	sent, confirmed := false, false
	var resultErr error
	for {
		line, err := wire.line(deadline)
		if err != nil {
			return errors.Join(ErrUploadSynchronization, err)
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "CONNECT":
			if sent {
				return errors.Join(ErrUploadSynchronization, errors.New("重复 CONNECT"))
			}
			if err := writeUploadBytes(wire.write, data); err != nil {
				return errors.Join(ErrUploadSynchronization, err)
			}
			sent = true
		case strings.HasPrefix(line, "+QFUPL:"):
			fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+QFUPL:")), ",")
			if len(fields) != 2 {
				resultErr = errors.New("上传结果格式无效")
				continue
			}
			size, err := strconv.Atoi(strings.TrimSpace(fields[0]))
			if err != nil || size != len(data) {
				resultErr = errors.New("上传长度不匹配")
				continue
			}
			checksum, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(fields[1]), "0x"), 16, 16)
			if err != nil || uint16(checksum) != uploadChecksum(data) {
				resultErr = errors.New("上传校验和不匹配")
				continue
			}
			confirmed = true
		case line == "OK":
			if resultErr != nil {
				return resultErr
			}
			if !sent || !confirmed {
				return errors.New("缺少文件上传完成凭据")
			}
			return nil
		case line == "ERROR" || strings.HasPrefix(line, "+CME ERROR"):
			return fmt.Errorf("模组拒绝文件上传: %s", line)
		default:
			if wire.unsolicited != nil {
				wire.unsolicited(line)
			}
		}
	}
}

// EC2x FILE Application Note: XOR big-endian words; pad an odd last byte low with zero.
func uploadChecksum(data []byte) uint16 {
	var result uint16
	for i, b := range data {
		if i%2 == 0 {
			result ^= uint16(b) << 8
		} else {
			result ^= uint16(b)
		}
	}
	return result
}

func waitUploadIdle(err error) {
	if errors.Is(err, ErrUploadSynchronization) {
		// Do not let the next serial owner send AT into a partially written file.
		// QFUPL exits transparent mode after its advertised no-input timeout.
		timer := time.NewTimer(fileUploadIdleTimeout + time.Second)
		defer timer.Stop()
		<-timer.C
	}
}

func (s *SerialAT) UploadFile(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.port.SetReadTimeout(200 * time.Millisecond); err != nil {
		return err
	}
	var pending string
	wire := uploadIO{write: s.port.Write, line: func(deadline time.Time) (string, error) {
		var buf [1024]byte
		for time.Now().Before(deadline) {
			if index := strings.IndexByte(pending, '\n'); index >= 0 {
				line := pending[:index]
				pending = pending[index+1:]
				return line, nil
			}
			n, err := s.port.Read(buf[:])
			if err != nil {
				return "", err
			}
			pending += string(buf[:n])
		}
		return "", context.DeadlineExceeded
	}}
	err := uploadFile(wire, name, data)
	waitUploadIdle(err)
	return errors.Join(err, ctx.Err())
}

func (m *Manager) UploadFile(ctx context.Context, name string, data []byte) error {
	if m == nil || !m.CanExecuteAT() || !m.IsHealthy() {
		return errors.New("AT 管理器不可用")
	}
	req := commandRequest{ctx: ctx, cmd: name, upload: append([]byte(nil), data...), respChan: make(chan string, 1), errChan: make(chan error, 1)}
	// Join the queue result even after caller cancellation; do not release an
	// outer device lease until this transfer has exited transparent mode.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stop:
		return errors.New("AT 管理器停止")
	case m.cmdChan <- req:
	}
	select {
	case err := <-req.errChan:
		return err
	case <-req.respChan:
		return ctx.Err()
	case <-m.stopped:
		return errors.New("AT 管理器停止")
	}
}

func (m *Manager) handleFileUpload(req commandRequest) {
	wire := uploadIO{write: m.port.Write, unsolicited: func(line string) {
		if m.isURC(line) {
			m.handleURC(line)
		}
	}, line: func(deadline time.Time) (string, error) {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-timer.C:
			return "", context.DeadlineExceeded
		case <-m.stop:
			return "", errors.New("AT 管理器停止")
		case msg := <-m.rxChan:
			return msg.Data, msg.Err
		}
	}}
	err := uploadFile(wire, req.cmd, req.upload)
	waitUploadIdle(err)
	if errors.Is(err, ErrUploadSynchronization) {
		m.markUnhealthy()
		m.Stop()
		m.notifyDisconnect("file_upload_unsynchronized")
	}
	if err != nil {
		req.errChan <- err
	} else {
		req.respChan <- ""
	}
}
