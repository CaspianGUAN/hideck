package qdc507

import (
	"errors"
	"io/fs"
	"testing"
)

func TestFindADBPrefersPrivatePackage(t *testing.T) {
	path, err := findADB(func(name string) (string, error) {
		if name != PackagedADBPath {
			t.Fatalf("unexpected system lookup: %s", name)
		}
		return name, nil
	}, func(string) (fs.FileInfo, error) { return nil, nil })
	if err != nil || path != PackagedADBPath {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestFindADBUsesSystemWithoutPrivatePackage(t *testing.T) {
	path, err := findADB(func(name string) (string, error) {
		if name == PackagedADBPath {
			t.Fatalf("unexpected lookup for absent package: %s", name)
		}
		return "/usr/bin/adb", nil
	}, func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist })
	if err != nil || path != "/usr/bin/adb" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestFindADBBrokenPrivatePackageDoesNotUseSystem(t *testing.T) {
	_, err := findADB(func(name string) (string, error) {
		if name != PackagedADBPath {
			t.Fatalf("unexpected system lookup: %s", name)
		}
		return "", fs.ErrPermission
	}, func(string) (fs.FileInfo, error) { return nil, nil })
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error=%v", err)
	}
}

func TestFindADBDanglingPrivateSymlinkDoesNotUseSystem(t *testing.T) {
	_, err := findADB(func(name string) (string, error) {
		if name != PackagedADBPath {
			t.Fatalf("unexpected system lookup: %s", name)
		}
		return "", fs.ErrNotExist
	}, func(string) (fs.FileInfo, error) { return nil, nil })
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error=%v", err)
	}
}
