package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These checks guard the sqlc output against two documentation leaks that
// survived the S5b review: a file header comment embedded in the first
// query's SQL text (sqlc attaches ANY comment before a "-- name:" line to
// that query), and a doc comment that only makes sense in its source file.

// sqlConst matches the raw string constant sqlc emits for each query.
var sqlConst = regexp.MustCompile("(?s)const (\\w+) = `(.*?)`")

func generatedFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("generated/*.sql.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated sqlc files found (err=%v)", err)
	}
	return files
}

func TestGeneratedSQL_CarriesNoBlockComments(t *testing.T) {
	for _, f := range generatedFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range sqlConst.FindAllStringSubmatch(string(src), -1) {
			if strings.Contains(m[2], "/*") {
				t.Errorf("%s: query %s embeds a block comment in its SQL text; move file headers into the first query's -- doc lines", f, m[1])
			}
		}
	}
}

func TestQueryDocComments_StandAlone(t *testing.T) {
	files, err := filepath.Glob("queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found (err=%v)", err)
	}
	relative := regexp.MustCompile(`(?im)^--\s*(same as (the )?(above|previous|last)|as above|see above|ditto)\b`)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := relative.FindIndex(src); loc != nil {
			line := strings.Count(string(src[:loc[0]]), "\n") + 1
			t.Errorf("%s:%d: doc comment refers to \"above\"; generated docs lose that context, so state the rationale in full", f, line)
		}
	}
}
