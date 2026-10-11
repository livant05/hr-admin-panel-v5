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
	"sort"

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
}

// Run imports the export in opts.Dir. The returned report is non-nil
// whenever the run got as far as reading the export, including on
// ErrInvalidRows, so callers can print what was wrong.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options) (*Report, error) {
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
	for _, spec := range tables {
		tr, err := loadTable(ctx, tx, spec, opts, rep)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", spec.Name, err)
		}
		rep.Tables = append(rep.Tables, tr)
	}

	if len(rep.Invalid) > 0 && !opts.SkipInvalid {
		return rep, fmt.Errorf("%w: %d row(s) rejected, nothing was written", ErrInvalidRows, len(rep.Invalid))
	}
	if opts.DryRun {
		return rep, nil // deferred Rollback discards everything
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("commit: %w", err)
	}
	return rep, nil
}

// loadTable reads one table's file and inserts its rows, each inside a
// savepoint so a rejected row does not poison the transaction.
func loadTable(ctx context.Context, tx pgx.Tx, spec tableSpec, opts Options, rep *Report) (TableReport, error) {
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

		inserted, drop, err := processRow(ctx, tx, spec.Name, meta, row)
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
	return tr, nil
}

// processRow runs one row inside a savepoint: on error the savepoint is
// rolled back so the surrounding transaction stays usable.
func processRow(ctx context.Context, tx pgx.Tx, table string, meta *tableMeta, row Row) (inserted bool, dropped []string, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	inserted, dropped, err = insertRow(ctx, sp, table, meta, row)
	if err != nil {
		_ = sp.Rollback(ctx)
		return false, dropped, err
	}
	return inserted, dropped, sp.Commit(ctx)
}
