package main

import (
	"context"
	"errors"
	"strings"

	"github.com/livant05/rrhh-go/internal/auth"
)

// errSeedCredentialActive is returned when a production start finds an active
// user with the committed development password.
var errSeedCredentialActive = errors.New("seed credential active")

// checkSeedGuard refuses a production start while the seed admin credential
// (migrations/0003_seed.sql, password known to anyone with the repository) is
// live. APP_ENV is matched case-insensitively so "Production" is not a way to
// skip the check. A failing check also stops the start: an unverifiable
// database is not a clean one. Outside production nothing is queried.
func checkSeedGuard(ctx context.Context, appEnv string, q auth.QueryRower) error {
	if !strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		return nil
	}
	active, err := auth.SeedCredentialActive(ctx, q)
	if err != nil {
		return err
	}
	if active {
		return errSeedCredentialActive
	}
	return nil
}
