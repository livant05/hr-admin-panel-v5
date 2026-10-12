package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type unknownPinRow struct {
	ID           string  `json:"id"`
	Pin          string  `json:"pin"`
	SerialNumber *string `json:"serial_number"`
	Attempts     int     `json:"attempts"`
	Resolved     bool    `json:"resolved"`
	LastSeen     *string `json:"last_seen"`
	CreatedAt    string  `json:"created_at"`
}

const zkUnknownPinsPath = "/api/zkteco_unknown_pins"

// seedUnknownPin inserts a pin with a unique value. The table has no
// company_id and a UNIQUE pin, so each test owns uniquely named rows and
// removes them on cleanup.
func seedUnknownPin(t *testing.T, pool *pgxpool.Pool, attempts int, resolved bool, lastSeen time.Time) unknownPinRow {
	t.Helper()
	pin := "PIN-" + uuid.NewString()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO zkteco_unknown_pins (pin, serial_number, attempts, resolved, last_seen)
		 VALUES ($1, 'SN-UNKNOWN', $2, $3, $4) RETURNING id`,
		pin, attempts, resolved, lastSeen,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed unknown pin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_unknown_pins WHERE id = $1`, id)
	})
	return unknownPinRow{ID: id, Pin: pin, Attempts: attempts, Resolved: resolved}
}

func pinIDsAmong(rows []unknownPinRow, wanted ...unknownPinRow) []string {
	own := map[string]bool{}
	for _, w := range wanted {
		own[w.ID] = true
	}
	var ids []string
	for _, r := range rows {
		if own[r.ID] {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// The table has no company_id (the sanctioned A1 exception), so these tests
// assert the admin gate, not tenant isolation.
func TestZktecoUnknownPins_AdminOnly(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPinsA")
	pin := seedUnknownPin(t, pool, 3, false, time.Now())

	t.Run("non-admin roles get 403 and no data", func(t *testing.T) {
		for _, role := range []string{"empleado", "manager", "rrhh"} {
			rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath, a.TokenAs(t, signer, role), nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: got %d (%s), want 403", role, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "forbidden" {
				t.Fatalf("%s: error.code = %q, want forbidden", role, code)
			}
			if strings.Contains(rec.Body.String(), pin.Pin) {
				t.Fatalf("%s: 403 body discloses a pin", role)
			}
		}
	})

	t.Run("the gate runs before query validation", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+"?bogus=1", a.TokenAs(t, signer, "empleado"), nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d, want 403 before any validation", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		if rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
	})

	t.Run("admin lists pins with their attempts counter", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath, a.Token(t, signer), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		var found *unknownPinRow
		rows := testutil.DecodeRows[unknownPinRow](t, rec)
		for i := range rows {
			if rows[i].ID == pin.ID {
				found = &rows[i]
			}
		}
		if found == nil {
			t.Fatalf("admin list must contain the seeded pin %s", pin.Pin)
		}
		if found.Pin != pin.Pin || found.Attempts != 3 || found.Resolved || strVal(found.SerialNumber) != "SN-UNKNOWN" {
			t.Fatalf("unexpected row %+v", *found)
		}
	})

	t.Run("a second company's admin sees the same global list", func(t *testing.T) {
		b := testutil.Company(t, pool, "ZkPinsB")
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+"?resolved=false", b.Token(t, signer), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := pinIDsAmong(testutil.DecodeRows[unknownPinRow](t, rec), pin); len(got) != 1 {
			t.Fatal("the pins table is global by design: any admin must see it")
		}
	})
}

func TestZktecoUnknownPins_ListRules(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPinsRules")
	ta := a.Token(t, signer)
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldest := seedUnknownPin(t, pool, 1, false, now.Add(-3*time.Hour))
	middle := seedUnknownPin(t, pool, 7, true, now.Add(-2*time.Hour))
	newest := seedUnknownPin(t, pool, 2, false, now.Add(-time.Hour))

	list := func(t *testing.T, query string) []unknownPinRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+query, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[unknownPinRow](t, rec)
	}

	t.Run("newest last_seen first", func(t *testing.T) {
		got := pinIDsAmong(list(t, "?_limit=1000"), oldest, middle, newest)
		want := []string{newest.ID, middle.ID, oldest.ID}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})

	t.Run("resolved filter", func(t *testing.T) {
		open := pinIDsAmong(list(t, "?resolved=false&_limit=1000"), oldest, middle, newest)
		if strings.Join(open, ",") != newest.ID+","+oldest.ID {
			t.Fatalf("resolved=false = %v, want newest then oldest", open)
		}
		done := list(t, "?resolved=true&_limit=1000")
		if got := pinIDsAmong(done, oldest, middle, newest); len(got) != 1 || got[0] != middle.ID {
			t.Fatalf("resolved=true = %v, want only the resolved pin", got)
		}
		for _, r := range done {
			if !r.Resolved {
				t.Fatalf("resolved=true returned an unresolved row: %+v", r)
			}
		}
	})

	t.Run("limit and offset page the list", func(t *testing.T) {
		if rows := list(t, "?_limit=2"); len(rows) != 2 {
			t.Fatalf("_limit=2 returned %d rows", len(rows))
		}
	})

	t.Run("bad parameters are validation_failed", func(t *testing.T) {
		for _, q := range []string{"?resolved=", "?resolved=maybe", "?resolved=1", "?bogus=1", "?_order=last_seen.asc", "?_limit=0", "?company_id=" + a.CompanyID} {
			rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+q, ta, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", q, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("%s: error.code = %q, want validation_failed", q, code)
			}
		}
	})

	t.Run("the endpoint is read-only", func(t *testing.T) {
		for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
			rec := testutil.Do(t, h, m, zkUnknownPinsPath, ta, map[string]any{"resolved": true})
			if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
				t.Fatalf("%s: got %d, want 404 or 405", m, rec.Code)
			}
		}
	})
}
