package qdc507

import (
	"embed"
	"io/fs"
)

// Runtime files and their upstream license are shipped together. ReadBundle
// still checks every pinned digest before anything is uploaded to the modem.
//
//go:embed assets/*
var embeddedRuntime embed.FS

func EmbeddedBundle() (fs.FS, error) {
	return fs.Sub(embeddedRuntime, "assets")
}
