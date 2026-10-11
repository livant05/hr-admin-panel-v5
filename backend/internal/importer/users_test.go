package importer_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/importer"
	"github.com/livant05/rrhh-go/internal/testutil"
)

const (
	user1 = "dddddddd-dddd-4ddd-8ddd-ddddddddddd1"
	user2 = "dddddddd-dddd-4ddd-8ddd-ddddddddddd2"
)

func usersExport(extra ...map[string]any) map[string]rows {
	exp := map[string]rows{
		"companies": {{"id": coID, "name": "Acme Test"}},
		"users": {
			{"id": user1, "company_id": coID, "email": "ana@example.test", "role": "admin", "first_name": "Ana", "last_name": "Prueba", "status": "active"},
			{"id": user2, "company_id": coID, "email": "beto@example.test", "role": "empleado", "first_name": "Beto", "last_name": "Muestra", "status": "active"},
		},
	}
	exp["users"] = append(exp["users"], extra...)
	return exp
}

func readCreds(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open passwords file: %v", err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != "email,temporary_password" {
		t.Fatalf("csv header = %v, want email,temporary_password", recs)
	}
	out := map[string]string{}
	for _, r := range recs[1:] {
		out[r[0]] = r[1]
	}
	return out
}

func TestUsers_GetTemporaryPasswordsWrittenToProtectedCSV(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	exp := usersExport()
	// The seed admin is in the export too (and a stray password_hash is ignored).
	exp["users"] = append(exp["users"], map[string]any{
		"id": seedAdmin, "company_id": seedCompany, "email": "livan.05@hotmail.com", "role": "admin", "password_hash": "ignored"})
	exp["companies"] = append(exp["companies"], map[string]any{"id": seedCompany, "name": "Ventatec"})

	var before string
	if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, seedAdmin).Scan(&before); err != nil {
		t.Fatal(err)
	}

	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), PasswordsOut: csvPath})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.UsersCreated != 2 {
		t.Fatalf("UsersCreated = %d, want 2", rep.UsersCreated)
	}
	st, err := os.Stat(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("csv mode = %v, want 0600", st.Mode().Perm())
	}
	creds := readCreds(t, csvPath)
	if len(creds) != 2 {
		t.Fatalf("csv rows = %v, want the 2 new users only", creds)
	}
	seen := map[string]bool{}
	for email, pw := range creds {
		if len(pw) < 16 {
			t.Errorf("password for %s too short (%d)", email, len(pw))
		}
		if seen[pw] {
			t.Errorf("password reused across users")
		}
		seen[pw] = true
		var hash string
		if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = $1`, email).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash == pw || !auth.CheckPassword(hash, pw) {
			t.Errorf("stored hash for %s does not verify the CSV password (or is plaintext)", email)
		}
	}
	var after string
	if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, seedAdmin).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("seed admin password hash changed")
	}

	// Passwords never reach the printed report.
	var buf bytes.Buffer
	rep.Write(&buf)
	for _, pw := range creds {
		if strings.Contains(buf.String(), pw) {
			t.Fatalf("report leaks a temporary password")
		}
	}
}

func TestUsers_RerunNeedsNoPasswordsFile(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	dir := writeExport(t, usersExport())
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	if _, err := run(t, pool, importer.Options{Dir: dir, PasswordsOut: csvPath}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, err := run(t, pool, importer.Options{Dir: dir}) // no --passwords-out, nothing new
	if err != nil {
		t.Fatalf("rerun without passwords file: %v", err)
	}
	if rep.UsersCreated != 0 || rep.Table("users").Present != 2 {
		t.Fatalf("rerun: created=%d users=%+v", rep.UsersCreated, rep.Table("users"))
	}
}

func TestUsers_NewUsersRequirePasswordsOut(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	_, err := run(t, pool, importer.Options{Dir: writeExport(t, usersExport())})
	if !errors.Is(err, importer.ErrPasswordsOutRequired) {
		t.Fatalf("err = %v, want ErrPasswordsOutRequired", err)
	}
	if got := count(t, pool, `SELECT count(*) FROM users WHERE id IN ($1,$2)`, user1, user2); got != 0 {
		t.Fatalf("users were committed without a passwords file")
	}
}

func TestUsers_ExistingPasswordsFileIsNeverOverwritten(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	if err := os.WriteFile(csvPath, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, pool, importer.Options{Dir: writeExport(t, usersExport()), PasswordsOut: csvPath})
	if err == nil {
		t.Fatal("expected an error when the passwords file exists")
	}
	if b, _ := os.ReadFile(csvPath); string(b) != "precious" {
		t.Fatalf("existing file was modified: %q", b)
	}
	if got := count(t, pool, `SELECT count(*) FROM users WHERE id IN ($1,$2)`, user1, user2); got != 0 {
		t.Fatalf("users were committed although the passwords file could not be created")
	}
}

func TestUsers_DryRunCountsButWritesNoFile(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, usersExport()), DryRun: true, PasswordsOut: csvPath})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if rep.UsersCreated != 2 {
		t.Fatalf("dry run UsersCreated = %d, want 2", rep.UsersCreated)
	}
	if _, err := os.Stat(csvPath); !os.IsNotExist(err) {
		t.Fatalf("dry run created the passwords file (err=%v)", err)
	}
	// And a dry run without the flag still works: the flag is only enforced for real runs.
	if _, err := run(t, pool, importer.Options{Dir: writeExport(t, usersExport()), DryRun: true}); err != nil {
		t.Fatalf("dry run without --passwords-out: %v", err)
	}
	if got := count(t, pool, `SELECT count(*) FROM users WHERE id IN ($1,$2)`, user1, user2); got != 0 {
		t.Fatalf("dry run persisted users")
	}
}

func TestUsers_EmailCollisionsAndMissingEmailAreInvalid(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := usersExport(
		// same email as the seed admin, different id (case-insensitive)
		map[string]any{"id": "dddddddd-dddd-4ddd-8ddd-ddddddddddd3", "company_id": coID, "email": "LIVAN.05@hotmail.com"},
		// same email as user1 inside the export
		map[string]any{"id": "dddddddd-dddd-4ddd-8ddd-ddddddddddd4", "company_id": coID, "email": "Ana@Example.test"},
		// no email: cannot receive a login
		map[string]any{"id": "dddddddd-dddd-4ddd-8ddd-ddddddddddd5", "company_id": coID, "email": nil},
	)
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), PasswordsOut: csvPath, SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("users")
	if tr.Inserted != 2 || tr.Invalid != 3 {
		t.Fatalf("users = %+v; invalid=%+v", tr, rep.Invalid)
	}
	if got := readCreds(t, csvPath); len(got) != 2 {
		t.Fatalf("csv must hold only the inserted users, got %v", got)
	}
}

func TestUsers_AbortedRunLeavesNoPasswordsFile(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := usersExport()
	exp["departments"] = rows{{"id": deptID, "company_id": "ffffffff-ffff-4fff-8fff-ffffffffffff", "name": "Orphan"}} // FK violation
	csvPath := filepath.Join(t.TempDir(), "passwords.csv")
	_, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), PasswordsOut: csvPath})
	if !errors.Is(err, importer.ErrInvalidRows) {
		t.Fatalf("err = %v, want ErrInvalidRows", err)
	}
	if _, err := os.Stat(csvPath); !os.IsNotExist(err) {
		t.Fatalf("aborted run left a passwords file (err=%v)", err)
	}
}
