package auth_test

import (
	"testing"
	"time"

	"github.com/livant05/rrhh-go/internal/auth"
)

func TestCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	cases := []struct {
		name  string
		hash  string
		plain string
		want  bool
	}{
		{"matching password", hash, "correct-horse", true},
		{"wrong password", hash, "wrong", false},
		{"empty hash never matches an empty password", "", "", false},
		{"empty hash never matches a real password", "", "correct-horse", false},
		{"garbage hash", "not-a-bcrypt-hash", "correct-horse", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.CheckPassword(tc.hash, tc.plain); got != tc.want {
				t.Fatalf("CheckPassword(%q, %q) = %v, want %v", tc.hash, tc.plain, got, tc.want)
			}
		})
	}
}

// TestCheckPassword_EmptyHashDoesBcryptWork pins AUTH-4: the empty-hash path
// must spend comparable work to a real comparison (a dummy bcrypt compare),
// not return early. A bcrypt compare at DefaultCost takes milliseconds; an
// early return takes nanoseconds.
func TestCheckPassword_EmptyHashDoesBcryptWork(t *testing.T) {
	auth.CheckPassword("", "warm-up") // first call may generate the dummy hash

	start := time.Now()
	auth.CheckPassword("", "correct-horse")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("empty-hash compare returned in %s; expected real bcrypt work", elapsed)
	}
}

func TestSigner_TTL(t *testing.T) {
	for _, ttl := range []time.Duration{24 * time.Hour, time.Hour} {
		if got := auth.NewSigner("secret", ttl).TTL(); got != ttl {
			t.Fatalf("TTL() = %s, want %s", got, ttl)
		}
	}
}
