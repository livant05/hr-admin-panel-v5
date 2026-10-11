package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// readTable reads <dir>/<table>.json: a JSON array of row objects as
// PostgREST returns it. A missing file reports exists=false.
func readTable(dir, table string) (rows []Row, exists bool, err error) {
	path := filepath.Join(dir, table+".json")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, true, fmt.Errorf("%s must be a JSON array of objects: %w", path, err)
	}
	// Exactly one JSON value: concatenated page arrays would otherwise import
	// only the first page and still exit 0.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, true, fmt.Errorf("%s has data after the first JSON array (merge pages into one array)", path)
	}
	return rows, true, nil
}

// ignoredFiles lists *.json files in dir that match no known table.
func ignoredFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read export dir: %w", err)
	}
	known := make(map[string]bool, len(tables))
	for _, t := range tables {
		known[t.Name+".json"] = true
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || known[e.Name()] {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// uniqueTracker detects rows that repeat a unique column set within the export.
type uniqueTracker struct {
	sets [][]string
	seen []map[string]string // per set: key -> id of the first row
}

func newUniqueTracker(sets [][]string) *uniqueTracker {
	t := &uniqueTracker{sets: sets}
	for range sets {
		t.seen = append(t.seen, map[string]string{})
	}
	return t
}

// keys returns the complete key of each unique set for row ("" when a column
// is missing: such rows are left to the database to judge).
func (t *uniqueTracker) keys(row Row) []string {
	out := make([]string, len(t.sets))
	for i, cols := range t.sets {
		parts := make([]string, len(cols))
		for j, c := range cols {
			v, ok := row[c]
			if !ok || v == nil {
				parts = nil
				break
			}
			parts[j] = fmt.Sprint(v)
		}
		if parts != nil {
			out[i] = strings.Join(parts, "\x00")
		}
	}
	return out
}

// check returns a rejection reason when row repeats the key of an earlier
// accepted row, or "". It claims nothing: call claim once the row was
// actually inserted, so a rejected row never blocks a later valid one.
func (t *uniqueTracker) check(row Row) string {
	for i, key := range t.keys(row) {
		if key == "" {
			continue
		}
		if first, dup := t.seen[i][key]; dup {
			return fmt.Sprintf("duplicate (%s) in export: same as row %s", strings.Join(t.sets[i], ", "), first)
		}
	}
	return ""
}

// claim records row's unique keys as taken by id.
func (t *uniqueTracker) claim(id string, row Row) {
	for i, key := range t.keys(row) {
		if key != "" {
			t.seen[i][key] = id
		}
	}
}
