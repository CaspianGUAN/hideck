package mbn

import "testing"

func TestListPreservesRawMetadataAndWhitespaceVariants(t *testing.T) {
	for _, prefix := range []string{`+QMBNCFG:"List",`, `+QMBNCFG: "List",`} {
		profiles, err := ParseList(prefix + `0,1,1,"OpenMkt-Commercial-CU",0x05011501,000000000` + "\r\nOK\r\n")
		if err != nil {
			t.Fatal(err)
		}
		p := profiles[0]
		if p.Name != "OpenMkt-Commercial-CU" || p.Version != "0x05011501" || p.Date != "000000000" || !p.Activated || !p.Selected || p.Origin != "unknown" {
			t.Fatalf("%+v", p)
		}
	}
}
func TestRejectMalformedOrRejectedResponses(t *testing.T) {
	for _, response := range []string{"ERROR", "+CME ERROR: 3", "OK", `+QMBNCFG:"List",0,2,1,"A"`, `+QMBNCFG:"List",x,0,1,"A"`} {
		if _, err := ParseList(response); err == nil {
			t.Fatalf("accepted %q", response)
		}
	}
}
func TestATInjectionIsRejected(t *testing.T) {
	for _, name := range []string{"", `A",0`, "A\rAT+CFUN=1,1", "A\x00B", "A\\B"} {
		if _, err := NamedCommand("Select", name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
func TestCapabilitiesDoNotAssumeExportOrActivate(t *testing.T) {
	c, err := ParseCapabilities(`+QMBNCFG: ("List","Select","AutoSel","Delete","Add","List_all")`)
	if err != nil || !c.List || !c.Select || !c.Add || !c.Delete || !c.AutoSel || c.Export {
		t.Fatalf("%+v %v", c, err)
	}
	a, err := ParseAutoSel(`+QMBNCFG:"AutoSel",1`)
	if err != nil || !a {
		t.Fatalf("%v %v", a, err)
	}
}
