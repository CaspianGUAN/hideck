package qdc507

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"testing/fstest"
)

func TestArtifactChecksSizeAndContent(t *testing.T) {
	data := []byte("verified runtime")
	a := Artifact{Name: "driver", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	for name, content := range map[string][]byte{"valid": data, "truncated": data[:3], "appended": append(append([]byte{}, data...), 0), "corrupt": []byte("modified runtime")} {
		t.Run(name, func(t *testing.T) {
			_, err := readArtifact(fstest.MapFS{"driver": &fstest.MapFile{Data: content}}, a)
			if (err == nil) != (name == "valid") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestUntrustedManifestCannotReplacePinnedHashes(t *testing.T) {
	source := fstest.MapFS{"manifest.json": &fstest.MapFile{Data: []byte(`{"files":[]}`)}}
	if _, err := ReadBundle(source); err == nil {
		t.Fatal("trusted supplied manifest")
	}
	manifest := Artifacts()
	manifest[0].Name = "changed"
	if Artifacts()[0].Name == "changed" {
		t.Fatal("manifest is mutable global state")
	}
}
