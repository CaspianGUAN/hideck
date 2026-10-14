package qdc507

import (
	"crypto/md5" // Required by the modem's legacy authorization protocol, not password storage.
	"fmt"
	"regexp"
	"strings"
)

var adbChallenge = regexp.MustCompile(`^[0-9]{8}$`)

func parseADBChallenge(response string) (string, error) {
	var challenge string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "OK" || line == "AT+QADBKEY?" {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "+QADBKEY:"))
		if !adbChallenge.MatchString(line) || challenge != "" {
			return "", fmt.Errorf("qdc507: 不支持的 ADB 授权挑战格式（内容已隐藏）")
		}
		challenge = line
	}
	if challenge == "" {
		return "", fmt.Errorf("qdc507: 未收到 ADB 授权挑战")
	}
	return challenge, nil
}

func deriveADBKey(challenge string) (string, error) {
	if !adbChallenge.MatchString(challenge) {
		return "", fmt.Errorf("qdc507: 无效的 ADB 授权挑战")
	}
	// Public firmware protocol constant, not a deployment credential. The
	// challenge-derived result must never be logged or persisted.
	password := []byte("SH_adb_quectel")
	salt := []byte(challenge)
	digest := initialADBDigest(password, salt)
	const cryptRounds = 1000
	for round := 0; round < cryptRounds; round++ {
		digest = nextADBDigest(adbDigestRound{password: password, salt: salt, digest: digest, round: round})
	}
	return encodeADBKey(digest), nil
}

func initialADBDigest(password, salt []byte) []byte {
	initial := md5.New()
	initial.Write(password)
	initial.Write([]byte("$1$"))
	initial.Write(salt)
	alternate := md5.Sum(append(append(append([]byte{}, password...), salt...), password...))
	initial.Write(alternate[:len(password)])
	for remaining := len(password); remaining > 0; remaining >>= 1 {
		if remaining&1 != 0 {
			initial.Write([]byte{0})
		} else {
			initial.Write(password[:1])
		}
	}
	return initial.Sum(nil)
}

type adbDigestRound struct {
	password, salt, digest []byte
	round                  int
}

func nextADBDigest(input adbDigestRound) []byte {
	password, salt, digest, round := input.password, input.salt, input.digest, input.round
	hash := md5.New()
	if round&1 != 0 {
		hash.Write(password)
	} else {
		hash.Write(digest)
	}
	if round%3 != 0 {
		hash.Write(salt)
	}
	if round%7 != 0 {
		hash.Write(password)
	}
	if round&1 != 0 {
		hash.Write(digest)
	} else {
		hash.Write(password)
	}
	return hash.Sum(nil)
}

func encodeADBKey(digest []byte) string {
	const alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var result strings.Builder
	// The firmware uses the first 15 characters of the MD5-crypt digest.
	for _, order := range [][3]int{{0, 6, 12}, {1, 7, 13}, {2, 8, 14}, {3, 9, 15}} {
		value := uint32(digest[order[0]])<<16 | uint32(digest[order[1]])<<8 | uint32(digest[order[2]])
		for digit := 0; digit < 4; digit++ {
			result.WriteByte(alphabet[value&63])
			value >>= 6
		}
	}
	return result.String()[:15]
}
