package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// seedUserID is the Ventatec admin row 0003_seed.sql inserts.
const seedUserID = "7b03222e-c5b3-4912-9565-95dda72222f8"

// TestSeedCredentialPresent pins D12 / SR-3: production must never run while
// any user row, active or not, still carries the committed seed hash. Each case starts from
// a fresh schema that has the seed row exactly as an initdb volume does.
func TestSeedCredentialPresent(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate string // applied to the isolated schema before the check
		want   bool
	}{
		{"active seed user is detected", ``, true},
		{"inactive seed user is still detected (an UPDATE could reactivate it)", `UPDATE users SET status = 'inactive' WHERE id = '` + seedUserID + `'`, true},
		{"deleted seed user (runbook preflight done) passes", `DELETE FROM users WHERE id = '` + seedUserID + `'`, false},
		{"seed email with a different password is fine", `UPDATE users SET password_hash = '$2a$10$notTheSeedHashAtAllXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX' WHERE id = '` + seedUserID + `'`, false},
		{"the seed hash on another account is still detected", `UPDATE users SET id = uuid_generate_v4(), email = 'copy@example.test' WHERE id = '` + seedUserID + `'`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := testutil.IsolatedSchema(t)
			if tt.mutate != "" {
				if _, err := pool.Exec(ctx, tt.mutate); err != nil {
					t.Fatalf("arrange: %v", err)
				}
			}
			got, err := auth.SeedCredentialPresent(ctx, pool)
			if err != nil {
				t.Fatalf("SeedCredentialPresent: %v", err)
			}
			if got != tt.want {
				t.Fatalf("SeedCredentialPresent = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("a database error is returned, never read as no seed", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		got, err := auth.SeedCredentialPresent(cancelled, pool)
		if err == nil || got {
			t.Fatalf("got (%v, %v), want (false, error): a failed check must stop startup, not pass it", got, err)
		}
	})
}

// TestSeedPasswordHash_MatchesTheSeedMigration keeps the constant honest in
// both directions: it is the hash 0003_seed.sql inserts, and it is bcrypt of
// the documented dev password.
func TestSeedPasswordHash_MatchesTheSeedMigration(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0003_seed.sql"))
	if err != nil {
		t.Fatalf("read seed migration: %v", err)
	}
	if !strings.Contains(string(b), "'"+auth.SeedPasswordHash+"'") {
		t.Fatalf("auth.SeedPasswordHash is not the hash in 0003_seed.sql")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(auth.SeedPasswordHash), []byte("Admin123")); err != nil {
		t.Fatalf("auth.SeedPasswordHash is not bcrypt(Admin123): %v", err)
	}
}
