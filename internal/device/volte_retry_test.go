package device

import (
	"errors"
	"testing"
	"time"

	"github.com/yibaiba/hideck/internal/volte"
)

func TestIsTransientVoLTEStartError(t *testing.T) {
	if isTransientVoLTEStartError(nil) {
		t.Fatal("nil")
	}
	if isTransientVoLTEStartError(volte.ErrNoUniqueProfile) || isTransientVoLTEStartError(ErrLebaraUKRFLocked) {
		t.Fatal("permanent errors must not retry")
	}
	if !isTransientVoLTEStartError(errors.New("volte provision apply_ims: read before apply: query mbn list: timeout")) {
		t.Fatal("AT timeout must retry")
	}
	if !isTransientVoLTEStartError(errors.New("allocate IMS: allocate client ID request failed after retries: write failed: write unix @->@qmi-proxy: write: broken pipe")) {
		t.Fatal("qmi-proxy broken pipe must retry after the control plane returns")
	}
	if !isTransientVoLTEStartError(errors.New("QMI 服务未就绪: IMSA")) {
		t.Fatal("temporarily unavailable IMSA service must retry")
	}
	if nativeVoLTERetryDelay(1) != 3*time.Second || nativeVoLTERetryDelay(2) != 6*time.Second {
		t.Fatalf("retry delay %s %s", nativeVoLTERetryDelay(1), nativeVoLTERetryDelay(2))
	}
}

func TestNativeVoLTEScheduleCoalescesPerDevice(t *testing.T) {
	pool := NewPool(nil)
	defer pool.cancel()
	first := pool.beginNativeVoLTESchedule("wwan0")
	if first == nil {
		t.Fatal("first VoLTE schedule was rejected")
	}
	if pool.beginNativeVoLTESchedule("wwan0") != nil {
		t.Fatal("duplicate VoLTE schedule was accepted")
	}
	if pool.beginNativeVoLTESchedule("wwan1") == nil {
		t.Fatal("another device was blocked by wwan0")
	}
	pool.endNativeVoLTESchedule("wwan0", first)
	if pool.beginNativeVoLTESchedule("wwan0") == nil {
		t.Fatal("completed VoLTE schedule could not be started again")
	}
}
