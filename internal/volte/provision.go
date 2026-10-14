package volte

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SnapshotSchema    = "hideck.volte.provision.v1"
	DefaultBackupDir  = "data/volte-provision"
	defaultATTimeout  = 8 * time.Second
	defaultWaitReenum = 45 * time.Second

	StageIdentity        = "identity"
	StageSnapshot        = "snapshot"
	StageApplyIMS        = "apply_ims"
	StageApplyUSBCFG     = "apply_usbcfg"
	StageApplyMBN        = "apply_mbn"
	StageVerify          = "verify"
	StageReenumerate     = "reenumerate"
	StageRestoreIdentity = "restore_identity"
	StageRestoreWrite    = "restore_write"
	StageRestoreVerify   = "restore_verify"
)

var (
	ErrRebootRequired = errors.New("volte provision: reboot required before verify")
	ErrFieldDrift     = errors.New("volte provision: field drift after write")
	ErrIMEIMismatch   = errors.New("volte provision: IMEI mismatch")
	ErrVIDPIDMismatch = errors.New("volte provision: VID/PID mismatch")
	ErrNotRestored    = errors.New("volte provision: not restored")
)

type ATTransport interface {
	ExecuteAT(deviceID, cmd string, timeout time.Duration) (string, error)
}

type ReenumerateWaiter interface {
	WaitByIMEI(ctx context.Context, imei string, timeout time.Duration) (deviceID string, err error)
}

type Desired struct {
	IMSEnabled   *bool
	VoLTEEnabled *bool
	UACEnabled   *bool
	MBNAutoSel   *bool
	MBNSelect    *string
	MCC          string
	MNC          string
}

type VoiceSettings struct {
	IMSEnabled   bool     `json:"ims_enabled"`
	VoLTEEnabled bool     `json:"volte_enabled"`
	USBFields    []string `json:"usbcfg_fields"`
	UACEnabled   bool     `json:"uac_enabled"`
	VID          string   `json:"vid"`
	PID          string   `json:"pid"`
	MBNAutoSel   bool     `json:"mbn_autosel"`
	MBNName      string   `json:"mbn_name"`
	MBNIndex     int      `json:"mbn_index"`
}

type Snapshot struct {
	Schema     string        `json:"schema"`
	CapturedAt string        `json:"captured_at"`
	IMEI       string        `json:"imei"`
	VID        string        `json:"vid"`
	PID        string        `json:"pid"`
	Original   VoiceSettings `json:"original"`
	Target     VoiceSettings `json:"target"`
	DeviceHint string        `json:"device_id_hint,omitempty"`
}

type Result struct {
	Stage          string
	DeviceID       string
	IMEITail       string
	SnapshotPath   string
	Original       VoiceSettings
	Current        VoiceSettings
	Target         VoiceSettings
	Written        []string
	Drift          []string
	RebootRequired bool
	Verified       bool
	Restored       bool
	OK             bool
}

type StageError struct {
	Stage    string
	DeviceID string
	Message  string
	Result   Result
	Err      error
}

func (e *StageError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("volte provision %s: %s: %v", e.Stage, e.Message, e.Err)
	}
	return fmt.Sprintf("volte provision %s: %s", e.Stage, e.Message)
}

