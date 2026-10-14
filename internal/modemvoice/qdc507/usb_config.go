package qdc507

import (
	"fmt"
	"strconv"
	"strings"
)

const adbUSBFlag = 5 // diag, nmea, at, modem, rmnet, adb, uac

type usbConfig struct {
	vid, pid uint64
	flags    [7]uint8
}

func parseUSBConfig(response string) (usbConfig, error) {
	var config usbConfig
	var found bool
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `+QCFG: "usbcfg",`) {
			continue
		}
		if found {
			return config, fmt.Errorf("qdc507: USB 配置响应不唯一")
		}
		var err error
		config, err = parseUSBFields(strings.Split(strings.TrimPrefix(line, `+QCFG: "usbcfg",`), ","))
		if err != nil {
			return config, err
		}
		found = true
	}
	if !found {
		return config, fmt.Errorf("qdc507: 未获得已适配的 USB 配置")
	}
	return config, nil
}

func parseUSBFields(fields []string) (usbConfig, error) {
	var config usbConfig
	if len(fields) != 9 {
		return config, fmt.Errorf("qdc507: 不支持的 USB 配置字段数")
	}
	var err error
	config.vid, err = strconv.ParseUint(strings.TrimSpace(fields[0]), 0, 16)
	if err != nil || config.vid == 0 {
		return config, fmt.Errorf("qdc507: 无效的 USB VID")
	}
	config.pid, err = strconv.ParseUint(strings.TrimSpace(fields[1]), 0, 16)
	if err != nil || config.pid == 0 {
		return config, fmt.Errorf("qdc507: 无效的 USB PID")
	}
	for index, value := range fields[2:] {
		switch strings.TrimSpace(value) {
		case "0":
			config.flags[index] = 0
		case "1":
			config.flags[index] = 1
		default:
			return config, fmt.Errorf("qdc507: 不支持的 USB 功能位")
		}
	}
	return config, nil
}

func (config usbConfig) command() string {
	command := fmt.Sprintf(`AT+QCFG="usbcfg",0x%X,0x%X`, config.vid, config.pid)
	for _, flag := range config.flags {
		command += fmt.Sprintf(",%d", flag)
	}
	return command
}
