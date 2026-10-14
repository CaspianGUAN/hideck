package volte

import (
	"context"
	"testing"
)

type managedMBNModem struct{ *FakeModem }

func (*managedMBNModem) MBNManaged(string) (bool, error) { return true, nil }

func TestManagedMBNDoesNotChangeDuringVoLTEProvisionOrRestore(t *testing.T) {
	modem := &managedMBNModem{newFakeModem()}
	p := NewProvisioner(modem, &MemoryStore{})
	profile := "nonexistent-operator-profile"
	res, err := p.Ensure(context.Background(), "wwan1", Desired{MCC: "234", MNC: "15", MBNSelect: &profile, MBNAutoSel: boolPtr(false), IMSEnabled: boolPtr(true), VoLTEEnabled: boolPtr(true)})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	// A subsequently applied SIM policy must not be undone by the old backup.
	modem.AutoSel = 0
	for i := range modem.MBN {
		modem.MBN[i].Selected = i == 1
		modem.MBN[i].Activated = i == 1
	}
	res, err = p.Restore(context.Background(), "wwan1")
	if err != nil || !res.Restored || modem.AutoSel != 0 || !modem.MBN[1].Selected {
		t.Fatalf("%+v %v", res, err)
	}
	for _, field := range res.Written {
		if field == "mbn" || field == "mbn_autosel" {
			t.Fatalf("managed MBN overwritten: %+v", res)
		}
	}
}
