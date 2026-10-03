package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsesOneSIPLinePerCard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `
sip_clients:
  - enabled: true
    mode: trunk
    server: "127.0.0.1:50600"
    transport: udp
    local_port: 5070
    device_id: wwp0s5u3i4
    inbound_target: "2135192402"
  - enabled: true
    mode: trunk
    server: "127.0.0.1:50600"
    transport: udp
    local_port: 5071
    device_id: wwp0s5u2i4
    inbound_target: "3105550100"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SIP.Enabled {
		t.Fatal("legacy sip_client enabled without being configured")
	}
	if len(cfg.SIPClients) != 2 {
		t.Fatalf("sip_clients = %+v", cfg.SIPClients)
	}
	second := cfg.SIPClients[1]
	if !second.Enabled || second.LocalPort != 5071 || second.DeviceID != "wwp0s5u2i4" ||
		second.InboundTo != "3105550100" || second.Mode != "trunk" {
		t.Fatalf("second line = %+v", second)
	}
}
