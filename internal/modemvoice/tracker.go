package modemvoice

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/uuid"
)

type TrackedCall struct {
	ID   string
	Call Call
}

type Change struct {
	Call  TrackedCall
	Ended bool
}

// Tracker belongs to one SIM/worker generation. Discard it on switch/restart.
// It accepts only validated successful snapshots; errors never end calls.
type Tracker struct {
	mu     sync.Mutex
	prefix string
	next   uint64
	active map[int]TrackedCall
}

func NewTracker() *Tracker {
	return &Tracker{prefix: uuid.NewString(), active: make(map[int]TrackedCall)}
}

func (t *Tracker) Apply(response string) ([]Change, error) {
	calls, err := ParseCLCC(response)
	if err != nil {
		return nil, err
	}
	return t.applyCalls(calls), nil
}

func (t *Tracker) applyCalls(calls []Call) []Change {
	t.mu.Lock()
	defer t.mu.Unlock()
	next := make(map[int]TrackedCall, len(calls))
	changes := make([]Change, 0)
	for _, call := range calls {
		previous, exists := t.active[call.Index]
		if exists && !sameCall(previous.Call, call) {
			changes = append(changes, Change{Call: previous, Ended: true})
			exists = false
		}
		id := previous.ID
		if !exists {
			t.next++
			id = fmt.Sprintf("modem-%s-%d", t.prefix, t.next)
		} else if call.Number == "" {
			call.Number = previous.Call.Number
			call.NumberType = previous.Call.NumberType
		}
		tracked := TrackedCall{ID: id, Call: call}
		next[call.Index] = tracked
		if !exists || previous.Call != call {
			changes = append(changes, Change{Call: tracked})
		}
	}
	for index, previous := range t.active {
		if _, exists := next[index]; !exists {
			changes = append(changes, Change{Call: previous, Ended: true})
		}
	}
	t.active = next
	return changes
}

func (t *Tracker) Calls() []TrackedCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]TrackedCall, 0, len(t.active))
	for _, call := range t.active {
		calls = append(calls, call)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Call.Index < calls[j].Call.Index })
	return calls
}
