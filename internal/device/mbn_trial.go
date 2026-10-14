package device

import (
	"context"
	"errors"
	"fmt"

	"github.com/yibaiba/hideck/internal/backend"
	"github.com/yibaiba/hideck/internal/mbn"
)

func (s *mbnDeviceSession) Prepare(ctx context.Context, request mbn.Request) error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	defer t.Unlock()
	if err := s.validateLocked(); err != nil {
		return err
	}
	if request.Kind == "import" {
		return nil
	}
	s.goal = request.Goal
	if request.Goal == "ims" && (!request.AllowNativeIMS || !IsNativeVoLTEMode(s.worker.Config.PhoneMode)) {
		return errors.New("原生 IMS 试配需要已处于 VoLTE 模式并明确授权")
	}
	if request.Goal == "data" && (!request.AllowData || s.probe == nil || s.worker.NetworkController() == nil) {
		return errors.New("蜂窝数据验证尚未授权或没有数据接口")
	}
	class, err := ClassifyWorkerLebaraUK(s.worker)
	if err != nil {
		return err
	}
	if shouldSuppressCellularRadio(s.worker.Config) || class.IsLebara || class.BlocksVoWiFi() {
		return errors.New("当前飞行/运营商射频锁禁止重启驻网；请先明确调整设备模式，MBN 操作不会解锁")
	}
	if err := s.pool.StopSoftwareIMS(s.owner.DeviceID); err != nil {
		return err
	}
	if err := s.pool.stopModemVoice(s.owner.DeviceID); err != nil {
		return err
	}
	if s.pool.volteCtl != nil {
		s.pool.stopNativeVoLTE(s.owner.DeviceID, "mbn_operation")
	}
	if network := s.worker.NetworkController(); network != nil && network.IsConnected() {
		return s.worker.StopNetwork()
	}
	return nil
}

func (s *mbnDeviceSession) Registration(ctx context.Context) (mbn.Registration, error) {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	defer t.Unlock()
	if err := s.validateLocked(); err != nil {
		return mbn.Registration{}, err
	}
	w := s.worker
	class, err := ClassifyWorkerLebaraUK(w)
	if err != nil {
		return mbn.Registration{}, err
	}
	if shouldSuppressCellularRadio(w.Config) || class.IsLebara || class.BlocksVoWiFi() {
		return mbn.Registration{}, errors.New("飞行/运营商射频策略禁止驻网试配；未修改射频策略")
	}
	if controller, ok := w.Backend.(backend.OperatingModeController); ok {
		mode, err := controller.GetOperatingMode(ctx)
		if err != nil {
			return mbn.Registration{}, err
		}
		if mode != backend.ModeOnline {
			if err := controller.SetOperatingMode(ctx, backend.ModeOnline); err != nil {
				return mbn.Registration{}, err
			}
		}
	}
	serving, err := w.Backend.GetServingSystem(ctx)
	if err != nil {
		return mbn.Registration{}, err
	}
	if serving == nil {
		return mbn.Registration{}, errors.New("未返回驻网状态")
	}
	r := mbn.Registration{Registered: serving.RegStatus == 1 || serving.RegStatus == 5, Attached: serving.PSAttached, PLMN: fmt.Sprintf("%03d%02d", serving.MCC, serving.MNC)}
	if s.goal == "ims" {
		if s.imsWorker != w {
			if err := s.pool.EnsureIMSClients(ctx, w.ID); err != nil {
				return r, err
			}
			if err := s.pool.SetNativeIMS(ctx, w.ID, true); err != nil {
				return r, err
			}
			s.imsWorker = w
		}
		status, err := s.pool.IMSAStatus(ctx, w.ID)
		if err != nil {
			return r, err
		}
		r.IMS = status.Registered
	}
	return r, nil
}

func (s *mbnDeviceSession) Probe(ctx context.Context, target string) error {
	t := s.pool.policyTransitionFor(s.owner.DeviceID)
	t.Lock()
	defer t.Unlock()
	if err := s.validateLocked(); err != nil {
		return err
	}
	if s.probe == nil || s.goal != "data" {
		return errors.New("未授权数据探测")
	}
	network := s.worker.NetworkController()
	if network == nil {
		return errors.New("没有可用数据接口")
	}
	if !network.IsConnected() {
		if err := network.Connect(); err != nil {
			return err
		}
	}
	if s.worker.Config.Interface == "" {
		return errors.New("无法确定蜂窝数据接口，拒绝使用主机默认网络验证")
	}
	return s.probe(ctx, s.worker.Config.Interface, target)
}
