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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// The per-request session lookup runs on every authenticated request, so a
// database failure there must not be a silent 500: the error is logged with
// the user id (review finding R4).
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
}
