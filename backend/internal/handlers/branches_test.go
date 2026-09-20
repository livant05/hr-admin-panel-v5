package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type branchRow struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
}

// TestBranches_CrossTenantLeak mirrors TestDepartments_CrossTenantLeak
// (design P5.3) with the table name and payload field changed.
func TestBranches_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "BranchLeakA")
	b := testutil.Company(t, pool, "BranchLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/branches", ta, map[string]string{"name": "Panama Centro"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[branchRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	branchA := rows[0]

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branchA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/branches/"+branchA.ID, tb, map[string]string{"name": "Hacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branchA.ID, ta, nil)
		var got branchRow
		if err := decodeJSON(getRec, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Name != "Panama Centro" {
			t.Fatalf("expected name to remain Panama Centro, got %q", got.Name)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/branches/"+branchA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branchA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected branch to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/branches", tb, map[string]string{"name": "David"})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[branchRow](t, testutil.Do(t, h, http.MethodGet, "/api/branches", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != branchA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.Name == "Panama Centro" {
				found = true
			}
			if r.Name == "David" {
				t.Fatalf("company A's list leaked company B's branch")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Panama Centro")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/branches", ta, map[string]string{
			"name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/branches", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "BranchEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/branches", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[branchRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestBranches_DeleteRestrictedWhileReferenced pins design P6.1: DELETE
// must return 409 while an employee's TEXT `branch` column still matches
// the branch's name, and must leave both rows unchanged.
func TestBranches_DeleteRestrictedWhileReferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "BranchRestrictDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": "Panama Centro"})
	branch := testutil.DecodeRows[branchRow](t, createRec)[0]

	seedEmployeeBranch(t, pool, c.CompanyID, "Ana", "Diaz", "Panama Centro")

	rec := testutil.Do(t, h, http.MethodDelete, "/api/branches/"+branch.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 while an employee references the branch, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branch.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the branch to survive the blocked delete, got %d", getRec.Code)
	}
}

// TestBranches_DeleteSucceedsWhenUnreferenced pins the complementary
// scenario: no employee references the branch, so DELETE succeeds.
func TestBranches_DeleteSucceedsWhenUnreferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "BranchDeleteOkCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": "Colon"})
	branch := testutil.DecodeRows[branchRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodDelete, "/api/branches/"+branch.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branch.ID, tok, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected the branch to be gone, got %d", getRec.Code)
	}
}

// TestBranches_RenamePropagatesToEmployees pins design P6.1's other half: a
// rename must update every employee row's `branch` TEXT column that matched
// the old name, atomically, and leave unrelated employees alone.
func TestBranches_RenamePropagatesToEmployees(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "BranchRenamePropagateCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": "Panama Centro"})
	branch := testutil.DecodeRows[branchRow](t, createRec)[0]

	e1 := seedEmployeeBranch(t, pool, c.CompanyID, "Ana", "Diaz", "Panama Centro")
	e2 := seedEmployeeBranch(t, pool, c.CompanyID, "Luis", "Gomez", "Panama Centro")
	e3 := seedEmployeeBranch(t, pool, c.CompanyID, "Rosa", "Perez", "David")

	rec := testutil.Do(t, h, http.MethodPatch, "/api/branches/"+branch.ID, tok, map[string]string{"name": "Panama Este"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	renamed := testutil.DecodeRows[branchRow](t, rec)[0]
	if renamed.Name != "Panama Este" {
		t.Fatalf("expected renamed branch name Panama Este, got %q", renamed.Name)
	}

	if got := employeeBranch(t, pool, e1); got != "Panama Este" {
		t.Fatalf("expected e1.branch = Panama Este, got %q", got)
	}
	if got := employeeBranch(t, pool, e2); got != "Panama Este" {
		t.Fatalf("expected e2.branch = Panama Este, got %q", got)
	}
	if got := employeeBranch(t, pool, e3); got != "David" {
		t.Fatalf("expected e3.branch to remain David, got %q", got)
	}
}

// TestBranches_RejectsEmptyName pins the spec's "Reject empty name"
// scenario: POST and PATCH with an empty or whitespace-only name must
// return 400 validation_failed with a fields.name detail, and must not
// create or mutate any row.
func TestBranches_RejectsEmptyName(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "BranchRejectEmptyNameCo")
	tok := c.Token(t, signer)

	t.Run("create with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("create with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	rows := testutil.DecodeRows[branchRow](t, testutil.Do(t, h, http.MethodGet, "/api/branches", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no branch to be created by a rejected empty-name request, got %d", len(rows))
	}

	createRec := testutil.Do(t, h, http.MethodPost, "/api/branches", tok, map[string]string{"name": "Panama Centro"})
	branch := testutil.DecodeRows[branchRow](t, createRec)[0]

	t.Run("update with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/branches/"+branch.ID, tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("update with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/branches/"+branch.ID, tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	getRec := testutil.Do(t, h, http.MethodGet, "/api/branches/"+branch.ID, tok, nil)
	var got branchRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Panama Centro" {
		t.Fatalf("expected branch name to remain Panama Centro after rejected renames, got %q", got.Name)
	}
}

// seedEmployeeBranch inserts a minimal employee row directly (bypassing the
// HTTP layer) so RESTRICT/rename tests can assert against employees.branch,
// a plain TEXT column with no FK to branch.
func seedEmployeeBranch(t *testing.T, pool *pgxpool.Pool, companyID, firstName, lastName, branch string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO employees (id, company_id, first_name, last_name, branch)
		 VALUES ($1, $2, $3, $4, $5)`,
		id, companyID, firstName, lastName, branch,
	)
	if err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	return id
}

// employeeBranch reads back an employee's branch column for
// rename-propagation assertions.
func employeeBranch(t *testing.T, pool *pgxpool.Pool, employeeID string) string {
	t.Helper()
	var branch *string
	err := pool.QueryRow(context.Background(),
		`SELECT branch FROM employees WHERE id = $1`, employeeID,
	).Scan(&branch)
	if err != nil {
		t.Fatalf("read employee branch: %v", err)
	}
	if branch == nil {
		return ""
	}
	return *branch
}
