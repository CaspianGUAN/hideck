// Package qdc507 manages the explicitly selected QDC507 module voice runtime.
// Host tools and the module's ARM runtime have separate compatibility checks.
package qdc507

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
)

const (
	RuntimeVersion = "qdc507-3.18.44-voice-20260712.5"
	SourceRevision = "0443dfdaf8aec086fd76ba2ee9152fd908114524"
	SourceURL      = "https://raw.githubusercontent.com/moluncn/mavo/" + SourceRevision + "/Resources/ModuleVoice/"
	KernelRelease  = "3.18.44"
)

type Artifact struct {
	Name   string
	Size   int64
	SHA256 string
}

type Upload struct {
	Name string
	Data []byte
}

func pinnedArtifact(name string) (Artifact, bool) {
	for _, a := range Artifacts() {
		if a.Name == name {
			return a, true
		}
	}
	return Artifact{}, false
}

func verifyBytes(data []byte, artifact Artifact) error {
	if int64(len(data)) != artifact.Size {
		return fmt.Errorf("qdc507: invalid size for %s", artifact.Name)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != artifact.SHA256 {
		return fmt.Errorf("qdc507: SHA-256 mismatch for %s", artifact.Name)
	}
	return nil
}

// Artifacts returns a fresh manifest. Metadata and license files are pinned too;
// an upstream manifest can never replace the hashes trusted by this program.
func Artifacts() []Artifact {
	return []Artifact{
		{"qdc507_aprv3.ko", 36664, "3d82d3dec4f1e323201bba87156df9d41438e08314097353f2607f9117211d4a"},
		{"qdc507_voice.ko", 999236, "ed3821682d5309969a01c764192c83feff9669c61ef237c69475cd1619cf296c"},
		{"mavo-pcm-bridge.armv7", 17860, "88d47c15e61d1428a59c821fed804c2e6490e82859a085062f21966b58d167fc"},
		{"manifest.json", 729, "f4f6c266ced7015d4e61d993a6e31247c26a9e85a8fdf1c6d842c459e1e2970a"},
		{"COPYING-GPL-2.0", 18693, "af8067302947c01fd9eee72befa54c7e3ef8a48fecde7fd71277f2290b2bf0f7"},
		{"MODULE-REPORT.md", 7443, "fb9d58336bcfdad8938d7833c113a815c2153d9a04564eb73cddabea737f8be2"},
	}
}

// ReadBundle reads and verifies a snapshot before deployment, avoiding a local
// verify-then-reopen race. The largest file is fixed at less than one megabyte.
func ReadBundle(source fs.FS) (map[string][]byte, error) {
	if source == nil {
		return nil, fmt.Errorf("qdc507: runtime source is required")
	}
	files := make(map[string][]byte)
	for _, artifact := range Artifacts() {
		data, err := readArtifact(source, artifact)
		if err != nil {
			return nil, err
		}
		files[artifact.Name] = data
	}
	return files, nil
}

func readArtifact(source fs.FS, artifact Artifact) ([]byte, error) {
	f, err := source.Open(artifact.Name)
	if err != nil {
		return nil, fmt.Errorf("qdc507: open %s: %w", artifact.Name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, artifact.Size+1))
	if err != nil {
		return nil, fmt.Errorf("qdc507: read %s: %w", artifact.Name, err)
	}
	if err := verifyBytes(data, artifact); err != nil {
		return nil, err
	}
	return data, nil
}
