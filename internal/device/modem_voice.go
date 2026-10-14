package device

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/modemvoice/audio"
	"github.com/yibaiba/hideck/internal/modemvoice/host"
	"github.com/yibaiba/hideck/internal/modemvoice/media"
	"github.com/yibaiba/hideck/internal/modemvoice/qdc507"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (p *Pool) ModemVoiceController() *host.Controller { return p.modemVoiceCtl }

func (p *Pool) ModemVoiceStatus(id string) map[string]interface{} {
	if p.modemVoiceCtl == nil || !p.IsModemVoice(id) {
		return nil
	}
	return p.modemVoiceCtl.DeviceStatus(id)
}

func (p *Pool) IsModemVoice(id string) bool {
	w := p.GetWorker(id)
	return w != nil && IsModemVoiceMode(w.Config.PhoneMode) && PhoneServiceEnabled(w.Config)
}

func (p *Pool) newModemVoiceController() *host.Controller {
	return host.New(host.Options{Prepare: p.prepareModemVoice, Identity: func(id string) string {
		w := p.GetWorker(id)
		if w == nil {
			return ""
		}
		sim := currentSIMOwnership(w)
		return fmt.Sprintf("%p:%s:%d", w, sim.iccid, sim.generation)
	}, Listen: func() (net.PacketConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	}})
}

func (p *Pool) stopModemVoice(id string) error {
	if p.modemVoiceCtl == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	err := p.modemVoiceCtl.Disable(ctx, id)
	if err != nil {
		logger.Warn("模组直拨停止失败", "device", id, "err", err)
	}
	return err
}

func (p *Pool) reconcileModemVoice(w *Worker) error {
	if p.modemVoiceCtl == nil {
		return nil
	}
	if !p.IsModemVoice(w.ID) || w.Config.AirplaneEnabled {
		return p.stopModemVoice(w.ID)
	}
	if err := p.stopNativeVoLTEForModemVoice(w.ID); err != nil {
		return err
	}
	if err := p.StopSoftwareIMS(w.ID); err != nil {
		return err
	}
	p.modemVoiceCtl.Enable(p.Context(), w.ID)
	return nil
}

func (p *Pool) stopNativeVoLTEForModemVoice(id string) error {
	p.cancelNativeVoLTEStart(id)
	if p.volteCtl == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(p.Context(), 90*time.Second)
	defer cancel()
	return p.volteCtl.DisableForHandoff(ctx, id)
}

// This constructor is the infrastructure boundary. Optional tools and bundles
// are accessed only after the user explicitly selects modem_voice.
func (p *Pool) prepareModemVoice(ctx context.Context, id string) (*host.Resources, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("模组直拨音频目前仅支持 Linux")
	}
	w := p.GetWorker(id)
	if w == nil || w.CurrentICCID() == "" {
		return nil, errors.New("模组直拨需要已识别的 SIM 卡")
	}
	sim := currentSIMOwnership(w)
	port := &modemVoicePort{pool: p, worker: w, iccid: sim.iccid, identityGeneration: sim.generation}
	if err := port.check(ctx); err != nil {
		return nil, err
	}
	firmware, err := port.ExecuteATContext(ctx, "AT+CGMR", 5*time.Second)
	if err != nil {
		return nil, err
	}
	if !hasFirmware(firmware, "QDC507GLEFM21") {
		return nil, errors.New("当前固件没有已验证的模组直拨音频适配器")
	}
	adb, err := qdc507.FindADB()
	if err != nil {
		return nil, err
	}
	capture, err := exec.LookPath("arecord")
	if err != nil {
		return nil, errors.New("未找到 arecord，请安装 alsa-utils 后重试")
	}
	playback, err := exec.LookPath("aplay")
	if err != nil {
		return nil, errors.New("未找到 aplay，请安装 alsa-utils 后重试")
	}
	root, err := filepath.Abs(filepath.Join("data", "modem-voice"))
	if err != nil {
		return nil, err
	}
	bundle, err := qdc507.EmbeddedBundle()
	if err != nil {
		return nil, err
	}
	return prepareQDC507(ctx, port, qdc507Setup{adb: adb, state: filepath.Join(root, "adb"),
		bundle: bundle, capture: capture, playback: playback})
}

type qdc507Setup struct {
	adb, state, capture, playback string
	bundle                        fs.FS
}

func prepareQDC507(ctx context.Context, port *modemVoicePort, setup qdc507Setup) (*host.Resources, error) {
	executor, err := qdc507.NewExec(qdc507.ExecOptions{Program: setup.adb, StateDirectory: setup.state})
	if err != nil {
		return nil, err
	}
	client, err := qdc507.NewADB(executor)
	if err != nil {
		return nil, err
	}
	if err := port.prepareADB(ctx, client, setup.state); err != nil {
		return nil, err
	}
	manager, err := qdc507.NewManager(qdc507.Options{USB: filepath.Base(port.worker.Config.USBPath),
		Firmware: "QDC507GLEFM21", Client: client, Source: setup.bundle, Check: port.check})
	if err != nil {
		return nil, err
	}
	resources := &host.Resources{Port: port, Check: port.checkIdentity, CanCall: port.check, Close: manager.Shutdown,
		Route: nil}
	alsa := audio.ALSA{CaptureProgram: setup.capture, PlaybackProgram: setup.playback}
	resources.Route = func() (media.AudioRoute, error) {
		return manager.AudioRoute(qdc507.AudioOptions{Resolve: func() (audio.Endpoint, error) {
			return audio.ResolveUSBPCM(port.worker.Config.USBPath)
		}, OpenPCM: alsa.Open})
	}
	_, err = manager.Prepare(ctx)
	return resources, err
}

func hasFirmware(response, firmware string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(response, "\r", ""), "\n") {
		if strings.TrimSpace(line) == firmware {
			return true
		}
	}
	return false
}
