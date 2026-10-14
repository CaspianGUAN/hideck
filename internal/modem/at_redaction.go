package modem

import "strings"

func isADBAuthCommand(command string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(command)), "AT+QADBKEY")
}

func logATCommand(command string) string {
	if isADBAuthCommand(command) {
		return "AT+QADBKEY [redacted]"
	}
	return command
}

func logATResponse(command, response string) string {
	if isADBAuthCommand(command) || hasADBAuthResponse(response) {
		return "[ADB authorization redacted]"
	}
	return response
}

func hasADBAuthResponse(response string) bool {
	if strings.Contains(strings.ToUpper(response), "QADBKEY") {
		return true
	}
	// An unframed legacy challenge can arrive late, after its request timed out.
	for _, line := range strings.FieldsFunc(response, func(r rune) bool { return r == '\n' || r == '|' }) {
		line = strings.TrimSpace(line)
		if len(line) == 8 && strings.Trim(line, "0123456789") == "" {
			return true
		}
	}
	return false
}
