package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/testutil"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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
		if err := checkSeedGuard(ctx, discardLog(), "production", pool); !errors.Is(err, errSeedCredentialPresent) {
			t.Fatalf("err = %v, want errSeedCredentialPresent", err)
		}
	})

	t.Run("the environment name is matched case-insensitively and trimmed", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		if err := checkSeedGuard(ctx, discardLog(), " Production ", pool); !errors.Is(err, errSeedCredentialPresent) {
			t.Fatalf("err = %v, want errSeedCredentialPresent", err)
		}
	})

	t.Run("production passes once the seed user is removed", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		// A bystander with another hash must survive the cleanup, so a pass
		// proves the guard keys on the seed hash and not on "no users left".
		if _, err := pool.Exec(ctx, `INSERT INTO users (company_id, email, role, status, password_hash)
			SELECT company_id, 'bystander@example.test', 'admin', 'active', '$2a$10$notTheSeedHashAtAllXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX'
			FROM users LIMIT 1`); err != nil {
			t.Fatalf("arrange bystander: %v", err)
		}
		tag, err := pool.Exec(ctx, `DELETE FROM users WHERE password_hash = $1`, auth.SeedPasswordHash)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("arrange: deleted %d seed rows, err = %v, want exactly 1", tag.RowsAffected(), err)
		}
		if err := checkSeedGuard(ctx, discardLog(), "production", pool); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("production with only an inactive seed row is refused and the error says so", func(t *testing.T) {
		pool, _ := testutil.IsolatedSchema(t)
		if _, err := pool.Exec(ctx, `UPDATE users SET status = 'inactive' WHERE password_hash = $1`, auth.SeedPasswordHash); err != nil {
			t.Fatalf("arrange: %v", err)
		}
		err := checkSeedGuard(ctx, discardLog(), "production", pool)
		if !errors.Is(err, errSeedCredentialPresent) {
			t.Fatalf("err = %v, want errSeedCredentialPresent", err)
		}
		for _, want := range []string{"inactive", "delete"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("message %q does not mention %q", err.Error(), want)
			}
		}
	})

	t.Run("a failed check in production refuses to start but is not reported as the seed", func(t *testing.T) {
		q := &failingQuerier{}
		err := checkSeedGuard(ctx, discardLog(), "production", q)
		if err == nil || errors.Is(err, errSeedCredentialPresent) || q.calls != 1 {
			t.Fatalf("err = %v calls = %d, want a wrapped query error after one call", err, q.calls)
		}
	})

	for _, env := range []string{"", "development", "staging"} {
		t.Run("outside production ("+env+") the database is not even queried", func(t *testing.T) {
			q := &failingQuerier{}
			if err := checkSeedGuard(ctx, discardLog(), env, q); err != nil || q.calls != 0 {
				t.Fatalf("err = %v calls = %d, want nil and no query", err, q.calls)
			}
		})
	}

	for _, env := range []string{"development", "staging"} {
		t.Run("outside production ("+env+") one log line says the guard was skipped and for which APP_ENV", func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, nil))
			if err := checkSeedGuard(ctx, log, env, &failingQuerier{}); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			out := strings.TrimSpace(buf.String())
			if strings.Count(out, "\n") != 0 || !strings.Contains(out, "seed guard skipped") || !strings.Contains(out, "app_env="+env) {
				t.Fatalf("log = %q, want one line with %q and app_env=%s", out, "seed guard skipped", env)
			}
		})
	}

	t.Run("in production the skip line is not logged", func(t *testing.T) {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		pool, _ := testutil.IsolatedSchema(t)
		_ = checkSeedGuard(ctx, log, "production", pool)
		if strings.Contains(buf.String(), "skipped") {
			t.Fatalf("log = %q, production must not claim the guard was skipped", buf.String())
		}
	})
}
