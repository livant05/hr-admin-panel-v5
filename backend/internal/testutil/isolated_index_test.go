package testutil_test

import (
	"context"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

func indexIn(t *testing.T, schema string) bool {
	t.Helper()
	admin := testutil.Pool(t)
	var exists bool
	err := admin.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)`,
		schema, testutil.UsersEmailLowerIndex).Scan(&exists)
	if err != nil {
		t.Fatalf("check index in %s: %v", schema, err)
	}
	return exists
}

// TestDropIndexInIsolatedSchema_NeverTouchesPublic pins the 0010 test hazard:
// an unqualified DROP INDEX resolves through search_path, so the drop must be
// schema-qualified and public's index must survive it.
func TestDropIndexInIsolatedSchema_NeverTouchesPublic(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()

	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if schema == "public" {
		t.Fatal("isolated pool must not be on public")
	}
	if !indexIn(t, "public") || !indexIn(t, schema) {
		t.Fatalf("precondition: index must exist in public and %s", schema)
	}

	testutil.DropIndexInIsolatedSchema(t, pool, testutil.UsersEmailLowerIndex)

	if indexIn(t, schema) {
		t.Fatalf("index must be gone from %s", schema)
	}
	if !indexIn(t, "public") {
		t.Fatal("public's index must be untouched")
	}
}
