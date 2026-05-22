package token

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// New returns a 32-character lowercase base32 token (no padding).
func New() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return strings.ToLower(enc.EncodeToString(raw)), nil
}
