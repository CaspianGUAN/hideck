package api

import (
	"encoding/json"
	"testing"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/yibaiba/hideck/internal/modem"
)

func TestRuntimeDTOPreservesSMSRXPending(t *testing.T) {
	dto := runtimeStateToDTO(runtimehost.State{IMSReady: true, SMSMOReady: true, SMSRXPending: true}, modem.DeviceStatus{})
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["sms_rx_pending"] != true || decoded["sms_ready"] != false || decoded["sms_mo_ready"] != true {
		t.Fatalf("SMS readiness DTO = %s", body)
	}
}
