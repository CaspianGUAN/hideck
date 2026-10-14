package policy

import "testing"

func TestCarrierSwitchOverrides(t *testing.T) {
	for _, test := range []struct {
		name, mcc, mnc string
		value          *bool
		want           bool
	}{
		{"spark-default", "530", "05", nil, true},
		{"spark-disabled", "530", "05", boolValue(false), false},
		{"spark-enabled", "530", "05", boolValue(true), true},
		{"vodafone-default", "234", "15", nil, false},
		{"2degrees-default", "530", "24", nil, false},
		{"explicit-enable", "234", "15", boolValue(true), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveEffectiveCarrierConfigWithOverride(test.mcc, test.mnc, CarrierOverride{
				WithholdDeviceIdentity: test.value, KeepChildSAOnRekeyDecline: test.value,
			})
			if got.WithholdDeviceIdentity != test.want || got.KeepChildSAOnRekeyDecline != test.want {
				t.Fatalf("withhold=%t keep=%t want=%t", got.WithholdDeviceIdentity, got.KeepChildSAOnRekeyDecline, test.want)
			}
		})
	}
}

func TestCarrierSwitchOverridePointersAreDetached(t *testing.T) {
	ClearCarrierOverrides()
	t.Cleanup(ClearCarrierOverrides)
	value := false
	if err := SetCarrierOverrides(map[string]CarrierOverride{"53005": {WithholdDeviceIdentity: &value, KeepChildSAOnRekeyDecline: &value}}); err != nil {
		t.Fatal(err)
	}
	value = true
	got := ResolveEffectiveCarrierConfig("530", "05")
	if got.WithholdDeviceIdentity || got.KeepChildSAOnRekeyDecline {
		t.Fatal("caller mutation changed stored flags")
	}
	copy, _ := carrierOverrideByKey(plmnKey("530", "05"))
	*copy.WithholdDeviceIdentity, *copy.KeepChildSAOnRekeyDecline = true, true
	got = ResolveEffectiveCarrierConfig("530", "05")
	if got.WithholdDeviceIdentity || got.KeepChildSAOnRekeyDecline {
		t.Fatal("returned pointers alias store")
	}
}

func boolValue(value bool) *bool { return &value }
