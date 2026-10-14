package mbn

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Service) trial(session Session, op *Operation) error {
	for _, name := range op.Request.Candidates {
		if !hasProfile(op.Original, name) {
			return fmt.Errorf("候选 %q 不在当前列表", name)
		}
	}
	var failure error
	for _, name := range op.Request.Candidates {
		if failure = s.cancelled(op); failure != nil {
			break
		}
		result := TrialResult{Profile: name, StartedAt: time.Now()}
		failure = s.apply(session, op, name, false)
		if failure == nil {
			failure = s.observe(session, op, &result)
		}
		result.EndedAt = time.Now()
		result.Passed = failure == nil
		if failure != nil {
			result.Error = failure.Error()
		}
		op.Results = append(op.Results, result)
		if err := s.checkpoint(op, "candidate_complete"); err != nil {
			return err
		}
		if failure == nil {
			s.keepSessionChoice(op.Current)
			op.Warning = "已达到所选目标；通话和短信未测试。尚未绑定 SIM 策略。"
			return nil
		}
		if !errors.Is(failure, errRegistrationGoal) {
			break
		}
	}
	if failure == nil {
		failure = errors.New("没有候选达到验证目标")
	}
	return errors.Join(failure, s.restore(session, op, op.Original))
}

var errRegistrationGoal = errors.New("未达到稳定驻网目标")

func (s *Service) observe(session Session, op *Operation, result *TrialResult) error {
	if err := s.checkpoint(op, "observing_registration"); err != nil {
		return err
	}
	wait := duration(op.Request.RegistrationSeconds, DefaultRegistration)
	stable := duration(op.Request.StabilitySeconds, DefaultStability)
	ctx, cancel := context.WithTimeout(s.ctx, wait+stable)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var since time.Time
	for {
		if err := s.cancelled(op); err != nil {
			return err
		}
		registration, err := session.Registration(ctx)
		if err != nil {
			// Only this candidate's observation deadline permits trying another
			// profile. Cancellation, lost ownership and transport errors stay fatal.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) && s.ctx.Err() == nil {
				return errors.Join(errRegistrationGoal, err)
			}
			return err
		}
		match := registration.Registered && (op.Request.TargetPLMN == "" || registration.PLMN == op.Request.TargetPLMN)
		if op.Request.Goal == "ims" {
			match = match && registration.IMS
		}
		if op.Request.Goal == "data" {
			match = match && registration.Attached
		}
		if !match {
			since = time.Time{}
		} else if since.IsZero() {
			since = time.Now()
		}
		if !since.IsZero() && time.Since(since) >= stable {
			result.PLMN = registration.PLMN
			if op.Request.Goal == "data" {
				if err := session.Probe(ctx, op.Request.ProbeURL); err != nil {
					return errors.Join(errRegistrationGoal, err)
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(errRegistrationGoal, ctx.Err())
		case <-ticker.C:
		}
	}
}
