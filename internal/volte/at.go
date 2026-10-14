package volte

import (
	"fmt"
	"github.com/yibaiba/hideck/internal/mbn"
	"strconv"
	"strings"
)

type IMSConfig struct {
	IMSEnabled   bool
	VoLTEEnabled bool
	Raw          string
}

type USBConfig struct {
	VID           string
	PID           string
	Fields        []string
	UACEnabled    bool
	EnableCommand string
	Raw           string
}

func ParseIMSConfig(resp string) (IMSConfig, error) {
	line, ok := findQCFGLine(resp, "ims")
	if !ok {
		return IMSConfig{}, fmt.Errorf("volte: missing +QCFG: \"ims\" in %q", compactAT(resp))
	}
	parts := splitQCFGArgs(line)
	if len(parts) < 1 {
		return IMSConfig{}, fmt.Errorf("volte: malformed IMS config %q", line)
	}
	imsOn, err := parse01(parts[0])
	if err != nil {
		return IMSConfig{}, fmt.Errorf("volte: ims enable: %w", err)
	}
	volteOn := imsOn
	if len(parts) > 1 {
		volteOn, err = parse01(parts[1])
		if err != nil {
			return IMSConfig{}, fmt.Errorf("volte: volte enable: %w", err)
		}
	}
	return IMSConfig{IMSEnabled: imsOn, VoLTEEnabled: volteOn, Raw: line}, nil
}

func IMSEnableCommand() string {
	return `AT+QCFG="ims",1,1`
}

func IMSSetCommands(imsEnabled, volteEnabled bool) []string {
	i, v := 0, 0
	if imsEnabled {
		i = 1
	}
	if volteEnabled {
		v = 1
	}
	return []string{
		fmt.Sprintf(`AT+QCFG="ims",%d,%d`, i, v),
		fmt.Sprintf(`AT+QCFG="ims",%d`, i),
	}
}

func IMSQueryCommand() string {
	return `AT+QCFG="ims"?`
}

func USBConfigQueryCommand() string {
	return `AT+QCFG="usbcfg"?`
}

func IMEIQueryCommands() []string {
	return []string{"AT+CGSN", "AT+GSN"}
}

func MBNAutoSelQueryCommand() string {
	return `AT+QMBNCFG="autosel"`
}

func MBNListQueryCommand() string {
	return `AT+QMBNCFG="list"`
}

func MBNAutoSelSetCommand(enabled bool) string {
	v := 0
	if enabled {
		v = 1
	}
	return fmt.Sprintf(`AT+QMBNCFG="autosel",%d`, v)
}

func MBNSelectCommand(name string) string {
	return fmt.Sprintf(`AT+QMBNCFG="select","%s"`, strings.TrimSpace(name))
}

func MBNActivateCommand() string {
	return `AT+QMBNCFG="activate"`
}

func USBConfigSetCommand(fields []string) string {
	return `AT+QCFG=` + strings.Join(quoteUSBCFG(fields), ",")
}

func ParseIMEI(resp string) (string, error) {
	for _, raw := range strings.Split(resp, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.EqualFold(line, "OK") || strings.EqualFold(line, "ERROR") {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "AT+") {
			continue
		}
		if strings.HasPrefix(upper, "+CGSN:") {
			line = strings.TrimSpace(line[len("+CGSN:"):])
		} else if strings.HasPrefix(upper, "+GSN:") {
			line = strings.TrimSpace(line[len("+GSN:"):])
		}
		digits := make([]rune, 0, len(line))
		for _, r := range line {
			if r >= '0' && r <= '9' {
				digits = append(digits, r)
			}
		}
		if len(digits) == 15 {
			return string(digits), nil
		}
	}
	return "", fmt.Errorf("volte: missing IMEI in %q", compactAT(resp))
}

type MBNEntry struct {
	Index     int
	Selected  bool
	Activated bool
	Name      string
}

func ParseMBNAutoSel(resp string) (bool, string, error) {
	line, ok := findPrefixedArgs(resp, "+qmbncfg:", "autosel")
	if !ok {
		return false, "", fmt.Errorf("volte: missing +QMBNCFG: \"AutoSel\" in %q", compactAT(resp))
	}
	parts := splitQCFGArgs(line)
	if len(parts) < 1 {
		return false, line, fmt.Errorf("volte: malformed AutoSel %q", line)
	}
	on, err := parse01(parts[0])
	if err != nil {
		return false, line, fmt.Errorf("volte: autosel: %w", err)
	}
	return on, line, nil
}

