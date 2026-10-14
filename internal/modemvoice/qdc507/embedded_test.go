package qdc507

import "testing"

func TestEmbeddedBundleMatchesPinnedArtifacts(t *testing.T) {
	source, err := EmbeddedBundle()
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadBundle(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(Artifacts()) {
		t.Fatalf("embedded artifact count = %d", len(files))
	}
}
