package runtimecore

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/engine/swu"
)

func TestEAPFailureRetriesBackOff(t *testing.T) {
	fixed := func(int) int64 { return int64(30 * time.Second) }
	err := fmt.Errorf("ePDG 会话失败: %w", fmt.Errorf("%w after aka_challenge_answered", swu.ErrEAPAuthenticationFailed))
	want := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
		10 * time.Minute, 30 * time.Minute, 30 * time.Minute,
	}
	attempt := 0
	for index, expected := range want {
		var delay int64
		delay, attempt = retryDecision(err, attempt, fixed)
		if time.Duration(delay) != expected {
			t.Fatalf("retry %d delay = %v, want %v", index, time.Duration(delay), expected)
		}
	}
}

func TestOtherFailuresKeepTheirRetryDelay(t *testing.T) {
	fixed := func(int) int64 { return int64(30 * time.Second) }
	for attempt := 0; attempt < 8; attempt++ {
		if delay, _ := retryDecision(errors.New("swu: IKE_SA_INIT timeout"), attempt, fixed); time.Duration(delay) != 30*time.Second {
			t.Fatalf("attempt %d delay = %v, want unchanged 30s", attempt, time.Duration(delay))
		}
	}
}
