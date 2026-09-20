package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type positionRow struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
}

// TestPositions_CrossTenantLeak mirrors TestDepartments_CrossTenantLeak
// (design P5.3) with the table name and payload field changed.
func TestPositions_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "PosLeakA")
	b := testutil.Company(t, pool, "PosLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/positions", ta, map[string]string{"name": "Vendedor"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[positionRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	posA := rows[0]

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+posA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/positions/"+posA.ID, tb, map[string]string{"name": "Hacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+posA.ID, ta, nil)
		var got positionRow
		if err := decodeJSON(getRec, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Name != "Vendedor" {
			t.Fatalf("expected name to remain Vendedor, got %q", got.Name)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/positions/"+posA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+posA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected position to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tb, map[string]string{"name": "Cajero"})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[positionRow](t, testutil.Do(t, h, http.MethodGet, "/api/positions", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != posA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.Name == "Vendedor" {
				found = true
			}
			if r.Name == "Cajero" {
				t.Fatalf("company A's list leaked company B's position")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Vendedor")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/positions", ta, map[string]string{
			"name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/positions", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "PosEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/positions", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[positionRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestPositions_DeleteRestrictedWhileReferenced pins design P6.1: DELETE
// must return 409 while an employee's TEXT `position` column still matches
// the position's name, and must leave both rows unchanged.
func TestPositions_DeleteRestrictedWhileReferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PosRestrictDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": "Vendedor"})
	pos := testutil.DecodeRows[positionRow](t, createRec)[0]

	seedEmployeePosition(t, pool, c.CompanyID, "Ana", "Diaz", "Vendedor")

	rec := testutil.Do(t, h, http.MethodDelete, "/api/positions/"+pos.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 while an employee references the position, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+pos.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the position to survive the blocked delete, got %d", getRec.Code)
	}
}

// TestPositions_DeleteSucceedsWhenUnreferenced pins the complementary
// scenario: no employee references the position, so DELETE succeeds.
func TestPositions_DeleteSucceedsWhenUnreferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PosDeleteOkCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": "Analista"})
	pos := testutil.DecodeRows[positionRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodDelete, "/api/positions/"+pos.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+pos.ID, tok, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected the position to be gone, got %d", getRec.Code)
	}
}

// TestPositions_RenamePropagatesToEmployees pins design P6.1's other half: a
// rename must update every employee row's `position` TEXT column that
// matched the old name, atomically, and leave unrelated employees alone.
func TestPositions_RenamePropagatesToEmployees(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PosRenamePropagateCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": "Vendedor"})
	pos := testutil.DecodeRows[positionRow](t, createRec)[0]

	e1 := seedEmployeePosition(t, pool, c.CompanyID, "Ana", "Diaz", "Vendedor")
	e2 := seedEmployeePosition(t, pool, c.CompanyID, "Luis", "Gomez", "Vendedor")
	e3 := seedEmployeePosition(t, pool, c.CompanyID, "Rosa", "Perez", "Cajero")

	rec := testutil.Do(t, h, http.MethodPatch, "/api/positions/"+pos.ID, tok, map[string]string{"name": "Asesor Comercial"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	renamed := testutil.DecodeRows[positionRow](t, rec)[0]
	if renamed.Name != "Asesor Comercial" {
		t.Fatalf("expected renamed position name Asesor Comercial, got %q", renamed.Name)
	}

	if got := employeePosition(t, pool, e1); got != "Asesor Comercial" {
		t.Fatalf("expected e1.position = Asesor Comercial, got %q", got)
	}
	if got := employeePosition(t, pool, e2); got != "Asesor Comercial" {
		t.Fatalf("expected e2.position = Asesor Comercial, got %q", got)
	}
	if got := employeePosition(t, pool, e3); got != "Cajero" {
		t.Fatalf("expected e3.position to remain Cajero, got %q", got)
	}
}

// TestPositions_RejectsEmptyName pins the spec's "Reject empty name"
// scenario: POST and PATCH with an empty or whitespace-only name must
// return 400 validation_failed with a fields.name detail, and must not
// create or mutate any row.
func TestPositions_RejectsEmptyName(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PosRejectEmptyNameCo")
	tok := c.Token(t, signer)

	t.Run("create with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("create with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	rows := testutil.DecodeRows[positionRow](t, testutil.Do(t, h, http.MethodGet, "/api/positions", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no position to be created by a rejected empty-name request, got %d", len(rows))
	}

	createRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tok, map[string]string{"name": "Vendedor"})
	pos := testutil.DecodeRows[positionRow](t, createRec)[0]

	t.Run("update with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/positions/"+pos.ID, tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("update with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/positions/"+pos.ID, tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	getRec := testutil.Do(t, h, http.MethodGet, "/api/positions/"+pos.ID, tok, nil)
	var got positionRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Vendedor" {
		t.Fatalf("expected position name to remain Vendedor after rejected renames, got %q", got.Name)
	}
}

// TestPositions_DuplicateNamePerTenant closes WARNING-3 from the phase1a
// verify report: a duplicate name within one tenant must 409 (the
// (company_id, lower(name)) unique index, design P6.1), while the very same
// name in a different tenant must succeed with 201 — proving the uniqueness
// constraint is scoped per-tenant, not global.
func TestPositions_DuplicateNamePerTenant(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "PosDupA")
	b := testutil.Company(t, pool, "PosDupB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	firstRec := testutil.Do(t, h, http.MethodPost, "/api/positions", ta, map[string]string{"name": "Supervisor"})
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the first create, got %d (body=%s)", firstRec.Code, firstRec.Body.String())
	}

	dupRec := testutil.Do(t, h, http.MethodPost, "/api/positions", ta, map[string]string{"name": "Supervisor"})
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate name in the same tenant, got %d (body=%s)", dupRec.Code, dupRec.Body.String())
	}
	if code := testutil.ErrCode(t, dupRec); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}

	otherTenantRec := testutil.Do(t, h, http.MethodPost, "/api/positions", tb, map[string]string{"name": "Supervisor"})
	if otherTenantRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for the same name in a different tenant, got %d (body=%s)", otherTenantRec.Code, otherTenantRec.Body.String())
	}
}

// seedEmployeePosition inserts a minimal employee row directly (bypassing
// the HTTP layer) so RESTRICT/rename tests can assert against
// employees.position, a plain TEXT column with no FK to position.
func seedEmployeePosition(t *testing.T, pool *pgxpool.Pool, companyID, firstName, lastName, position string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO employees (id, company_id, first_name, last_name, position)
		 VALUES ($1, $2, $3, $4, $5)`,
		id, companyID, firstName, lastName, position,
	)
	if err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	return id
}

// employeePosition reads back an employee's position column for
// rename-propagation assertions.
func employeePosition(t *testing.T, pool *pgxpool.Pool, employeeID string) string {
	t.Helper()
	var pos *string
	err := pool.QueryRow(context.Background(),
		`SELECT position FROM employees WHERE id = $1`, employeeID,
	).Scan(&pos)
	if err != nil {
		t.Fatalf("read employee position: %v", err)
	}
	if pos == nil {
		return ""
	}
	return *pos
}
