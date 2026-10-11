package importer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/livant05/rrhh-go/internal/auth"
)

// passwordBytes of entropy per temporary password; base64url gives 24 chars.
const passwordBytes = 18

// credential is one new user's login. The password lives only in memory until
// the CSV is written; it is never logged or printed.
type credential struct {
	ID       string // user id, used to verify an ambiguous commit
	Email    string
	Password string
}

// runState carries per-run data shared between rows and tables.
type runState struct {
	dryRun  bool
	pending *credential  // set by a transform for the row being processed
	creds   []credential // credentials of users actually inserted
}

func generatePassword() (string, error) {
	b := make([]byte, passwordBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// transformUser gives every user that is not already in the database a random
// temporary password (bcrypt via auth.HashPassword) and refuses emails that
// belong to a different user. Users already present are left untouched.
func transformUser(ctx context.Context, q querier, row Row, st *runState) error {
	id := stringField(row, "id")
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil // ON CONFLICT (id) DO NOTHING keeps the stored row, hash included
	}

	email := strings.TrimSpace(stringField(row, "email"))
	if email == "" {
		return invalidf("email: required to issue a temporary password")
	}
	var taken bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1) AND id <> $2::uuid)`, email, id,
	).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return invalidf("email: already used by a different user")
	}

	cred := &credential{ID: id, Email: email}
	delete(row, "password_hash") // an export never carries Supabase's hashes; never trust one
	if st.dryRun {
		st.pending = cred // counted only; no password is generated or hashed
		return nil
	}
	pw, err := generatePassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	row["password_hash"] = hash
	cred.Password = pw
	st.pending = cred
	return nil
}

// checkPasswordsOut fails fast when the target file already exists, before any
// database work.
func checkPasswordsOut(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("passwords file %s already exists; refusing to overwrite it", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("passwords file: %w", err)
	}
	return nil
}

// writePasswords creates path exclusively with mode 0600 and writes
// email,temporary_password rows. On any failure the partial file is removed.
func writePasswords(path string, creds []credential) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create passwords file: %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(path)
		}
	}()
	w := csv.NewWriter(f)
	if err = w.Write([]string{"email", "temporary_password"}); err != nil {
		return err
	}
	for _, c := range creds {
		if err = w.Write([]string{c.Email, c.Password}); err != nil {
			return err
		}
	}
	w.Flush()
	if err = w.Error(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
