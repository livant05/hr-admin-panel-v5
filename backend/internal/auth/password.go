package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Password length policy, counted in bytes: bcrypt silently ignores everything
// past 72 bytes, so a longer password would be accepted but only partly
// checked (AUTH-7).
const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 72
)

// tempPasswordBytes of entropy per temporary password; base64url gives 24
// characters, inside the policy bounds.
const tempPasswordBytes = 18

// ValidatePassword enforces the server-side password policy: 8 to 72 bytes.
func ValidatePassword(pw string) error {
	switch n := len(pw); {
	case n < MinPasswordBytes:
		return fmt.Errorf("password must be at least %d bytes", MinPasswordBytes)
	case n > MaxPasswordBytes:
		return fmt.Errorf("password must be at most %d bytes", MaxPasswordBytes)
	}
	return nil
}

// GenerateTempPassword returns a random 24-character base64url password that
// satisfies ValidatePassword. Callers show it once and store only its bcrypt
// hash; it must never be logged.
func GenerateTempPassword() (string, error) {
	b := make([]byte, tempPasswordBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
