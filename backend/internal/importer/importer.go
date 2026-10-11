// Package importer loads a Supabase data export (one <table>.json per table,
// each a JSON array of PostgREST row objects) into the Go schema, preserving
// the original UUIDs. The whole import runs in one transaction; it is
// insert-only (ON CONFLICT (id) DO NOTHING), so re-runs are idempotent and
// seed rows are never touched.
package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidRows is returned (wrapped) when rows were rejected and
// SkipInvalid is off; nothing is written in that case.
var ErrInvalidRows = errors.New("importer: invalid rows")

// Options configures a run.
type Options struct {
	Dir         string // directory with <table>.json files
	DryRun      bool   // validate and report, then roll back
	SkipInvalid bool   // skip and report invalid rows instead of aborting
	// PasswordsOut is where the temporary passwords of newly created users are
	// written (CSV, 0600, never overwritten). Required only for a real run
	// that creates users; a dry run never writes it.
	PasswordsOut string

	// Test seams (nil in production). Commit replaces tx.Commit; Probe
	// replaces the post-failure check that a user id exists in the database.
	Commit func(ctx context.Context, tx pgx.Tx) error
	Probe  func(ctx context.Context, userID string) (bool, error)
}

// ErrCommitOutcomeUnknown is returned when the commit failed and whether the
// transaction reached the database could not be determined; the passwords
// file was kept on purpose.
var ErrCommitOutcomeUnknown = errors.New("importer: commit outcome unknown")

// Run imports the export in opts.Dir. The returned report is non-nil
// whenever the run got as far as reading the export, including on
// ErrInvalidRows, so callers can print what was wrong.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options) (*Report, error) {
	if !opts.DryRun {
		if err := checkPasswordsOut(opts.PasswordsOut); err != nil {
			return nil, err
		}
	}
	ignored, err := ignoredFiles(opts.Dir)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	rep := &Report{DryRun: opts.DryRun, Ignored: ignored}
	st := &runState{dryRun: opts.DryRun}
	for _, spec := range tables {
		tr, err := loadTable(ctx, tx, spec, opts, rep, st)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", spec.Name, err)
		}
		rep.Tables = append(rep.Tables, tr)
	}

	if len(rep.Invalid) > 0 && !opts.SkipInvalid {
		return rep, fmt.Errorf("%w: %d row(s) rejected, nothing was written", ErrInvalidRows, len(rep.Invalid))
	}
	rep.UsersCreated = len(st.creds)
	if opts.DryRun {
		return rep, nil // deferred Rollback discards everything
	}

	// The credentials are written before the commit: if the file cannot be
	// created exclusively, nothing is committed; if the commit fails, the
	// file is removed only once the rollback is confirmed.
	wroteFile := false
	if len(st.creds) > 0 {
		if opts.PasswordsOut == "" {
			return rep, fmt.Errorf("%w: %d new user(s)", ErrPasswordsOutRequired, len(st.creds))
		}
		if err := writePasswords(opts.PasswordsOut, st.creds); err != nil {
			return rep, err
		}
		wroteFile = true
	}
	commit := opts.Commit
	if commit == nil {
		commit = func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }
	}
	if err := commit(ctx, tx); err != nil {
		if !wroteFile {
			return rep, fmt.Errorf("commit: %w", err)
		}
		// The outcome of a failed commit is ambiguous (the connection may
		// drop after COMMIT was sent, or ctx may be cancelled mid-commit):
		// never delete the only copy of the passwords without checking.
		return rep, resolveCommitFailure(opts, pool, st.creds[0].ID, rep, err)
	}
	return rep, nil
}

