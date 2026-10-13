package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// failingQuerier proves a code path never touches the database.
type failingQuerier struct{ calls int }

func (f *failingQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	f.calls++
	return errRow{errors.New("boom")}
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// TestSeedGuard pins D12 layer 2: a production start with an active seed
// credential is refused; any other environment is untouched.
func TestSeedGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("production with the seed user active is refused", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		if err := checkSeedGuard(ctx, "production", pool); !errors.Is(err, errSeedCredentialActive) {
			t.Fatalf("err = %v, want errSeedCredentialActive", err)
		}
	})

	t.Run("the environment name is matched case-insensitively and trimmed", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		if err := checkSeedGuard(ctx, " Production ", pool); !errors.Is(err, errSeedCredentialActive) {
			t.Fatalf("err = %v, want errSeedCredentialActive", err)
		}
	})

	t.Run("production passes once the seed user is removed", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE password_hash <> ''`); err != nil {
			t.Fatalf("arrange: %v", err)
		}
		if err := checkSeedGuard(ctx, "production", pool); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("a failed check in production refuses to start but is not reported as the seed", func(t *testing.T) {
		q := &failingQuerier{}
		err := checkSeedGuard(ctx, "production", q)
		if err == nil || errors.Is(err, errSeedCredentialActive) || q.calls != 1 {
			t.Fatalf("err = %v calls = %d, want a wrapped query error after one call", err, q.calls)
		}
	})

	for _, env := range []string{"", "development", "staging"} {
		t.Run("outside production ("+env+") the database is not even queried", func(t *testing.T) {
			q := &failingQuerier{}
			if err := checkSeedGuard(ctx, env, q); err != nil || q.calls != 0 {
				t.Fatalf("err = %v calls = %d, want nil and no query", err, q.calls)
			}
		})
	}
}
