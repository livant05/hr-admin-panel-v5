package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
