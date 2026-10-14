package config

import "testing"

func TestNotificationDeviceFiltersRoundTrip(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  enabled: false
notification_device_filters:
  bark: [old]
`)
	filters := map[string][]string{"telegram": {"wws27u1i4"}, "bark": {"wws27u2i4", "wws27u3i4"}, "email": nil}
	if err := UpdateNotificationInFile(path, NotificationConfigs{DeviceFilters: filters}); err != nil {
		t.Fatalf("UpdateNotificationInFile() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := cfg.NotificationDeviceFilters
	if len(got) != 2 || len(got["telegram"]) != 1 || got["telegram"][0] != "wws27u1i4" ||
		len(got["bark"]) != 2 || got["bark"][1] != "wws27u3i4" {
		t.Fatalf("filters = %#v", got)
	}

	if err := UpdateNotificationInFile(path, NotificationConfigs{}); err != nil {
		t.Fatalf("clear filters: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.NotificationDeviceFilters) != 0 {
		t.Fatalf("cleared filters = %#v", cfg.NotificationDeviceFilters)
	}
}
