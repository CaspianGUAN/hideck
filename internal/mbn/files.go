package mbn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// DiskFiles is infrastructure; the service receives it through Files.
type DiskFiles struct{ Directory string }

func (f DiskFiles) path(id string) (string, error) {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("无效的 MBN 文件摘要")
	}
	return filepath.Join(f.Directory, id+".mbn"), nil
}
func (f DiskFiles) Read(id string) ([]byte, error) {
	path, err := f.path(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
func (f DiskFiles) Put(id string, data []byte) error {
	path, err := f.path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.Directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(f.Directory, ".upload-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func (f DiskFiles) Delete(id string) error {
	path, err := f.path(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