func (e *StageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ApplyOptions struct {
	WaitReenumerate time.Duration
	Reboot          bool
}

type Provisioner struct {
	at     ATTransport
	store  SnapshotStore
	waiter ReenumerateWaiter
	now    func() time.Time
	opts   ApplyOptions
}

func NewProvisioner(at ATTransport, store SnapshotStore) *Provisioner {
	if store == nil {
		store = &FileStore{Dir: DefaultBackupDir}
	}
	return &Provisioner{
		at:    at,
		store: store,
		now:   time.Now,
		opts:  ApplyOptions{WaitReenumerate: 0, Reboot: false},
	}
}

func (p *Provisioner) WithWaiter(w ReenumerateWaiter) *Provisioner {
	if p != nil {
		p.waiter = w
	}
	return p
}

func (p *Provisioner) WithOptions(opts ApplyOptions) *Provisioner {
	if p != nil {
		p.opts = opts
	}
	return p
}

func (p *Provisioner) Ensure(ctx context.Context, deviceID string, desired Desired) (Result, error) {
	res, snap, err := p.capture(ctx, deviceID, desired)
	if err != nil {
		return res, err
	}
	return p.apply(ctx, deviceID, snap, res.Current, true, false)
}

func (p *Provisioner) Restore(ctx context.Context, deviceID string) (Result, error) {
	res := Result{DeviceID: deviceID, Stage: StageRestoreIdentity, Restored: false}
	current, imei, _, err := p.readSettings(deviceID)
	if err != nil {
		return res, p.fail(res, StageRestoreIdentity, "read live identity", err)
	}
	res.IMEITail = IMEITail(imei)
	res.Current = current
	snap, path, err := p.store.Load(imei)
	if err != nil {
		return res, p.fail(res, StageRestoreIdentity, "load snapshot", err)
	}
	res.SnapshotPath = path
	if host, ok := p.at.(interface{ MBNManaged(string) (bool, error) }); ok {
		managed, err := host.MBNManaged(deviceID)
		if err != nil {
			return res, err
		}
		if managed {
			snap.Original.MBNName = current.MBNName
			snap.Original.MBNAutoSel = current.MBNAutoSel
			snap.Original.MBNIndex = current.MBNIndex
		}
	}
	res.Original = snap.Original
	res.Target = snap.Original
	if snap.IMEI != imei {
		return res, p.fail(res, StageRestoreIdentity, "snapshot IMEI does not match live module", ErrIMEIMismatch)
	}
	if canonHexID(snap.VID) != canonHexID(current.VID) || canonHexID(snap.PID) != canonHexID(current.PID) {
		return res, p.fail(res, StageRestoreIdentity, "snapshot VID/PID does not match live module", ErrVIDPIDMismatch)
	}
	applied, err := p.apply(ctx, deviceID, snap.withTarget(snap.Original), current, true, true)
	if err != nil {
		applied.Restored = false
		stage := applied.Stage
		if stage == StageApplyIMS || stage == StageApplyUSBCFG || stage == StageApplyMBN {
			stage = StageRestoreWrite
		} else if stage == StageVerify || stage == StageReenumerate {
			stage = StageRestoreVerify
		}
		applied.Stage = stage
		return applied, p.fail(applied, stage, "restore did not complete", err)
	}
	if !settingsEqual(applied.Current, snap.Original) {
		applied.Stage = StageRestoreVerify
		applied.Restored = false
		applied.OK = false
		applied.Drift = diffSettings(applied.Current, snap.Original)
		return applied, p.fail(applied, StageRestoreVerify, "restored values do not match snapshot original", ErrFieldDrift)
	}
	applied.Restored = true
	applied.OK = true
	applied.Stage = StageRestoreVerify
	return applied, nil
}

func (s Snapshot) withTarget(target VoiceSettings) Snapshot {
	s.Target = target
	return s
}

func (p *Provisioner) capture(ctx context.Context, deviceID string, desired Desired) (Result, Snapshot, error) {
	_ = ctx
	res := Result{DeviceID: deviceID, Stage: StageIdentity, Restored: false}
	if host, ok := p.at.(interface{ MBNManaged(string) (bool, error) }); ok {
		managed, err := host.MBNManaged(deviceID)
		if err != nil {
			return res, Snapshot{}, err
		}
		if managed {
			desired.MCC = ""
			desired.MNC = ""
			desired.MBNSelect = nil
			desired.MBNAutoSel = nil
		}
	}
	current, imei, entries, err := p.readSettings(deviceID)
	if err != nil {
		return res, Snapshot{}, p.fail(res, StageIdentity, "read module identity", err)
	}
	res.IMEITail = IMEITail(imei)
	res.Current = current
	res.Original = current
	if strings.TrimSpace(desired.MCC) != "" && desired.MBNSelect == nil {
		name, mbnErr := UniqueMBN(desired.MCC, desired.MNC, entries)
		if mbnErr != nil {
			return res, Snapshot{}, p.fail(res, StageIdentity, "select MBN", mbnErr)
		}
		desired.MBNSelect = &name
		if desired.MBNAutoSel == nil {
			desired.MBNAutoSel = boolPtr(false)
		}
	}
	target := overlayDesired(current, desired)
	if canonHexID(target.VID) != canonHexID(current.VID) || canonHexID(target.PID) != canonHexID(current.PID) {
		return res, Snapshot{}, p.fail(res, StageIdentity, "refusing VID/PID change", ErrVIDPIDMismatch)
	}
	res.Target = target
	res.Stage = StageSnapshot
	now := p.now().UTC().Format(time.RFC3339)
	existing, path, loadErr := p.store.Load(imei)
	snap := Snapshot{
		Schema:     SnapshotSchema,
		CapturedAt: now,
		IMEI:       imei,
		VID:        current.VID,
		PID:        current.PID,
		Original:   current,
		Target:     target,
		DeviceHint: deviceID,
	}
	if loadErr == nil && existing.IMEI == imei && existing.VID != "" {
		snap.Original = existing.Original
		snap.CapturedAt = existing.CapturedAt
		if snap.CapturedAt == "" {
			snap.CapturedAt = now
		}
		res.Original = snap.Original
	} else if loadErr != nil && !errors.Is(loadErr, ErrSnapshotNotFound) {
		return res, Snapshot{}, p.fail(res, StageSnapshot, "read existing snapshot", loadErr)
	}
	path, err = p.store.Save(snap)
	if err != nil {
		return res, Snapshot{}, p.fail(res, StageSnapshot, "write snapshot", err)
	}
	res.SnapshotPath = path
	if containsSecret(snap) {
		return res, Snapshot{}, p.fail(res, StageSnapshot, "snapshot contained forbidden fields", errors.New("refusing to persist ports or secrets"))
	}
	return res, snap, nil
}

func (p *Provisioner) apply(ctx context.Context, deviceID string, snap Snapshot, live VoiceSettings, haveLive, restoring bool) (Result, error) {
	res := Result{
		DeviceID:     deviceID,
		IMEITail:     IMEITail(snap.IMEI),
		SnapshotPath: "",
		Original:     snap.Original,
		Target:       snap.Target,
		Restored:     false,
	}
	if path, err := p.store.PathFor(snap.IMEI); err == nil {
		res.SnapshotPath = path
	}
	var (
		current VoiceSettings
		imei    string
		err     error
	)
	if haveLive {
		current = live
		imei = snap.IMEI
	} else {
		current, imei, _, err = p.readSettings(deviceID)
		if err != nil {
			stage := StageApplyIMS
			if restoring {
				stage = StageRestoreWrite
			}
			return res, p.fail(res, stage, "read before apply", err)
		}
	}
	if imei != snap.IMEI {
		stage := StageIdentity
		if restoring {
			stage = StageRestoreIdentity
		}
		res.Current = current
		return res, p.fail(res, stage, "live IMEI changed", ErrIMEIMismatch)
	}
	res.Current = current

	if err := p.applyIMS(deviceID, &res); err != nil {
		return res, err
	}
	if err := p.applyMBN(deviceID, &res); err != nil {
		return res, err
	}
	if err := p.applyUSBCFG(deviceID, &res); err != nil {
		return res, err
	}

	if res.RebootRequired && p.opts.Reboot {
		if _, err := p.exec(deviceID, "AT+CFUN=1,1"); err != nil {
			return res, p.fail(res, StageReenumerate, "reboot command", err)
		}
	}
	if res.RebootRequired && p.waiter != nil {
		wait := p.opts.WaitReenumerate
		if wait <= 0 {
			wait = defaultWaitReenum
		}
		newID, err := p.waiter.WaitByIMEI(ctx, snap.IMEI, wait)
		if err != nil {
			res.Stage = StageReenumerate
			res.OK = false
			res.Verified = false
			res.Restored = false
			return res, p.fail(res, StageReenumerate, "wait for IMEI after re-enumeration", err)
		}
		deviceID = newID
		res.DeviceID = newID
	}

	current, imei, _, err = p.readSettings(deviceID)
	if err != nil {
		return res, p.fail(res, StageVerify, "re-read after apply", err)
	}
	if imei != snap.IMEI {
		return res, p.fail(res, StageVerify, "IMEI mismatch after apply", ErrIMEIMismatch)
	}
	res.Current = current
	res.Drift = desiredDrift(current, snap.Target, res.RebootRequired && p.waiter == nil)
	res.Stage = StageVerify
	if len(res.Drift) > 0 {
		res.OK = false
		res.Verified = false
		res.Restored = false
		return res, p.fail(res, StageVerify, "approved fields drifted", ErrFieldDrift)
	}
	if res.RebootRequired && p.waiter == nil {
		res.OK = false
		res.Verified = false
		return res, p.fail(res, StageVerify, "module must re-enumerate before verify", ErrRebootRequired)
	}
	res.RebootRequired = current.UACEnabled != snap.Target.UACEnabled
	res.OK = true
	res.Verified = true
	return res, nil
}

func (p *Provisioner) applyIMS(deviceID string, res *Result) error {
	res.Stage = StageApplyIMS
	if res.Current.IMSEnabled == res.Target.IMSEnabled && res.Current.VoLTEEnabled == res.Target.VoLTEEnabled {
		return nil
	}
	var last error
	for _, cmd := range IMSSetCommands(res.Target.IMSEnabled, res.Target.VoLTEEnabled) {
		if _, err := p.exec(deviceID, cmd); err != nil {
			last = err
			continue
		}
		last = nil
		break
	}
	if last != nil {
		return p.fail(*res, StageApplyIMS, "write IMS/VoLTE", last)
	}
	res.Written = appendUnique(res.Written, "ims")
	current, _, _, err := p.readSettings(deviceID)
	if err != nil {
		return p.fail(*res, StageApplyIMS, "re-query IMS", err)
	}
	res.Current = current
	if current.IMSEnabled != res.Target.IMSEnabled {
		res.Drift = appendUnique(res.Drift, "ims")
		return p.fail(*res, StageApplyIMS, "IMS enable bit did not take", ErrFieldDrift)
	}
	if current.VoLTEEnabled != res.Target.VoLTEEnabled {
		res.Drift = appendUnique(res.Drift, "volte")
		return p.fail(*res, StageApplyIMS, "VoLTE enable bit did not take", ErrFieldDrift)
	}
	res.Written = appendUnique(res.Written, "volte")
	return nil
}

func (p *Provisioner) applyMBN(deviceID string, res *Result) error {
	res.Stage = StageApplyMBN
	if res.Current.MBNAutoSel == res.Target.MBNAutoSel && res.Current.MBNName == res.Target.MBNName {
		return nil
	}
	if res.Target.MBNName != res.Current.MBNName && strings.TrimSpace(res.Target.MBNName) == "" {
		return p.fail(*res, StageApplyMBN, "refusing empty MBN name", errors.New("mbn name required"))
	}
	if res.Current.MBNName != res.Target.MBNName {
		if _, err := p.exec(deviceID, MBNAutoSelSetCommand(false)); err != nil {
			return p.fail(*res, StageApplyMBN, "disable MBN autosel", err)
		}
		if _, err := p.exec(deviceID, MBNSelectCommand(res.Target.MBNName)); err != nil {
			return p.fail(*res, StageApplyMBN, "select MBN", err)
		}
		if _, err := p.exec(deviceID, MBNActivateCommand()); err != nil {
			return p.fail(*res, StageApplyMBN, "activate MBN", err)
		}
		res.Written = appendUnique(res.Written, "mbn")
		res.RebootRequired = true
	}
	if res.Current.MBNAutoSel != res.Target.MBNAutoSel && res.Target.MBNName == res.Current.MBNName {
		if _, err := p.exec(deviceID, MBNAutoSelSetCommand(res.Target.MBNAutoSel)); err != nil {
			return p.fail(*res, StageApplyMBN, "write MBN autosel", err)
		}
		res.Written = appendUnique(res.Written, "mbn_autosel")
	}
	current, _, _, err := p.readSettings(deviceID)
	if err != nil {
		return p.fail(*res, StageApplyMBN, "re-query MBN", err)
	}
	res.Current = current
	return nil
}

func (p *Provisioner) applyUSBCFG(deviceID string, res *Result) error {
	res.Stage = StageApplyUSBCFG
	if canonHexID(res.Current.VID) != canonHexID(res.Target.VID) || canonHexID(res.Current.PID) != canonHexID(res.Target.PID) {
		return p.fail(*res, StageApplyUSBCFG, "refusing VID/PID change", ErrVIDPIDMismatch)
	}
	if res.Current.UACEnabled == res.Target.UACEnabled && strings.Join(res.Current.USBFields, ",") == strings.Join(res.Target.USBFields, ",") {
		return nil
	}
	fields := withUACFields(res.Current.USBFields, res.Target.UACEnabled)
	if len(fields) < 3 {
		// Some QDC507 MBN images reject AT+QCFG="usbcfg"?. Skip UAC; IMS still proceeds.
		return nil
	}
	fields[0] = res.Current.USBFields[0]
	fields[1] = res.Current.USBFields[1]
	if _, err := p.exec(deviceID, USBConfigSetCommand(fields)); err != nil {
		return p.fail(*res, StageApplyUSBCFG, "write USBCFG", err)
	}
	res.Written = appendUnique(res.Written, "usbcfg")
	res.RebootRequired = true
	current, _, _, err := p.readSettings(deviceID)
	if err != nil {
		return p.fail(*res, StageApplyUSBCFG, "re-query USBCFG", err)
	}
	res.Current = current
	return nil
}

func (p *Provisioner) readSettings(deviceID string) (VoiceSettings, string, []MBNEntry, error) {
	imei, err := p.readIMEI(deviceID)
	if err != nil {
		return VoiceSettings{}, "", nil, err
	}
	imsResp, err := p.exec(deviceID, IMSQueryCommand())
	if err != nil {
		return VoiceSettings{}, imei, nil, fmt.Errorf("query IMS: %w", err)
	}
	ims, err := ParseIMSConfig(imsResp)
	if err != nil {
		return VoiceSettings{}, imei, nil, err
	}
	var usb USBConfig
	usbResp, usbErr := p.exec(deviceID, USBConfigQueryCommand())
	if usbErr == nil {
		parsed, parseErr := ParseUSBConfig(usbResp)
		if parseErr != nil {
			return VoiceSettings{}, imei, nil, parseErr
		}
		usb = parsed
	}
	autoResp, err := p.exec(deviceID, MBNAutoSelQueryCommand())
	if err != nil {
		return VoiceSettings{}, imei, nil, fmt.Errorf("query mbn autosel: %w", err)
	}
	autoSel, _, err := ParseMBNAutoSel(autoResp)
	if err != nil {
		return VoiceSettings{}, imei, nil, err
	}
	listResp, err := p.exec(deviceID, MBNListQueryCommand())
	if err != nil {
		return VoiceSettings{}, imei, nil, fmt.Errorf("query mbn list: %w", err)
	}
	entries, err := ParseMBNList(listResp)
	if err != nil {
		return VoiceSettings{}, imei, nil, err
	}
	selected, _ := SelectedMBN(entries)
	return VoiceSettings{
		IMSEnabled:   ims.IMSEnabled,
		VoLTEEnabled: ims.VoLTEEnabled,
		USBFields:    append([]string(nil), usb.Fields...),
		UACEnabled:   usb.UACEnabled,
		VID:          usb.VID,
		PID:          usb.PID,
		MBNAutoSel:   autoSel,
		MBNName:      selected.Name,
		MBNIndex:     selected.Index,
	}, imei, entries, nil
}

func (p *Provisioner) readIMEI(deviceID string) (string, error) {
	var last error
	for _, cmd := range IMEIQueryCommands() {
		resp, err := p.exec(deviceID, cmd)
		if err != nil {
			last = err
			continue
		}
		imei, err := ParseIMEI(resp)
		if err != nil {
			last = err
			continue
		}
		return imei, nil
	}
	if last == nil {
		last = errors.New("empty IMEI")
	}
	return "", last
}

func (p *Provisioner) exec(deviceID, cmd string) (string, error) {
	if p == nil {
		return "", errors.New("volte provision: AT transport is not configured")
	}
	return execAT(p.at, deviceID, cmd)
}

func (p *Provisioner) fail(res Result, stage, message string, err error) error {
	res.Stage = stage
	res.OK = false
	res.Verified = false
	if stage != StageRestoreVerify {
		res.Restored = false
	}
	return &StageError{Stage: stage, DeviceID: res.DeviceID, Message: message, Result: res, Err: err}
}

func overlayDesired(current VoiceSettings, d Desired) VoiceSettings {
	t := current
	if d.IMSEnabled != nil {
		t.IMSEnabled = *d.IMSEnabled
	}
	if d.VoLTEEnabled != nil {
		t.VoLTEEnabled = *d.VoLTEEnabled
	}
	if d.UACEnabled != nil && len(current.USBFields) >= 3 {
		t.UACEnabled = *d.UACEnabled
		t.USBFields = withUACFields(current.USBFields, *d.UACEnabled)
	}
	if d.MBNAutoSel != nil {
		t.MBNAutoSel = *d.MBNAutoSel
	}
	if d.MBNSelect != nil {
		t.MBNName = strings.TrimSpace(*d.MBNSelect)
	}
	return t
}

func desiredDrift(current, target VoiceSettings, pendingReboot bool) []string {
	var drift []string
	if current.IMSEnabled != target.IMSEnabled {
		drift = append(drift, "ims")
	}
	if current.VoLTEEnabled != target.VoLTEEnabled {
		drift = append(drift, "volte")
	}
	if !pendingReboot && current.UACEnabled != target.UACEnabled {
		drift = append(drift, "usbcfg")
	}
	if current.MBNName != target.MBNName {
		drift = append(drift, "mbn")
	}
	if !pendingReboot && current.MBNAutoSel != target.MBNAutoSel && current.MBNName == target.MBNName {
		drift = append(drift, "mbn_autosel")
	}
	return drift
}

func settingsEqual(a, b VoiceSettings) bool {
	return a.IMSEnabled == b.IMSEnabled &&
		a.VoLTEEnabled == b.VoLTEEnabled &&
		a.UACEnabled == b.UACEnabled &&
		a.MBNAutoSel == b.MBNAutoSel &&
		a.MBNName == b.MBNName &&
		canonHexID(a.VID) == canonHexID(b.VID) &&
		canonHexID(a.PID) == canonHexID(b.PID)
}

func diffSettings(a, b VoiceSettings) []string {
	return desiredDrift(a, b, false)
}

func appendUnique(list []string, v string) []string {
	for _, item := range list {
		if item == v {
			return list
		}
	}
	return append(list, v)
}

func containsSecret(snap Snapshot) bool {
	blob := strings.ToLower(fmt.Sprintf("%#v", snap))
	if strings.Contains(blob, "/dev/") || strings.Contains(blob, "ttyusb") || strings.Contains(blob, "cdc-wdm") {
		return true
	}
	if strings.Contains(blob, "adb") && strings.Contains(blob, "password") {
		return true
	}
	return false
}

func boolPtr(v bool) *bool { return &v }