// resolveCommitFailure checks, on a fresh context, whether the import was in
// fact committed, and decides the fate of the passwords file accordingly.
func resolveCommitFailure(opts Options, pool *pgxpool.Pool, userID string, rep *Report, commitErr error) error {
	vctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := opts.Probe
	if probe == nil {
		probe = func(ctx context.Context, id string) (bool, error) {
			var ok bool
			err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid)`, id).Scan(&ok)
			return ok, err
		}
	}
	committed, perr := probe(vctx, userID)
	switch {
	case perr != nil:
		return fmt.Errorf("%w: commit failed (%v) and the check failed (%v); the passwords file %s was KEPT on purpose, it may hold the only copy of the temporary passwords",
			ErrCommitOutcomeUnknown, commitErr, perr, opts.PasswordsOut)
	case committed:
		rep.CommitWarning = fmt.Sprintf("the commit reported an error (%v) but the import WAS committed; keep and distribute %s", commitErr, opts.PasswordsOut)
		return nil
	default:
		os.Remove(opts.PasswordsOut) // confirmed absent: those passwords belong to nothing
		return fmt.Errorf("commit: %w", commitErr)
	}
}

// loadTable reads one table's file and inserts its rows, each inside a
// savepoint so a rejected row does not poison the transaction.
func loadTable(ctx context.Context, tx pgx.Tx, spec tableSpec, opts Options, rep *Report, st *runState) (TableReport, error) {
	tr := TableReport{Name: spec.Name}
	rows, exists, err := readTable(opts.Dir, spec.Name)
	if err != nil {
		return tr, err
	}
	if !exists {
		tr.Missing = true
		return tr, nil
	}
	meta, err := loadMeta(ctx, tx, spec.Name)
	if err != nil {
		return tr, err
	}

	seen := map[string]bool{}
	uniq := newUniqueTracker(spec.Unique)
	dropped := map[string]bool{}
	reject := func(id, reason string) {
		tr.Invalid++
		rep.Invalid = append(rep.Invalid, InvalidRow{Table: spec.Name, ID: id, Reason: reason})
	}

	for _, row := range rows {
		tr.Read++
		id, err := rowID(row)
		if err != nil {
			reject("(no id)", err.Error())
			continue
		}
		if seen[id] {
			reject(id, "duplicate id in export")
			continue
		}
		seen[id] = true
		if reason := uniq.check(id, row); reason != "" {
			reject(id, reason)
			continue
		}

		inserted, drop, err := processRow(ctx, tx, spec, meta, row, st)
		for _, k := range drop {
			dropped[k] = true
		}
		if err != nil {
			reason, ok := invalidReason(err)
			if !ok {
				return tr, fmt.Errorf("row %s: %w", id, err)
			}
			reject(id, reason)
			continue
		}
		if inserted {
			tr.Inserted++
		} else {
			tr.Present++
		}
	}
	for k := range dropped {
		tr.Dropped = append(tr.Dropped, k)
	}
	sort.Strings(tr.Dropped)
	if spec.After != nil {
		if err := spec.After(ctx, tx, tr); err != nil {
			return tr, err
		}
	}
	return tr, nil
}

// processRow runs one row inside a savepoint: on error the savepoint is
// rolled back so the surrounding transaction stays usable.
func processRow(ctx context.Context, tx pgx.Tx, spec tableSpec, meta *tableMeta, row Row, st *runState) (inserted bool, dropped []string, err error) {
	st.pending = nil
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	if spec.Transform != nil {
		if err = spec.Transform(ctx, sp, row, st); err != nil {
			_ = sp.Rollback(ctx)
			return false, nil, err
		}
	}
	inserted, dropped, err = insertRow(ctx, sp, spec.Name, meta, row)
	if err != nil {
		_ = sp.Rollback(ctx)
		return false, dropped, err
	}
	if err := sp.Commit(ctx); err != nil {
		return false, dropped, err
	}
	if inserted && st.pending != nil {
		st.creds = append(st.creds, *st.pending)
	}
	return inserted, dropped, nil
}

// ErrPasswordsOutRequired is returned when a real run would create users but
// Options.PasswordsOut is empty.
var ErrPasswordsOutRequired = errors.New("importer: --passwords-out is required when new users are created")
