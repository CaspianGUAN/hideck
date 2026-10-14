package config

import (
	"github.com/spf13/viper"
	"testing"
)

func TestModemVoiceAutoADBConfiguration(t *testing.T) {
	for _, item := range []struct {
		name, yaml string
		allowed    bool
	}{
		{"default", "devices: []", true},
		{"enabled", "modem_voice:\n  auto_enable_adb: true", true},
		{"disabled", "modem_voice:\n  auto_enable_adb: false", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			config, err := Load(writeTempConfig(t, item.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if config.ModemVoice.ADBEnableAllowed() != item.allowed {
				t.Fatal("incorrect ADB configuration")
			}
		})
	}
}
