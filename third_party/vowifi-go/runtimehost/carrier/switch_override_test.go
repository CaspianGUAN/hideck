package carrier

import "testing"

func TestCarrierSwitchOverridesThroughFiles(t *testing.T) {
	ClearCarrierOverrides()
	t.Cleanup(ClearCarrierOverrides)
	for _, test := range []struct {
		name, body string
		json       bool
	}{
		{"flags.yaml", "carrier_overrides:\n  53005:\n    withhold_device_identity: false\n    keep_child_sa_on_rekey_decline: false\n", false},
		{"flags.json", `[{"MCC":"530","MNC":"05","WithholdDeviceIdentity":false,"KeepChildSAOnRekeyDecline":false}]`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ClearCarrierOverrides()
			path := writeOverrideFile(t, test.name, test.body)
			if test.json {
				if _, err := LoadCarrierOverridesJSON(path); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, _, _, err := LoadCarrierOverrides(path); err != nil {
					t.Fatal(err)
				}
			}
			got := ResolveEffectiveCarrierConfig("530", "05")
			if got.WithholdDeviceIdentity || got.KeepChildSAOnRekeyDecline {
				t.Fatal("explicit false did not override Spark preset")
			}
		})
	}
}
