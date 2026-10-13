package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/auth"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"7 bytes is too short", strings.Repeat("a", 7), true},
		{"8 bytes is the minimum", strings.Repeat("a", 8), false},
		{"72 bytes is the maximum", strings.Repeat("a", 72), false},
		{"73 bytes is too long", strings.Repeat("a", 73), true},
		{"empty", "", true},
		// Length is counted in bytes (bcrypt truncates at 72 bytes), not runes.
		{"37 two-byte runes is 74 bytes and rejected despite 37 chars", strings.Repeat("é", 37), true},
		{"36 two-byte runes is exactly 72 bytes", strings.Repeat("é", 36), false},
		{"3 three-byte runes is 9 bytes and accepted despite 3 chars", strings.Repeat("€", 3), false},
		{"2 three-byte runes is 6 bytes", strings.Repeat("€", 2), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.ValidatePassword(tt.pw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePassword(%d bytes) error = %v, wantErr %v", len(tt.pw), err, tt.wantErr)
			}
		})
	}
}

func TestGenerateTempPassword(t *testing.T) {
	urlSafe := regexp.MustCompile(`^[A-Za-z0-9_-]{24}$`)

	first, err := auth.GenerateTempPassword()
	if err != nil {
		t.Fatalf("GenerateTempPassword: %v", err)
	}
	if !urlSafe.MatchString(first) {
		t.Fatalf("temporary password %q must be 24 base64url characters", first)
	}
	if err := auth.ValidatePassword(first); err != nil {
		t.Fatalf("generated password must satisfy the policy: %v", err)
	}

	second, err := auth.GenerateTempPassword()
	if err != nil {
		t.Fatalf("GenerateTempPassword: %v", err)
	}
	if first == second {
		t.Fatalf("two calls returned the same password %q", first)
	}
}
