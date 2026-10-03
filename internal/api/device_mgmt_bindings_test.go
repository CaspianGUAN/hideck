package api

import (
	"testing"

	"github.com/yibaiba/hideck/internal/config"
)

func TestKeepFileDeviceBindingsUndoesRuntimeProjection(t *testing.T) {
	file := config.DeviceConfig{ID: "wwp0s5u3i4", ModemIMEI: "353837418209860"}
	runtime := config.DeviceConfig{
		Interface: "wwp0s5u3i4", ControlDevice: "/dev/cdc-wdm0",
		ATPort: "/dev/ttyUSB2", USBPath: "/sys/bus/usb/devices/3-3",
	}
	// 配置页只改了名称，绑定字段是 GET 投影出来的运行时值。
	submitted := runtime
	submitted.ID, submitted.Name, submitted.ModemIMEI = file.ID, "renamed", file.ModemIMEI

	got := keepFileDeviceBindings(submitted, file, runtime)
	if got.Interface != "" || got.ControlDevice != "" || got.ATPort != "" || got.USBPath != "" {
		t.Fatalf("runtime bindings leaked into the file: %+v", got)
	}
	if got.Name != "renamed" {
		t.Fatalf("name = %q", got.Name)
	}
	if managedNetworkConfigChanged(file, got) || deviceConfigRequiresRestart(file, got) {
		t.Fatal("renaming a device must not rebuild or restart it")
	}
}

func TestKeepFileDeviceBindingsAcceptsExplicitChange(t *testing.T) {
	file := config.DeviceConfig{ATPort: "/dev/ttyUSB2"}
	runtime := config.DeviceConfig{ATPort: "/dev/ttyUSB2"}
	got := keepFileDeviceBindings(config.DeviceConfig{ATPort: "/dev/ttyUSB3"}, file, runtime)
	if got.ATPort != "/dev/ttyUSB3" {
		t.Fatalf("ATPort = %q, want the submitted change", got.ATPort)
	}
}
