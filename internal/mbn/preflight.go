package mbn

import (
	"errors"
	"fmt"
)

// Validate against the saved live snapshot before stopping an existing service.
// Mutating paths still re-read their state immediately before writing.
func (s *Service) preflight(session Session, op *Operation) error {
	r, current := op.Request, op.Original
	switch r.Kind {
	case "apply":
		if !r.AutoSel && !hasProfile(current, r.Profile) && r.AssetID != "" {
			return s.validateImport(session.Owner(), r, current)
		}
		return validateApply(current, r.Profile, r.AutoSel, r.Version)
	case "import":
		return s.validateImport(session.Owner(), r, current)
	case "trial":
		if !current.AutoSel && current.Active() == "" {
			return errors.New("无法确认原配置，不能开始需要恢复能力的试配")
		}
		for _, name := range r.Candidates {
			if err := validateApply(current, name, false, ""); err != nil {
				return fmt.Errorf("候选 %q: %w", name, err)
			}
		}
	case "delete":
		return s.validateDelete(op)
	case "restore":
		previous, err := s.store.Operation(s.ctx, r.RestoreOperation)
		if err != nil {
			return err
		}
		if !sameOwner(previous.Original.Owner, session.Owner()) {
			return ErrOwnerChanged
		}
		return validateApply(current, previous.Original.Active(), previous.Original.AutoSel, "")
	}
	return nil
}

func validateApply(current Snapshot, name string, auto bool, version string) error {
	if !current.Capabilities.AutoSel || (!auto && !current.Capabilities.Select) {
		return errors.New("固件未声明所需 MBN 写入能力")
	}
	if auto {
		return nil
	}
	for _, p := range current.Profiles {
		if p.Name != name {
			continue
		}
		if version != "" && p.Version != version {
			return errors.New("目标 MBN 的版本与固定策略不匹配")
		}
		return nil
	}
	return errors.New("目标配置不在模组当前列表中")
}

func (s *Service) validateImport(owner Owner, r Request, current Snapshot) error {
	if !current.Capabilities.Add {
		return errors.New("固件未声明 Add 能力")
	}
	a, _, err := s.Asset(s.ctx, r.AssetID)
	if err != nil {
		return err
	}
	if (a.Model != "" && a.Model != owner.Model) || (a.Firmware != "" && a.Firmware != owner.Firmware) {
		return errors.New("文件声明的型号/固件与当前模组不匹配")
	}
	if !r.AcceptUnknownCompatibility {
		return errors.New("必须确认外部 MBN 未经本系统验证，可能不兼容当前固件")
	}
	return nil
}

func (s *Service) validateDelete(op *Operation) error {
	if !op.Original.Capabilities.Delete || !hasProfile(op.Original, op.Request.Profile) {
		return errors.New("固件不支持删除或待删除配置不存在")
	}
	policies, err := s.store.Policies(s.ctx)
	if err != nil {
		return err
	}
	for _, p := range policies {
		if p.Mode == "fixed" && p.Profile == op.Request.Profile && p.Model == op.Original.Owner.Model && p.Firmware == op.Original.Owner.Firmware {
			return errors.New("配置仍被 MBN 策略引用，请先明确修改策略")
		}
	}
	for _, p := range op.Original.Profiles {
		if p.Name != op.Request.Profile || (!p.Selected && !p.Activated) {
			continue
		}
		if op.Request.Replacement == p.Name {
			return errors.New("使用中的 MBN 必须指定不同的替代配置")
		}
		return validateApply(op.Original, op.Request.Replacement, false, "")
	}
	return nil
}
