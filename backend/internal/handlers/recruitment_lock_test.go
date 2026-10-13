package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// lockedResource describes a recruitment resource whose PATCH merge read takes
// FOR NO KEY UPDATE and whose DELETE takes FOR UPDATE (CO-S2d-1).
type lockedResource struct {
	name   string
	table  string
	path   string
	create string // POST body of a fresh row
	column string // column the hold trigger watches
	marker string
	patch  string // PATCH body that sets column to marker
	other  string // column the PATCH does not send
}

var lockedResources = []lockedResource{
	{
		name: "job_postings", table: "job_postings", path: jobPostingsPath,
		create: `{"title":"Serial"}`, column: "title", marker: "hold-this-patch",
		patch: `{"title":"hold-this-patch"}`, other: "department",
	},
	{
		name: "candidates", table: "candidates", path: candidatesPath,
		create: `{"full_name":"Serial"}`, column: "full_name", marker: "hold-this-patch",
		patch: `{"full_name":"hold-this-patch"}`, other: "phone",
	},
}

func serveAsync(h http.Handler, wg *sync.WaitGroup, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec, req)
	}()
	return rec
}

func authedRequest(method, path, tok, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

// TestRecruitment_PatchAndDeleteSerialize proves the claim in the README and in
// GetCandidateForMerge / GetJobPostingForMerge: FOR NO KEY UPDATE (PATCH) and
// FOR UPDATE (DELETE) conflict, so on the same row the two requests run one
// after the other in either order, instead of the PATCH writing to a row the
// DELETE is removing. Every wait is on pg_blocking_pids, never a sleep.
//
// The first two subtests pin the ordering end to end. The third pins what the
// merge read's own lock buys: a concurrent committed write to a column the PATCH
// does not send is read, not overwritten with a stale copy (lost update).
func TestRecruitment_PatchAndDeleteSerialize(t *testing.T) {
	for _, lr := range lockedResources {
		t.Run(lr.name+"/delete waits for a patch in flight", func(t *testing.T) {
			// Isolated schema: the hold trigger must never fire for another test.
			pool, _ := testutil.IsolatedSchema(t)
			h, signer := testutil.Server(t, pool)
			c := testutil.Company(t, pool, "Serial1"+lr.name)
			tok := c.Token(t, signer)
			id := createRecruitmentRow(t, h, tok, lr)

			holderPID, release := parkPatchesInTrigger(t, pool, lr.table, lr.column, lr.marker)
			var wg sync.WaitGroup
			defer func() {
				release()
				wg.Wait()
			}()

			patchRec := serveAsync(h, &wg, authedRequest(http.MethodPatch, lr.path+"/"+id, tok, lr.patch))
			patchPID := waitUntilBlockedBy(t, pool, holderPID) // PATCH: merge read done, parked in the trigger

			delRec := serveAsync(h, &wg, authedRequest(http.MethodDelete, lr.path+"/"+id, tok, ""))
			waitUntilBlockedBy(t, pool, patchPID) // DELETE: queued behind the PATCH's row lock

			release()
			wg.Wait()
			if patchRec.Code != http.StatusOK {
				t.Fatalf("PATCH got %d (%s), want 200", patchRec.Code, patchRec.Body.String())
			}
			if delRec.Code != http.StatusNoContent {
				t.Fatalf("DELETE got %d (%s), want 204 once the PATCH finished", delRec.Code, delRec.Body.String())
			}
			if n := countRowsByID(t, pool, lr.table, id); n != 0 {
				t.Fatalf("%d row(s) left, want the DELETE to have removed it", n)
			}
		})

		t.Run(lr.name+"/patch waits for a delete in flight", func(t *testing.T) {
			pool := testutil.Pool(t)
			h, signer := testutil.Server(t, pool)
			c := testutil.Company(t, pool, "Serial2"+lr.name)
			tok := c.Token(t, signer)
			id := createRecruitmentRow(t, h, tok, lr)

			// A DELETE in flight is the FOR UPDATE row lock of deleteGuarded held
			// across the request; hold the same lock on a connection of our own.
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE id = $1 FOR UPDATE`, lr.table), id); err != nil {
				t.Fatalf("lock row: %v", err)
			}
			var holderPID int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				t.Fatalf("holder pid: %v", err)
			}

			var wg sync.WaitGroup
			defer func() {
				_ = tx.Rollback(ctx)
				wg.Wait()
			}()
			patchRec := serveAsync(h, &wg, authedRequest(http.MethodPatch, lr.path+"/"+id, tok, lr.patch))
			waitUntilBlockedBy(t, pool, holderPID) // PATCH: parked on the merge read

			if err := tx.Rollback(ctx); err != nil {
				t.Fatalf("release row lock: %v", err)
			}
			wg.Wait()
			if patchRec.Code != http.StatusOK {
				t.Fatalf("PATCH got %d (%s), want 200 after the lock holder let go", patchRec.Code, patchRec.Body.String())
			}
			if n := countRowsByID(t, pool, lr.table, id); n != 1 {
				t.Fatalf("%d row(s), want the patched row to exist", n)
			}
		})

		t.Run(lr.name+"/patch merge read does not lose a concurrent write", func(t *testing.T) {
			pool := testutil.Pool(t)
			h, signer := testutil.Server(t, pool)
			c := testutil.Company(t, pool, "Serial3"+lr.name)
			tok := c.Token(t, signer)
			id := createRecruitmentRow(t, h, tok, lr)

			// A concurrent writer updates a column the PATCH body does not carry and
			// stays uncommitted. The PATCH's merge read must wait for it: a plain
			// read would snapshot the old value and write it back over this one.
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = 'concurrent' WHERE id = $1`, lr.table, lr.other), id); err != nil {
				t.Fatalf("concurrent write: %v", err)
			}
			var holderPID int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				t.Fatalf("holder pid: %v", err)
			}

			var wg sync.WaitGroup
			defer func() {
				_ = tx.Rollback(ctx)
				wg.Wait()
			}()
			patchRec := serveAsync(h, &wg, authedRequest(http.MethodPatch, lr.path+"/"+id, tok, lr.patch))
			waitUntilBlockedBy(t, pool, holderPID)

			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit concurrent write: %v", err)
			}
			wg.Wait()
			if patchRec.Code != http.StatusOK {
				t.Fatalf("PATCH got %d (%s), want 200", patchRec.Code, patchRec.Body.String())
			}
			var got string
			if err := pool.QueryRow(ctx,
				fmt.Sprintf(`SELECT coalesce(%s, '<null>') FROM %s WHERE id = $1`, lr.other, lr.table), id).Scan(&got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got != "concurrent" {
				t.Fatalf("%s = %q after the PATCH, want the concurrent write %q to survive", lr.other, got, "concurrent")
			}
		})
	}
}

func createRecruitmentRow(t *testing.T, h http.Handler, tok string, lr lockedResource) string {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, lr.path, tok, json.RawMessage(lr.create))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s: got %d (%s)", lr.name, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[struct {
		ID string `json:"id"`
	}](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create %s returned %d rows", lr.name, len(rows))
	}
	return rows[0].ID
}

func countRowsByID(t *testing.T, pool *pgxpool.Pool, table, id string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE id = $1`, table), id).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
