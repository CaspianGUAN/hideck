package device

import (
	"testing"

	qmicore "github.com/yibaiba/hideck/internal/qmi"
)

func TestBindVoiceDTMFRetainsQMIInstance(t *testing.T) {
	oldCore, newCore := &qmicore.Manager{}, &qmicore.Manager{}
	p := &Pool{workers: map[string]*Worker{"wwan1": {ID: "wwan1", QMICore: oldCore}}}
	bound, err := p.BindVoiceDTMF("wwan1")
	if err != nil {
		t.Fatal(err)
	}
	p.workers["wwan1"] = &Worker{ID: "wwan1", QMICore: newCore}
	if bound != oldCore {
		t.Fatal("bound DTMF lost original QMI instance")
	}
	current, err := p.BindVoiceDTMF("wwan1")
	if err != nil || current != newCore {
		t.Fatalf("replacement binding = %v, err=%v", current, err)
	}
	delete(p.workers, "wwan1")
	if _, err := p.BindVoiceDTMF("wwan1"); err == nil {
		t.Fatal("missing Worker accepted")
	}
}
