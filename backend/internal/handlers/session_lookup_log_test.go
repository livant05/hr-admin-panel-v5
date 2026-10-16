package handlers_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// The per-request session lookup runs on every authenticated request, so a
// database failure there must not be a silent 500: the error is logged with
// the user id.
func TestSessionCheck_LookupFailureIsLogged(t *testing.T) {
	live := testutil.Pool(t)
	broken, err := pgxpool.New(context.Background(), live.Config().ConnString())
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	broken.Close() // every query on it now fails

	var logs bytes.Buffer
	signer := auth.NewSigner(testutil.TestSecret, time.Hour)
	api := handlers.New(broken, signer, slog.New(slog.NewTextHandler(&logs, nil)))
	h := api.Routes(signer)

	userID := uuid.NewString()
	token, err := signer.Sign(userID, uuid.NewString(), "admin", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	rec := testutil.Do(t, h, http.MethodGet, "/api/me", token, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (%s)", rec.Code, rec.Body.String())
	}
	out := logs.String()
	if !strings.Contains(out, "session lookup") || !strings.Contains(out, userID) {
		t.Fatalf("expected a 'session lookup' error log with user_id %s, got: %q", userID, out)
	}
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "err=") {
		t.Fatalf("a database failure must be logged at ERROR with its error, got: %q", out)
	}
}

// sessionLookupFails delegates to the real pool but fails the GetSessionUser
// query with err.
type sessionLookupFails struct {
	db.DBTX
	err error
}

func (f sessionLookupFails) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "name: GetSessionUser") {
		return failedRow{f.err}
	}
	return f.DBTX.QueryRow(ctx, sql, args...)
}

// A client that goes away mid-request cancels the lookup's context. That is
// not an outage, so it stays out of the error level, keeping real database
// failures (one log line per authenticated request) easy to spot.
func TestSessionCheck_CanceledLookupIsNotLoggedAsAnError(t *testing.T) {
	pool := testutil.Pool(t)
	var logs bytes.Buffer
	signer := auth.NewSigner(testutil.TestSecret, time.Hour)
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(&logs, nil)))
	api.Queries = db.New(sessionLookupFails{pool, context.Canceled})
	h := api.Routes(signer)

	token, err := signer.Sign(uuid.NewString(), uuid.NewString(), "admin", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", token, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (%s)", rec.Code, rec.Body.String())
	}
	out := logs.String()
	if strings.Contains(out, "level=ERROR") {
		t.Fatalf("a canceled lookup must not log at error level, got: %q", out)
	}
	if !strings.Contains(out, "session lookup") {
		t.Fatalf("the canceled lookup should still be logged at a lower level, got: %q", out)
	}
}
