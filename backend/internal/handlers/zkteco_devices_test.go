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

type zkDeviceRow struct {
	ID              string  `json:"id"`
	CompanyID       *string `json:"company_id"`
	SerialNumber    string  `json:"serial_number"`
	Name            *string `json:"name"`
	Location        *string `json:"location"`
	Status          *string `json:"status"`
	LastSeen        *string `json:"last_seen"`
	FirmwareVersion *string `json:"firmware_version"`
	CreatedAt       string  `json:"created_at"`
}

const zkDevicesPath = "/api/zkteco_devices"

// zkSeed describes a device row inserted straight into the table. A nil
// companyID is an unclaimed device (company_id NULL).
type zkSeed struct {
	companyID *string
	name      string
	location  string
	lastSeen  *time.Time
}

// seedZkDevice inserts a device with a unique serial and registers its
// cleanup. zkteco_devices.company_id is ON DELETE SET NULL, so deleting a test
// company would otherwise strand its devices in the unclaimed pool that other
// tests (and a real dev database) read.
func seedZkDevice(t *testing.T, pool *pgxpool.Pool, s zkSeed) zkDeviceRow {
	t.Helper()
	serial := "ZKT-" + uuid.NewString()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO zkteco_devices (company_id, serial_number, name, location, status, last_seen, firmware_version)
		 VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), 'online', $5, 'fw-1')
		 RETURNING id`,
		s.companyID, serial, s.name, s.location, s.lastSeen,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed zkteco device: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_devices WHERE id = $1`, id)
	})
	return readZkDevice(t, pool, id)
}

