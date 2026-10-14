package mbn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type operationStart struct {
	deviceID string
	request  Request
	policy   bool
}

func (s *Service) Start(ctx context.Context, deviceID string, request Request) (Operation, error) {
	return s.start(ctx, operationStart{deviceID: deviceID, request: request})
}

func (s *Service) start(ctx context.Context, input operationStart) (Operation, error) {
	deviceID, request := input.deviceID, input.request
	if err := validateRequest(request); err != nil {
		return Operation{}, err
	}
	request = cloneOperationRequest(request)
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, replayed, err := s.replayOperation(ctx, deviceID, request); replayed || err != nil {
		return previous, err
	}
	if s.active[deviceID] != "" {
		return Operation{}, ErrConflict
	}
	owner, err := s.gateway.Identity(ctx, deviceID)
	if err != nil {
		return Operation{}, err
	}
	if request.ExpectedOwner != nil && !sameGeneration(*request.ExpectedOwner, owner) {
		return Operation{}, ErrOwnerChanged
	}
	// Check under the same lock as enqueueing: a concurrent manual operation
	// must not leave a new uncertain result between this check and Start.
	op := Operation{ID: newID(), DeviceID: deviceID, RequestID: request.RequestID, Request: request, Status: "queued", Stage: "queued", Results: []TrialResult{}}
	op.ExpectedOwner = owner
	op.RecoveryOf, err = s.recoveryForStart(ctx, owner, input)
	if err != nil {
		return Operation{}, err
	}
	if err := s.store.SaveOperation(ctx, &op); err != nil {
		return op, err
	}
	s.active[deviceID] = op.ID
	s.wg.Add(1)
	go s.run(op)
	return op, nil
}

func cloneOperationRequest(request Request) Request {
	cloned := request
	cloned.Candidates = append([]string(nil), request.Candidates...)
	if request.ExpectedOwner != nil {
		owner := *request.ExpectedOwner
		cloned.ExpectedOwner = &owner
	}
	return cloned
}

func (s *Service) replayOperation(ctx context.Context, deviceID string, request Request) (Operation, bool, error) {
	previous, err := s.store.FindRequest(ctx, deviceID, request.RequestID)
	if errors.Is(err, ErrNotFound) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	a, _ := json.Marshal(previous.Request)
	b, _ := json.Marshal(request)
	if string(a) != string(b) {
		return Operation{}, false, fmt.Errorf("%w: request_id 已用于不同请求", ErrConflict)
	}
	return previous, true, nil
}
