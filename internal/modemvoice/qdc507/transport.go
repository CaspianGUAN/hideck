package qdc507

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var ErrADBNotFound = errors.New("未找到 ADB 连接")

var usbLocation = regexp.MustCompile(`^[0-9]+-[0-9]+(?:\.[0-9]+)*$`)
var bootIdentity = regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
var deviceLine = regexp.MustCompile(`^(?:\(no serial number\)|\S+)\s+(\S+)\s+(.*)$`)

type transport struct{ id, usb string }

// A serial is intentionally not used: these modules can advertise the literal
// "(no serial number)". Only the current physical USB location selects a target.
func selectTransport(output, usb string) (transport, error) {
	if !usbLocation.MatchString(usb) {
		return transport{}, fmt.Errorf("qdc507: invalid USB location")
	}
	var matches []transport
	for _, line := range strings.Split(output, "\n") {
		parts := deviceLine.FindStringSubmatch(strings.TrimSpace(line))
		if parts == nil {
			continue
		}
		fields := strings.Fields(parts[2])
		for _, field := range fields {
			if field != "usb:"+usb {
				continue
			}
			if parts[1] != "device" {
				return transport{}, fmt.Errorf("qdc507: USB %s ADB transport is not ready", usb)
			}
			id := ""
			for _, f := range fields {
				if strings.HasPrefix(f, "transport_id:") {
					id = strings.TrimPrefix(f, "transport_id:")
				}
			}
			n, err := strconv.ParseUint(id, 10, 64)
			if err != nil || n == 0 {
				return transport{}, fmt.Errorf("qdc507: USB %s has no valid ADB transport ID", usb)
			}
			matches = append(matches, transport{id: id, usb: usb})
		}
	}
	if len(matches) == 0 {
		return transport{}, fmt.Errorf("qdc507: USB %s %w；请检查此模组是否已开启 ADB，以及宿主机或容器能否访问该 USB 接口", usb, ErrADBNotFound)
	}
	if len(matches) > 1 {
		return transport{}, fmt.Errorf("qdc507: USB %s 匹配到 %d 个 ADB 连接，无法唯一确认目标模组", usb, len(matches))
	}
	return matches[0], nil
}

const shellStatus = "__HIDECK_QDC507_STATUS__"

type ShellResult struct {
	Output string
	Status int
}

func parseShell(output string) (ShellResult, error) {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	marker := strings.LastIndex(output, "\n"+shellStatus)
	if marker < 0 {
		return ShellResult{}, fmt.Errorf("qdc507: remote shell exit status is missing")
	}
	status, err := strconv.Atoi(strings.TrimSpace(output[marker+1+len(shellStatus):]))
	if err != nil || status < 0 || status > 255 {
		return ShellResult{}, fmt.Errorf("qdc507: invalid remote shell exit status")
	}
	return ShellResult{Output: strings.TrimSpace(output[:marker]), Status: status}, nil
}

func quoteShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
