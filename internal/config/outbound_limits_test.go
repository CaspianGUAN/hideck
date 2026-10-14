package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestOutboundLimitsDecodePerCardAndExplicitDisable(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	path := writeTempConfig(t, `outbound_limits:
  calls:
    per_hour: 40
  sms:
    disabled: true
  cards:
    "8986001234567890123":
      sms:
        disabled: false
        per_day: 200
`)
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	limits := config.OutboundLimits
	if limits.Calls.PerHour != 40 || limits.SMS.Disabled == nil || !*limits.SMS.Disabled {
		t.Fatalf("limits=%+v", limits)
	}
	card := limits.Cards["8986001234567890123"]
	if card.SMS.PerDay != 200 || card.SMS.Disabled == nil || *card.SMS.Disabled {
		t.Fatalf("card=%+v", card)
	}
}
