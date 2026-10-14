package device

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/backend"
	"github.com/yibaiba/hideck/internal/modem"
)

var ErrATUnavailable = errors.New("AT 控制不可用")

type ATRequest struct {
	DeviceID string
	Command  string
	Timeout  time.Duration
}

type atSerialSession interface {
	Execute(string, time.Duration) (string, error)
	Close() error
}

func openDeviceATSession(port string) (atSerialSession, error) {
	return modem.NewSerialAT(port, 115200, 8, 1, "N")
}

// ExecuteAT retains the native VoLTE host contract. Its transient requests share
// the device gate with the HTTP terminal and future voice runtime owner.
func (p *Pool) ExecuteAT(deviceID, command string, timeout time.Duration) (string, error) {
	return p.ExecuteATContext(p.Context(), ATRequest{DeviceID: deviceID, Command: command, Timeout: timeout})
}

func (p *Pool) ExecuteATContext(ctx context.Context, request ATRequest) (string, error) {
	releaseOperation, err := p.AcquireDeviceOperation(request.DeviceID)
	if err != nil {
		return "", err
	}
	defer releaseOperation()
	if ctx == nil || request.Timeout <= 0 || strings.TrimSpace(request.Command) == "" || strings.ContainsAny(request.Command, "\r\n\x00") {
		return "", fmt.Errorf("%w: 无效的 AT 请求", ErrATUnavailable)
	}
	worker := p.GetWorker(strings.TrimSpace(request.DeviceID))
	if worker == nil {
		return "", fmt.Errorf("%w: 设备未找到或未运行", ErrATUnavailable)
	}
	iccid := worker.CurrentICCID()
	release, err := p.acquireDeviceAT(ctx, worker.ID)
	if err != nil {
		return "", err
	}
	defer release()
	if p.GetWorker(worker.ID) != worker || worker.CurrentICCID() != iccid {
		return "", fmt.Errorf("%w: 排队期间设备或 SIM 已更换", ErrATUnavailable)
	}
	select {
	case <-worker.stop:
		return "", fmt.Errorf("%w: 设备正在停止", ErrATUnavailable)
	default:
	}
	return p.executeWorkerAT(ctx, worker, request)
}

func (p *Pool) executeWorkerAT(ctx context.Context, worker *Worker, request ATRequest) (string, error) {
	if worker.Modem.OwnsATRuntime() {
		if !worker.Modem.CanExecuteAT() || !worker.Modem.IsHealthy() {
			return "", fmt.Errorf("%w: AT 管理器未启动或不可用", ErrATUnavailable)
		}
		return worker.Modem.ExecuteATContext(ctx, request.Command, request.Timeout)
	}
	mode := resolvedBackendMode(worker.Config)
	if worker.Backend != nil {
		mode = worker.Backend.Mode()
	}
	if mode != backend.BackendQMI {
		return "", fmt.Errorf("%w: 当前设备没有可用 AT 管理器", ErrATUnavailable)
	}
	port := worker.ResolvedATPort()
	if port == "" {
		return "", fmt.Errorf("%w: 当前设备没有可用 AT 端口", ErrATUnavailable)
	}
	if p.openATSession == nil {
		return "", fmt.Errorf("%w: 未配置 AT 串口适配器", ErrATUnavailable)
	}
	session, err := p.openATSession(port)
	if err != nil {
		return "", fmt.Errorf("打开 AT 端口 %s 失败: %w", port, err)
	}
	// Do not release the gate while a written command is still waiting for its
	// final response. Cancellation prevents queued writes; it cannot retract ATD.
	response, commandErr := executeTransientAT(ctx, session, request)
	return response, errors.Join(commandErr, session.Close())
}

func executeTransientAT(ctx context.Context, session atSerialSession, request ATRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	response, err := session.Execute(request.Command, request.Timeout)
	return response, errors.Join(err, ctx.Err())
}

func (p *Pool) acquireDeviceAT(ctx context.Context, deviceID string) (func(), error) {
	p.atPortMu.Lock()
	if p.atPortLocks == nil {
		p.atPortLocks = make(map[string]chan struct{})
	}
	gate := p.atPortLocks[deviceID]
	if gate == nil {
		gate = make(chan struct{}, 1)
		p.atPortLocks[deviceID] = gate
	}
	p.atPortMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case gate <- struct{}{}:
	}
	if err := ctx.Err(); err != nil {
		<-gate
		return nil, err
	}
	return func() { <-gate }, nil
}
