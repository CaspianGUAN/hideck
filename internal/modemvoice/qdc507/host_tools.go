package qdc507

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

const PackagedADBPath = "/usr/libexec/hideck/adb"

// FindADB prefers our optional OpenWrt package without replacing system adb.
// A broken private installation is an error, not permission to select another ADB.
func FindADB() (string, error) { return findADB(exec.LookPath, os.Lstat) }

func findADB(lookup func(string) (string, error), lstat func(string) (fs.FileInfo, error)) (string, error) {
	_, statErr := lstat(PackagedADBPath)
	if statErr == nil {
		path, err := lookup(PackagedADBPath)
		if err != nil {
			return "", fmt.Errorf("HiDeck 专用 ADB 不可用：%w", err)
		}
		return path, nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("检查 HiDeck 专用 ADB 失败：%w", statErr)
	}
	path, err := lookup("adb")
	if err != nil {
		return "", fmt.Errorf("未找到 ADB，请安装 Android platform-tools；OpenWrt 请安装 hideck-adb：%w", err)
	}
	return path, nil
}
