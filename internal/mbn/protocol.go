// Package mbn manages modem carrier profiles without provisioning IMS or USB audio.
package mbn

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Profile struct {
	Index     int    `json:"index"`
	Selected  bool   `json:"selected"`
	Activated bool   `json:"activated"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Date      string `json:"date"`
	Origin    string `json:"origin"`
}

type Capabilities struct {
	List    bool   `json:"list"`
	Select  bool   `json:"select"`
	AutoSel bool   `json:"autosel"`
	Add     bool   `json:"add"`
	Delete  bool   `json:"delete"`
	Export  bool   `json:"export"`
	Raw     string `json:"raw"`
}

// ValidateName rejects AT delimiters rather than silently rewriting a profile.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\"\\\r\n\x00") {
		return errors.New("MBN 名称为空或包含 AT 控制字符")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return errors.New("MBN 名称包含控制字符")
		}
	}
	return nil
}

func NamedCommand(action, name string) (string, error) {
	if action != "Select" && action != "Delete" && action != "Add" {
		return "", errors.New("未知 MBN 操作")
	}
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return fmt.Sprintf(`AT+QMBNCFG="%s","%s"`, action, name), nil
}

func CheckResponse(response string, err error) error {
	if err != nil {
		return err
	}
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "ERROR" || strings.HasPrefix(line, "+CME ERROR") || strings.HasPrefix(line, "+CMS ERROR") {
			return fmt.Errorf("MBN 命令被模组拒绝: %s", line)
		}
	}
	return nil
}

func ParseCapabilities(response string) (Capabilities, error) {
	c := Capabilities{Raw: response}
	if err := CheckResponse(response, nil); err != nil {
		return c, err
	}
	for _, line := range strings.Split(response, "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "+qmbncfg:") {
			continue
		}
		for _, token := range strings.FieldsFunc(strings.ToLower(line), func(r rune) bool { return r == '"' || r == ',' || r == '(' || r == ')' }) {
			switch strings.TrimSpace(token) {
			case "list":
				c.List = true
			case "select":
				c.Select = true
			case "autosel":
				c.AutoSel = true
			case "add":
				c.Add = true
			case "delete":
				c.Delete = true
			}
		}
	}
	if !c.List {
		return c, errors.New("QMBNCFG 未声明 List 能力，无法确认该固件支持 MBN 管理")
	}
	return c, nil
}

func responseFields(line string) ([]string, error) {
	_, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return nil, errors.New("MBN 响应缺少分隔符")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(rest)))
	r.TrimLeadingSpace = true
	return r.Read()
}

func ParseList(response string) ([]Profile, error) {
	if err := CheckResponse(response, nil); err != nil {
		return nil, err
	}
	profiles := []Profile{}
	for _, line := range strings.Split(response, "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "+qmbncfg:") {
			continue
		}
		fields, err := responseFields(line)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 || !strings.EqualFold(fields[0], "list") {
			continue
		}
		if len(fields) < 5 {
			return nil, errors.New("MBN List 响应字段不完整")
		}
		index, err := strconv.Atoi(fields[1])
		if err != nil || index < 0 {
			return nil, errors.New("MBN 索引无效")
		}
		selected, err := parseBit(fields[2])
		if err != nil {
			return nil, err
		}
		activated, err := parseBit(fields[3])
		if err != nil {
			return nil, err
		}
		if err := ValidateName(fields[4]); err != nil {
			return nil, err
		}
		p := Profile{Index: index, Selected: selected, Activated: activated, Name: fields[4], Origin: "unknown"}
		if len(fields) > 5 {
			p.Version = fields[5]
		}
		if len(fields) > 6 {
			p.Date = fields[6]
		}
		profiles = append(profiles, p)
	}
	if len(profiles) == 0 {
		return nil, errors.New("MBN List 未返回可验证的配置记录")
	}
	return profiles, nil
}

func ParseAutoSel(response string) (bool, error) {
	if err := CheckResponse(response, nil); err != nil {
		return false, err
	}
	for _, line := range strings.Split(response, "\n") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "+qmbncfg:") {
			continue
		}
		fields, err := responseFields(line)
		if err != nil {
			return false, err
		}
		if len(fields) == 2 && strings.EqualFold(fields[0], "autosel") {
			return parseBit(fields[1])
		}
	}
	return false, errors.New("缺少 AutoSel 响应")
}

func parseBit(s string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("无效的 MBN 状态 %q", s)
}
