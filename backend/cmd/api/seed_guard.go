package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/livant05/rrhh-go/internal/auth"
)

// errSeedCredentialPresent is returned when a production start finds a user
// row, active or inactive, with the committed development password hash. The
// text is what the operator reads in the log, so it says what to do.
var errSeedCredentialPresent = errors.New("seed credential present: a user row (active or inactive) still has the seed password hash; delete it before starting with APP_ENV=production (docs/cutover-runbook.md)")

// checkSeedGuard refuses a production start while the seed admin credential
// (migrations/0003_seed.sql, password known to anyone with the repository) is
// in the users table. An inactive row counts because reactivating it is one
// UPDATE away. APP_ENV is matched case-insensitively so "Production" is not a
// way to skip the check. A failing check also stops the start: an unverifiable
// database is not a clean one. Outside production nothing is queried and one
// log line records that the guard did not run, so a misspelled APP_ENV shows
// up in the logs instead of silently disabling the check.
func checkSeedGuard(ctx context.Context, log *slog.Logger, appEnv string, q auth.QueryRower) error {
	if !strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		log.Info("seed guard skipped: APP_ENV is not production", "app_env", appEnv)
		return nil
	}
	present, err := auth.SeedCredentialPresent(ctx, q)
	if err != nil {
		return err
	}
	if present {
		return errSeedCredentialPresent
	}
	return nil
}
