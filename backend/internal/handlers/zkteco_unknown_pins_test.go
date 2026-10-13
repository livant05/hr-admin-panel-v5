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

// seedUnknownPin inserts a unique pin punched on the device with the given
// serial. The table has no company_id: a pin belongs to a tenant only through
// its device, so each test seeds the device it wants the pin to belong to.
func seedUnknownPin(t *testing.T, pool *pgxpool.Pool, serial string, attempts int, resolved bool, lastSeen time.Time) unknownPinRow {
	t.Helper()
	return seedUnknownPinValue(t, pool, "PIN-"+uuid.NewString(), serial, attempts, resolved, lastSeen)
}

func seedUnknownPinValue(t *testing.T, pool *pgxpool.Pool, pin, serial string, attempts int, resolved bool, lastSeen time.Time) unknownPinRow {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO zkteco_unknown_pins (pin, serial_number, attempts, resolved, last_seen)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		pin, serial, attempts, resolved, lastSeen,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed unknown pin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_unknown_pins WHERE id = $1`, id)
	})
	return unknownPinRow{ID: id, Pin: pin, SerialNumber: &serial, Attempts: attempts, Resolved: resolved}
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

// claimedSerial seeds a device claimed by the tenant and returns its serial.
func claimedSerial(t *testing.T, pool *pgxpool.Pool, tn testutil.Tenant) string {
	t.Helper()
	return seedZkDevice(t, pool, zkSeed{companyID: &tn.CompanyID}).SerialNumber
}

// unclaimedSerial seeds a device that belongs to no company.
func unclaimedSerial(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return seedZkDevice(t, pool, zkSeed{}).SerialNumber
}

func TestZktecoUnknownPins_AdminOnly(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPinsA")
	sn := claimedSerial(t, pool, a)
	pin := seedUnknownPin(t, pool, sn, 3, false, time.Now())

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
		if found.Pin != pin.Pin || found.Attempts != 3 || found.Resolved || strVal(found.SerialNumber) != sn {
			t.Fatalf("unexpected row %+v", *found)
		}
	})
}

// CO-S5a-8: the table has no company_id, so tenancy comes from the device that
// punched the pin (serial_number -> zkteco_devices.company_id).
func TestZktecoUnknownPins_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPinsLeakA")
	b := testutil.Company(t, pool, "ZkPinsLeakB")
	snA, snB, snFree := claimedSerial(t, pool, a), claimedSerial(t, pool, b), unclaimedSerial(t, pool)
	now := time.Now()
	pinA := seedUnknownPin(t, pool, snA, 1, false, now)
	pinB := seedUnknownPin(t, pool, snB, 1, false, now)
	pinFree := seedUnknownPin(t, pool, snFree, 1, false, now)

	list := func(t *testing.T, tok string) []unknownPinRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+"?_limit=1000", tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[unknownPinRow](t, rec)
	}

	t.Run("an admin sees only the pins of its own devices", func(t *testing.T) {
		rows := list(t, a.Token(t, signer))
		if got := pinIDsAmong(rows, pinA); len(got) != 1 {
			t.Fatal("admin of A must see the pin punched on A's device")
		}
		if got := pinIDsAmong(rows, pinB, pinFree); len(got) != 0 {
			t.Fatalf("admin of A sees pins of B's or unclaimed devices: %v", got)
		}
		rows = list(t, b.Token(t, signer))
		if got := pinIDsAmong(rows, pinB); len(got) != 1 {
			t.Fatal("admin of B must see the pin punched on B's device")
		}
		if got := pinIDsAmong(rows, pinA, pinFree); len(got) != 0 {
			t.Fatalf("admin of B sees pins of A's or unclaimed devices: %v", got)
		}
	})

	t.Run("no row of another tenant leaks through the serial", func(t *testing.T) {
		for _, r := range list(t, a.Token(t, signer)) {
			if r.SerialNumber != nil && (*r.SerialNumber == snB || *r.SerialNumber == snFree) {
				t.Fatalf("A received a pin of serial %s", *r.SerialNumber)
			}
		}
	})

	t.Run("the same pin on devices of A and B is two rows, one per tenant", func(t *testing.T) {
		shared := "SHARED-" + uuid.NewString()[:8]
		sharedA := seedUnknownPinValue(t, pool, shared, snA, 2, false, now)
		sharedB := seedUnknownPinValue(t, pool, shared, snB, 5, false, now)
		var gotA, gotB []unknownPinRow
		for _, r := range list(t, a.Token(t, signer)) {
			if r.Pin == shared {
				gotA = append(gotA, r)
			}
		}
		for _, r := range list(t, b.Token(t, signer)) {
			if r.Pin == shared {
				gotB = append(gotB, r)
			}
		}
		if len(gotA) != 1 || gotA[0].ID != sharedA.ID || gotA[0].Attempts != 2 {
			t.Fatalf("A rows for the shared pin = %+v, want only its own (attempts 2)", gotA)
		}
		if len(gotB) != 1 || gotB[0].ID != sharedB.ID || gotB[0].Attempts != 5 {
			t.Fatalf("B rows for the shared pin = %+v, want only its own (attempts 5)", gotB)
		}
	})

	t.Run("a device that gets claimed later exposes its pins to the new owner only", func(t *testing.T) {
		if _, err := pool.Exec(context.Background(),
			`UPDATE zkteco_devices SET company_id = $1 WHERE serial_number = $2`, a.CompanyID, snFree); err != nil {
			t.Fatalf("claim device: %v", err)
		}
		if got := pinIDsAmong(list(t, a.Token(t, signer)), pinFree); len(got) != 1 {
			t.Fatal("after the claim A must see the pin of its new device")
		}
		if got := pinIDsAmong(list(t, b.Token(t, signer)), pinFree); len(got) != 0 {
			t.Fatal("B must still not see it")
		}
	})
}

func TestZktecoUnknownPins_ListRules(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPinsRules")
	ta := a.Token(t, signer)
	// The list is scoped to the tenant's own devices, so the three seeded pins
	// are the only rows these assertions can see.
	sn := claimedSerial(t, pool, a)
	base := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	oldest := seedUnknownPin(t, pool, sn, 1, false, base.Add(1*time.Hour))
	middle := seedUnknownPin(t, pool, sn, 7, true, base.Add(2*time.Hour))
	newest := seedUnknownPin(t, pool, sn, 2, false, base.Add(3*time.Hour))

	list := func(t *testing.T, query string) []unknownPinRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+query, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[unknownPinRow](t, rec)
	}

	ids := func(rows []unknownPinRow) string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return strings.Join(out, ",")
	}

	t.Run("newest last_seen first", func(t *testing.T) {
		got := ids(list(t, "?_limit=3"))
		want := newest.ID + "," + middle.ID + "," + oldest.ID
		if got != want {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})

	t.Run("resolved filter", func(t *testing.T) {
		if open := ids(list(t, "?resolved=false&_limit=2")); open != newest.ID+","+oldest.ID {
			t.Fatalf("resolved=false = %v, want newest then oldest", open)
		}
		done := list(t, "?resolved=true&_limit=1")
		if got := ids(done); got != middle.ID {
			t.Fatalf("resolved=true = %v, want only the resolved pin", got)
		}
		if !done[0].Resolved {
			t.Fatalf("resolved=true returned an unresolved row: %+v", done[0])
		}
	})

	t.Run("limit and offset page the list", func(t *testing.T) {
		cases := []struct{ query, want string }{
			{"?_limit=1", newest.ID},
			{"?_limit=1&_offset=1", middle.ID},
			{"?_limit=1&_offset=2", oldest.ID},
			{"?_limit=2&_offset=1", middle.ID + "," + oldest.ID},
			{"?resolved=false&_limit=1&_offset=1", oldest.ID},
		}
		for _, c := range cases {
			if got := ids(list(t, c.query)); got != c.want {
				t.Fatalf("%s = %v, want %v", c.query, got, c.want)
			}
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