func ParseMBNList(resp string) ([]MBNEntry, error) {
	profiles, err := mbn.ParseList(resp)
	if err != nil {
		return nil, err
	}
	entries := make([]MBNEntry, 0, len(profiles))
	for _, p := range profiles {
		entries = append(entries, MBNEntry{Index: p.Index, Name: p.Name, Selected: p.Selected, Activated: p.Activated})
	}
	return entries, nil
}

func SelectedMBN(entries []MBNEntry) (MBNEntry, bool) {
	var selected MBNEntry
	found := false
	for _, e := range entries {
		if e.Selected && e.Activated {
			return e, true
		}
		if e.Selected && !found {
			selected = e
			found = true
		}
	}
	return selected, found
}

func cutQuoted(s string) (quoted, before, after string, ok bool) {
	start := strings.Index(s, `"`)
	if start < 0 {
		return "", "", "", false
	}
	rest := s[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", "", "", false
	}
	return rest[:end], strings.TrimSpace(s[:start]), strings.TrimSpace(rest[end+1:]), true
}

func withUACFields(fields []string, enabled bool) []string {
	out := append([]string(nil), fields...)
	if len(out) == 0 {
		return out
	}
	bit := "0"
	if enabled {
		bit = "1"
	}
	out[len(out)-1] = bit
	return out
}

func canonHexID(v string) string {
	s := strings.TrimSpace(strings.ToLower(v))
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimLeft(s, "0")
	if s == "" {
		s = "0"
	}
	return "0x" + strings.ToUpper(s)
}

func sameUSBIdentity(a, b USBConfig) bool {
	return canonHexID(a.VID) == canonHexID(b.VID) && canonHexID(a.PID) == canonHexID(b.PID)
}

func isATError(resp string) bool {
	for _, raw := range strings.Split(resp, "\n") {
		line := strings.TrimSpace(raw)
		upper := strings.ToUpper(line)
		if upper == "ERROR" || strings.HasPrefix(upper, "+CME ERROR") || strings.HasPrefix(upper, "+CMS ERROR") {
			return true
		}
	}
	return false
}

func atResult(resp string, err error) error {
	if err != nil {
		return err
	}
	if isATError(resp) {
		return fmt.Errorf("AT ERROR (%s)", compactAT(resp))
	}
	return nil
}

func findPrefixedArgs(resp, prefix, key string) (string, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	prefix = strings.ToLower(prefix)
	for _, raw := range strings.Split(resp, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if !strings.Contains(lower, `"`+key+`"`) {
			continue
		}
		_, rest, ok := strings.Cut(line, ",")
		if !ok {
			return "", false
		}
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func IMEITail(imei string) string {
	imei = strings.TrimSpace(imei)
	if len(imei) <= 4 {
		return imei
	}
	return imei[len(imei)-4:]
}

func ParseUSBConfig(resp string) (USBConfig, error) {
	line, ok := findQCFGLine(resp, "usbcfg")
	if !ok {
		return USBConfig{}, fmt.Errorf("volte: missing +QCFG: \"usbcfg\" in %q", compactAT(resp))
	}
	parts := splitQCFGArgs(line)
	if len(parts) < 3 {
		return USBConfig{}, fmt.Errorf("volte: malformed usbcfg %q", line)
	}
	cfg := USBConfig{
		VID:    parts[0],
		PID:    parts[1],
		Fields: parts,
		Raw:    line,
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	cfg.UACEnabled = last == "1"
	if !cfg.UACEnabled {
		enabled := append([]string(nil), parts...)
		enabled[len(enabled)-1] = "1"
		cfg.EnableCommand = `AT+QCFG=` + strings.Join(quoteUSBCFG(enabled), ",")
	}
	return cfg, nil
}

func findQCFGLine(resp, key string) (string, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, raw := range strings.Split(resp, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		prefix := `+qcfg: "` + key + `"`
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		_, rest, ok := strings.Cut(line, ",")
		if !ok {
			return "", false
		}
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func splitQCFGArgs(args string) []string {
	parts := strings.Split(args, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func parse01(v string) (bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return false, err
	}
	switch n {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("expected 0 or 1, got %s", v)
	}
}

func quoteUSBCFG(parts []string) []string {
	out := make([]string, len(parts)+1)
	out[0] = `"usbcfg"`
	copy(out[1:], parts)
	return out
}

func compactAT(resp string) string {
	return strings.Join(strings.Fields(resp), " ")
}
