package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is the subset of pgx.Tx the importer needs; a savepoint (nested
// pgx.Tx) satisfies it too.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// invalidError marks a row the importer refuses; it is reported, never fatal.
type invalidError struct{ reason string }

func (e *invalidError) Error() string { return e.reason }

func invalidf(format string, args ...any) error {
	return &invalidError{reason: fmt.Sprintf(format, args...)}
}

// invalidReason classifies err as a row-level rejection: an importer
// validation error or a data/integrity error (SQLSTATE class 22 or 23) from
// Postgres. Anything else (connection loss, syntax error) is fatal.
// pgErr.Detail is deliberately not used for NOT NULL / unique violations: for
// those Postgres puts the whole failing row (personal data) in it.
func invalidReason(err error) (string, bool) {
	var ie *invalidError
	if errors.As(err, &ie) {
		return ie.reason, true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	if !strings.HasPrefix(pgErr.Code, "22") && !strings.HasPrefix(pgErr.Code, "23") {
		return "", false
	}
	reason := pgErr.Message
	if pgErr.ColumnName != "" {
		reason += " (column " + pgErr.ColumnName + ")"
	}
	if pgErr.Code == "23503" && pgErr.Detail != "" { // FK detail only carries key ids
		reason += ": " + pgErr.Detail
	}
	return reason, true
}

type column struct {
	Name string
	Type string // information_schema udt_name, e.g. uuid, int4, jsonb
}

// tableMeta is the live column set of a table, minus generated columns.
type tableMeta struct {
	cols      map[string]column
	generated map[string]bool
}

// loadMeta reads the columns of the table in the current schema.
func loadMeta(ctx context.Context, q querier, table string) (*tableMeta, error) {
	rows, err := q.Query(ctx, `
		SELECT column_name, udt_name, is_generated = 'ALWAYS' OR COALESCE(identity_generation, '') = 'ALWAYS'
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	defer rows.Close()

	m := &tableMeta{cols: map[string]column{}, generated: map[string]bool{}}
	for rows.Next() {
		var c column
		var gen bool
		if err := rows.Scan(&c.Name, &c.Type, &gen); err != nil {
			return nil, err
		}
		if gen {
			m.generated[c.Name] = true
			continue
		}
		m.cols[c.Name] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(m.cols) == 0 {
		return nil, fmt.Errorf("table %s does not exist in the target database", table)
	}
	return m, nil
}

// encodeValue turns a decoded JSON value into the text parameter sent to
// Postgres (cast to the column type in SQL), or nil for SQL NULL. json/jsonb
// columns receive the value's JSON encoding, so an object stays an object and
// a JSON string stays a JSON string.
func encodeValue(c column, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if c.Type == "json" || c.Type == "jsonb" {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("column %s: %w", c.Name, err)
		}
		return string(b), nil
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	}
	return nil, invalidf("column %s (%s): unsupported %T value", c.Name, c.Type, v)
}

// rowID validates and canonicalizes the row's id.
func rowID(row Row) (string, error) {
	raw, ok := row["id"].(string)
	if !ok {
		return "", invalidf("missing or non-string id")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", invalidf("id is not a valid uuid")
	}
	row["id"] = id.String()
	return id.String(), nil
}

// insertRow inserts the row with its original id. Only keys that match a live
// non-generated column are written; the others are returned as dropped. The
// insert is ON CONFLICT (id) DO NOTHING, so an existing id is left untouched
// (inserted=false) and no UPDATE path exists.
func insertRow(ctx context.Context, q querier, table string, meta *tableMeta, row Row) (inserted bool, dropped []string, err error) {
	var names []string
	for k := range row {
		if _, ok := meta.cols[k]; ok {
			names = append(names, k)
		} else {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(names)

	idents := make([]string, len(names))
	places := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		c := meta.cols[n]
		idents[i] = pgx.Identifier{n}.Sanitize()
		places[i] = fmt.Sprintf("$%d::text::%s", i+1, pgx.Identifier{c.Type}.Sanitize())
		if args[i], err = encodeValue(c, row[n]); err != nil {
			return false, dropped, err
		}
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO NOTHING",
		pgx.Identifier{table}.Sanitize(), strings.Join(idents, ", "), strings.Join(places, ", "))
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return false, dropped, err
	}
	return tag.RowsAffected() == 1, dropped, nil
}
