package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var haproxyUsersFilePath = "/opt/app/haproxy/data/users.csv"

func buildHaproxyUsersContent(users []HaproxyUserEntry) string {
	if len(users) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(users) * 120)

	hasEntries := false
	for _, user := range users {
		username := strings.TrimSpace(user.Username)
		if username == "" {
			continue
		}
		if uuid := strings.TrimSpace(user.VLESSUUID); uuid != "" {
			b.WriteString(username)
			b.WriteByte(',')
			b.WriteString(uuid)
			b.WriteByte('\n')
			hasEntries = true
		}
		if trojan := normalizeTrojanHash(user.TrojanPassword); trojan != "" {
			b.WriteString(username)
			b.WriteByte(',')
			b.WriteString(trojan)
			b.WriteByte('\n')
			hasEntries = true
		}
		if anytls := normalizeAnytlsHash(user.AnytlsPassword); anytls != "" {
			b.WriteString(username)
			b.WriteByte(',')
			b.WriteString(anytls)
			b.WriteByte('\n')
			hasEntries = true
		}
		if naive := normalizeNaiveToken(username, user.NaivePassword); naive != "" {
			b.WriteString(username)
			b.WriteString(",basic:")
			b.WriteString(naive)
			b.WriteByte('\n')
			hasEntries = true
		}
	}

	if !hasEntries {
		return ""
	}
	return b.String()
}

func applyHaproxyModule(modules DeployModulesPayload) (bool, error) {
	if !modules.HaproxyEnabled {
		err := os.Remove(haproxyUsersFilePath)
		switch {
		case err == nil:
			return true, nil
		case os.IsNotExist(err):
			return false, nil
		default:
			return false, fmt.Errorf("remove haproxy users file: %w", err)
		}
	}

	content := buildHaproxyUsersContent(modules.HaproxyUsers)

	if err := os.MkdirAll(filepath.Dir(haproxyUsersFilePath), 0o755); err != nil {
		return false, fmt.Errorf("create haproxy data dir: %w", err)
	}

	existing, readErr := os.ReadFile(haproxyUsersFilePath)
	if readErr == nil && bytes.Equal(existing, []byte(content)) {
		return false, nil
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		return false, fmt.Errorf("read haproxy users file: %w", readErr)
	}
	if err := os.WriteFile(haproxyUsersFilePath, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("write haproxy users file: %w", err)
	}

	return true, nil
}

func normalizeTrojanHash(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	sum := sha256.Sum224([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeAnytlsHash(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeNaiveToken(username, secret string) string {
	username = strings.TrimSpace(username)
	secret = strings.TrimSpace(secret)
	if username == "" || secret == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + secret))
}