// readZkDevice reads a device straight from the table (never through the API)
// so "unchanged" assertions cannot be fooled by the handler under test.
func readZkDevice(t *testing.T, pool *pgxpool.Pool, id string) zkDeviceRow {
	t.Helper()
	var d zkDeviceRow
	var lastSeen *time.Time
	var created time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT id::text, company_id::text, serial_number, name, location, status, last_seen, firmware_version, created_at
		 FROM zkteco_devices WHERE id = $1`, id,
	).Scan(&d.ID, &d.CompanyID, &d.SerialNumber, &d.Name, &d.Location, &d.Status, &lastSeen, &d.FirmwareVersion, &created)
	if err != nil {
		t.Fatalf("read zkteco device %s: %v", id, err)
	}
	if lastSeen != nil {
		s := lastSeen.UTC().Format(time.RFC3339Nano)
		d.LastSeen = &s
	}
	d.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	return d
}

func sameStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sameInstant compares two RFC 3339 timestamps as instants, because the API
// renders the session time zone while readZkDevice normalizes to UTC.
func sameInstant(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	ta, errA := time.Parse(time.RFC3339Nano, *a)
	tb, errB := time.Parse(time.RFC3339Nano, *b)
	return errA == nil && errB == nil && ta.Equal(tb)
}

func sameZkDevice(a, b zkDeviceRow) bool {
	return a.ID == b.ID && sameStrPtr(a.CompanyID, b.CompanyID) && a.SerialNumber == b.SerialNumber &&
		sameStrPtr(a.Name, b.Name) && sameStrPtr(a.Location, b.Location) && sameStrPtr(a.Status, b.Status) &&
		sameInstant(a.LastSeen, b.LastSeen) && sameStrPtr(a.FirmwareVersion, b.FirmwareVersion) && a.CreatedAt == b.CreatedAt
}

func zkIDs(rows []zkDeviceRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestZktecoDevices_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkLeakA")
	b := testutil.Company(t, pool, "ZkLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	devA := seedZkDevice(t, pool, zkSeed{companyID: &a.CompanyID, name: "A door"})
	devB := seedZkDevice(t, pool, zkSeed{companyID: &b.CompanyID, name: "B door", location: "B hall"})
	unclaimed := seedZkDevice(t, pool, zkSeed{name: "pool device"})

	t.Run("list returns only the caller's devices", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: got %d (%s)", rec.Code, rec.Body.String())
		}
		rows := testutil.DecodeRows[zkDeviceRow](t, rec)
		sawOwn := false
		for _, r := range rows {
			if r.CompanyID == nil || *r.CompanyID != a.CompanyID {
				t.Fatalf("company A's list leaked a row it does not own: %+v", r)
			}
			if r.ID == devA.ID {
				sawOwn = true
			}
		}
		if !sawOwn {
			t.Fatalf("company A's list must contain its own device, got %v", zkIDs(rows))
		}
		if strings.Contains(rec.Body.String(), devB.SerialNumber) {
			t.Fatal("company A's list body contains company B's serial number")
		}
	})

	t.Run("unclaimed NULL-company rows are excluded", func(t *testing.T) {
		for name, tok := range map[string]string{"A": ta, "B": tb} {
			rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, tok, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s list: got %d", name, rec.Code)
			}
			for _, r := range testutil.DecodeRows[zkDeviceRow](t, rec) {
				if r.ID == unclaimed.ID || r.CompanyID == nil {
					t.Fatalf("%s's list contains an unclaimed device: %+v", name, r)
				}
			}
		}
	})

	t.Run("id filter cannot reach another tenant or the pool", func(t *testing.T) {
		for _, id := range []string{devB.ID, unclaimed.ID} {
			rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath+"?id="+id, ta, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
			}
			if rows := testutil.DecodeRows[zkDeviceRow](t, rec); len(rows) != 0 {
				t.Fatalf("filtering by %s must return [], got %+v", id, rows)
			}
		}
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath+"?id="+devA.ID, ta, nil)
		if rows := testutil.DecodeRows[zkDeviceRow](t, rec); len(rows) != 1 || rows[0].ID != devA.ID {
			t.Fatalf("own id filter: got %+v", rows)
		}
	})

	t.Run("get by path is not offered and never discloses another tenant's device", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath+"/"+devB.ID, ta, nil)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("got %d, want 404 or 405", rec.Code)
		}
		if strings.Contains(rec.Body.String(), devB.SerialNumber) {
			t.Fatal("response body discloses the other tenant's serial number")
		}
	})

	t.Run("patch another tenant's device is 404 and leaves it unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, zkDevicesPath+"/"+devB.ID, ta, map[string]any{"name": "hijacked", "location": "x"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403), got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		if after := readZkDevice(t, pool, devB.ID); !sameZkDevice(after, devB) {
			t.Fatalf("B's device changed:\nbefore %+v\nafter  %+v", devB, after)
		}
	})

	t.Run("patch an unclaimed device is 404 and leaves it unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, zkDevicesPath+"/"+unclaimed.ID, ta, map[string]any{"name": "taken"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d (%s), want 404", rec.Code, rec.Body.String())
		}
		if after := readZkDevice(t, pool, unclaimed.ID); !sameZkDevice(after, unclaimed) {
			t.Fatalf("unclaimed device changed:\nbefore %+v\nafter  %+v", unclaimed, after)
		}
	})

	t.Run("patch a missing or malformed id is 404", func(t *testing.T) {
		for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodPatch, zkDevicesPath+"/"+id, ta, map[string]any{"name": "x"})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("id %q: got %d, want 404", id, rec.Code)
			}
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		if rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("list: got %d, want 401", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodPatch, zkDevicesPath+"/"+devA.ID, "", map[string]any{"name": "x"}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("patch: got %d, want 401", rec.Code)
		}
	})
}

func TestZktecoDevices_PatchUpdatesOnlyNameAndLocation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkPatchA")
	ta := a.Token(t, signer)
	seen := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	dev := seedZkDevice(t, pool, zkSeed{companyID: &a.CompanyID, name: "old", location: "old loc", lastSeen: &seen})
	path := zkDevicesPath + "/" + dev.ID

	t.Run("updates name and location and returns a one-element array", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{"name": "  Entrada  ", "location": "Lobby"})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		rows := testutil.DecodeRows[zkDeviceRow](t, rec)
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want a one-element array", len(rows))
		}
		got := rows[0]
		if strVal(got.Name) != "Entrada" || strVal(got.Location) != "Lobby" {
			t.Fatalf("name/location = %q/%q, want trimmed Entrada/Lobby", strVal(got.Name), strVal(got.Location))
		}
		if got.SerialNumber != dev.SerialNumber || strVal(got.CompanyID) != a.CompanyID ||
			strVal(got.Status) != "online" || strVal(got.FirmwareVersion) != "fw-1" || !sameInstant(got.LastSeen, dev.LastSeen) {
			t.Fatalf("a field other than name/location changed: %+v", got)
		}
		after := readZkDevice(t, pool, dev.ID)
		if strVal(after.Name) != "Entrada" || after.SerialNumber != dev.SerialNumber || !sameInstant(after.LastSeen, dev.LastSeen) {
			t.Fatalf("stored row unexpected: %+v", after)
		}
	})

	t.Run("an absent key is kept and an explicit null clears", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{"location": nil})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		after := readZkDevice(t, pool, dev.ID)
		if strVal(after.Name) != "Entrada" {
			t.Fatalf("absent name must be kept, got %q", strVal(after.Name))
		}
		if after.Location != nil {
			t.Fatalf("explicit null must clear location, got %q", strVal(after.Location))
		}
	})

	t.Run("an empty object is a no-op that still returns the row", func(t *testing.T) {
		before := readZkDevice(t, pool, dev.ID)
		rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if rows := testutil.DecodeRows[zkDeviceRow](t, rec); len(rows) != 1 || rows[0].ID != dev.ID {
			t.Fatalf("got %+v", rows)
		}
		if after := readZkDevice(t, pool, dev.ID); !sameZkDevice(after, before) {
			t.Fatalf("row changed by an empty patch: %+v -> %+v", before, after)
		}
	})

	t.Run("forbidden fields are rejected with 400 bad_request and nothing changes", func(t *testing.T) {
		before := readZkDevice(t, pool, dev.ID)
		for _, field := range []string{"serial_number", "company_id", "status", "id", "last_seen", "firmware_version", "created_at"} {
			rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{"name": "sneaky", field: "x"})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", field, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "bad_request" {
				t.Fatalf("%s: error.code = %q, want bad_request", field, code)
			}
		}
		if after := readZkDevice(t, pool, dev.ID); !sameZkDevice(after, before) {
			t.Fatalf("a rejected patch changed the row: %+v -> %+v", before, after)
		}
	})

	t.Run("malformed bodies are 400", func(t *testing.T) {
		for _, body := range []string{``, `not json`, `[]`, `null`, `{"name":"a"}{"name":"b"}`, `{"name":5}`} {
			rec := doRawBody(t, h, http.MethodPatch, path, ta, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q: got %d (%s), want 400", body, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("overlong values are validation_failed with the field named", func(t *testing.T) {
		for _, field := range []string{"name", "location"} {
			rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{field: strings.Repeat("x", 201)})
			assertAttendanceValidationFailed(t, rec, field)
		}
	})

	t.Run("a blank name is validation_failed", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, ta, map[string]any{"name": "   "})
		assertAttendanceValidationFailed(t, rec, "name")
	})
}

func TestZktecoDevices_ListRules(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkListA")
	ta := a.Token(t, signer)
	newer := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	older := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	never := seedZkDevice(t, pool, zkSeed{companyID: &a.CompanyID, name: "never seen"})
	old := seedZkDevice(t, pool, zkSeed{companyID: &a.CompanyID, name: "older", lastSeen: &older})
	recent := seedZkDevice(t, pool, zkSeed{companyID: &a.CompanyID, name: "newer", lastSeen: &newer})

	t.Run("ordered by last_seen descending with never-seen last", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		got := zkIDs(testutil.DecodeRows[zkDeviceRow](t, rec))
		want := []string{recent.ID, old.ID, never.ID}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})

	t.Run("limit and offset page the list", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath+"?_limit=1&_offset=1", ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := zkIDs(testutil.DecodeRows[zkDeviceRow](t, rec)); len(got) != 1 || got[0] != old.ID {
			t.Fatalf("page = %v, want [%s]", got, old.ID)
		}
	})

	t.Run("empty list is an array not null", func(t *testing.T) {
		empty := testutil.Company(t, pool, "ZkListEmpty")
		rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, empty.Token(t, signer), nil)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("got %d %q, want 200 []", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown parameters and _order are validation_failed", func(t *testing.T) {
		for _, q := range []string{"?bogus=1", "?company_id=" + a.CompanyID, "?status=online", "?_order=last_seen.desc", "?_limit=0", "?_offset=-1", "?id=not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath+q, ta, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", q, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("%s: error.code = %q, want validation_failed", q, code)
			}
		}
	})

	t.Run("any authenticated role may list and rename", func(t *testing.T) {
		te := a.TokenAs(t, signer, "empleado")
		if rec := testutil.Do(t, h, http.MethodGet, zkDevicesPath, te, nil); rec.Code != http.StatusOK {
			t.Fatalf("list as empleado: got %d (%s)", rec.Code, rec.Body.String())
		}
		rec := testutil.Do(t, h, http.MethodPatch, zkDevicesPath+"/"+recent.ID, te, map[string]any{"name": "renamed"})
		if rec.Code != http.StatusOK {
			t.Fatalf("patch as empleado: got %d (%s)", rec.Code, rec.Body.String())
		}
	})
}
