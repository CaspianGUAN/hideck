package mbn

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) Policies(ctx context.Context) ([]Policy, error) { return s.store.Policies(ctx) }

func (s *Service) SavePolicy(ctx context.Context, p Policy) error {
	if !strings.HasPrefix(p.Key, "sim:") && !strings.HasPrefix(p.Key, "device:") {
		return errors.New("MBN 策略必须绑定 SIM 或物理模组")
	}
	if len(strings.TrimSpace(strings.SplitN(p.Key, ":", 2)[1])) == 0 {
		return errors.New("策略标识为空")
	}
	switch p.Mode {
	case "inherit", "autosel":
		p.Profile = ""
		p.Version = ""
		p.AssetID = ""
		p.AllowImport = false
	case "fixed":
		if err := ValidateName(p.Profile); err != nil {
			return err
		}
		if p.Model == "" || p.Firmware == "" {
			return errors.New("固定策略必须包含模组型号和固件")
		}
		if p.AllowImport && p.AssetID == "" {
			return errors.New("允许自动导入时必须指定文件")
		}
	default:
		return errors.New("未知 MBN 策略")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.AssetID != "" {
		if _, _, err := s.Asset(ctx, p.AssetID); err != nil {
			return err
		}
	}
	if err := s.store.SavePolicy(ctx, p); err != nil {
		return err
	}
	for key, override := range s.overrides {
		if override.Key == p.Key {
			delete(s.overrides, key)
		}
	}
	return nil
}

func (s *Service) EffectivePolicy(ctx context.Context, owner Owner) (Policy, error) {
	s.mu.Lock()
	override, exists := s.overrides[owner.IMEI+":"+owner.ICCID]
	if exists && (override.SessionGeneration != owner.Generation || override.SessionIdentityGeneration != owner.IdentityGeneration) {
		delete(s.overrides, owner.IMEI+":"+owner.ICCID)
		exists = false
	}
	s.mu.Unlock()
	if exists {
		return override, nil
	}
	policies, err := s.store.Policies(ctx)
	if err != nil {
		return Policy{}, err
	}
	devicePolicy := Policy{Mode: "inherit"}
	for _, p := range policies {
		if p.Key == "sim:"+owner.ICCID && p.Mode != "inherit" {
			return p, nil
		}
		if p.Key == "device:"+owner.IMEI {
			devicePolicy = p
		}
	}
	return devicePolicy, nil
}

func (s *Service) keepSessionChoice(snapshot Snapshot) {
	policy := Policy{Key: "sim:" + snapshot.Owner.ICCID, Mode: "fixed", Model: snapshot.Owner.Model, Firmware: snapshot.Owner.Firmware, Profile: snapshot.Active(), SessionGeneration: snapshot.Owner.Generation, SessionIdentityGeneration: snapshot.Owner.IdentityGeneration}
	if snapshot.AutoSel {
		policy.Mode = "autosel"
	}
	s.mu.Lock()
	s.overrides[snapshot.Owner.IMEI+":"+snapshot.Owner.ICCID] = policy
	s.mu.Unlock()
}

// Reconcile returns pending until a separately persisted operation has verified
// the profile. A failed attempt is exposed and not retried on every scan.
func (s *Service) Reconcile(ctx context.Context, owner Owner) (bool, error) {
	policy, err := s.EffectivePolicy(ctx, owner)
	if err != nil {
		return false, err
	}
	if policy.Mode == "inherit" {
		return false, nil
	}
	if s.Busy(owner.DeviceID) {
		return true, nil
	}
	current, err := s.Inspect(ctx, owner.DeviceID)
	if err != nil {
		return false, err
	}
	if !sameGeneration(owner, current.Owner) {
		return false, ErrOwnerChanged
	}
	if policy.Mode == "fixed" {
		if policy.Model != current.Owner.Model || policy.Firmware != current.Owner.Firmware {
			return false, errors.New("固定 MBN 策略的型号或固件不匹配")
		}
		versionMatches := policy.Version == ""
		for _, p := range current.Profiles {
			if p.Name == policy.Profile && p.Version == policy.Version {
				versionMatches = true
			}
		}
		if hasProfile(current, policy.Profile) && !versionMatches {
			return false, errors.New("固定 MBN 版本不匹配，未替换现有配置")
		}
		if activeProfile(current, policy.Profile) && !current.AutoSel {
			return false, nil
		}
	} else if current.AutoSel {
		return false, nil
	}
	request := Request{
		ExpectedOwner: &current.Owner,
		RequestID: fmt.Sprintf("policy-%s-%s-%s-%d-%d-%d", s.policySessionID, owner.IMEI, owner.ICCID,
			owner.Generation, owner.IdentityGeneration, policy.UpdatedAt.UnixNano()),
		Kind: "apply", Profile: policy.Profile, Version: policy.Version, AutoSel: policy.Mode == "autosel",
	}
	if policy.Mode == "fixed" && !hasProfile(current, policy.Profile) {
		if !policy.AllowImport {
			return false, errors.New("策略指定的 MBN 不在模组中，且未授权自动导入")
		}
		request.AssetID = policy.AssetID
		request.AcceptUnknownCompatibility = true
	}
	op, err := s.start(ctx, operationStart{deviceID: owner.DeviceID, request: request, policy: true})
	if err != nil {
		return false, err
	}
	if !op.Running() {
		return false, fmt.Errorf("MBN 策略未生效，前次操作 %s: %s", op.ID, op.Error)
	}
	return true, nil
}
