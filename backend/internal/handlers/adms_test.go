package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/ratelimit"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// ADMS HTTP tests. The wire format is PROVISIONAL (see adms.go); the tenancy,
// idempotency, rejection and limit behavior asserted here is binding. Every
// test builds its own API: testutil.Server keeps the production limiter, and
// these tests need a fake-clock one and a readable log.

const (
	admsDay     = "2026-03-02"
	admsPath    = "/iclock/cdata"
	admsGetPath = "/iclock/getrequest"
	admsCmdPath = "/iclock/devicecmd"
)

type admsEnv struct {
	h    http.Handler
	clk  *stepClock
	logs *bytes.Buffer
}

func newADMS(t *testing.T, pool *pgxpool.Pool, perMinute, burst int) admsEnv {
	t.Helper()
	signer := auth.NewSigner(testutil.TestSecret, time.Hour)
	logs := &bytes.Buffer{}
	api := handlers.New(pool, signer, slog.New(slog.NewJSONHandler(logs, nil)))
	clk := &stepClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	api.ADMSLimiter = ratelimit.New(perMinute, burst)
	api.ADMSLimiter.Now = clk.Now
	return admsEnv{h: api.Routes(signer), clk: clk, logs: logs}
}

// newADMSDefault is an env whose limiter is large enough to never interfere.
func newADMSDefault(t *testing.T, pool *pgxpool.Pool) admsEnv {
	return newADMS(t, pool, 100000, 100000)
}

func (e admsEnv) do(method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:5000"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// push posts an ATTLOG body for the serial and returns the response.
func (e admsEnv) push(sn, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, admsPath+"?SN="+sn+"&table=ATTLOG&Stamp=1", body)
}

// line renders one ATTLOG line: PIN \t datetime \t unused \t code.
func line(pin, at string, code int) string {
	return fmt.Sprintf("%s\t%s\t0\t%d", pin, at, code)
}

func batch(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// claimedDevice inserts a device owned by the tenant and returns its serial.
func claimedDevice(t *testing.T, pool *pgxpool.Pool, tn testutil.Tenant) string {
	t.Helper()
	sn := admsSerial(t, pool)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO zkteco_devices (company_id, serial_number, status, last_seen) VALUES ($1, $2, 'offline', now() - interval '1 day')`,
		tn.CompanyID, sn); err != nil {
		t.Fatalf("insert claimed device: %v", err)
	}
	return sn
}

// unclaimedDevice inserts a device with no company, as the importer leaves an
// unclaimed one, and returns its serial: a known serial ADMS accepts.
func unclaimedDevice(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	sn := admsSerial(t, pool)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO zkteco_devices (serial_number, status, last_seen) VALUES ($1, 'offline', now() - interval '1 day')`, sn); err != nil {
		t.Fatalf("insert unclaimed device: %v", err)
	}
	return sn
}

func deviceCount(t *testing.T, pool *pgxpool.Pool, sn string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM zkteco_devices WHERE serial_number = $1`, sn).Scan(&n); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	return n
}

// counters returns the rejection counters of a serial keyed by reason.
func counters(t *testing.T, pool *pgxpool.Pool, sn string) map[string]int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT reason, count FROM zkteco_ingest_rejections WHERE serial_number = $1`, sn)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var reason string
		var n int64
		if err := rows.Scan(&reason, &n); err != nil {
			t.Fatal(err)
		}
		out[reason] = n
	}
	return out
}

func wantCounters(t *testing.T, pool *pgxpool.Pool, sn string, want map[string]int64) {
	t.Helper()
	got := counters(t, pool, sn)
	if len(got) != len(want) {
		t.Fatalf("counters = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("counters = %v, want %v", got, want)
		}
	}
}

func unknownPinAttempts(t *testing.T, pool *pgxpool.Pool, sn, pin string) (attempts int, exists bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT attempts FROM zkteco_unknown_pins WHERE serial_number = $1 AND pin = $2`, sn, pin).Scan(&attempts)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false
	case err != nil:
		// Any other failure (dropped column, closed pool, bad schema) must not
		// read as "no such pin", or every absence assertion passes vacuously.
		t.Fatalf("read unknown pin attempts for %s/%s: %v", sn, pin, err)
	}
	return attempts, true
}

func wantBody(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || strings.TrimSpace(rec.Body.String()) != body {
		t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), status, body)
	}
}

func TestADMS_PublicEndpointsNeedNoTokenAndAPIStaysProtected(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	sn := unclaimedDevice(t, pool)

	rec := env.do(http.MethodGet, admsPath+"?SN="+sn+"&options=all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET cdata without Authorization got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "GET OPTION") || !strings.Contains(body, "ATTLOGStamp=") {
		t.Fatalf("option text = %q", body)
	}

	wantBody(t, env.do(http.MethodGet, admsGetPath+"?SN="+sn, ""), http.StatusOK, "OK")
	wantBody(t, env.do(http.MethodPost, admsCmdPath+"?SN="+sn, "ID=1&Return=0&CMD=DATA"), http.StatusOK, "OK")

	// Opening /iclock/ must not weaken /api/*.
	for _, target := range []string{"/api/users", "/api/zkteco_devices", "/api/zkteco_unknown_pins", "/api/me"} {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token got %d, want 401", target, rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "unauthorized" {
			t.Fatalf("GET %s error.code = %q, want unauthorized", target, code)
		}
	}
}

func TestADMS_HeartbeatRefreshesKnownDevicesAndNeverChangesOwner(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpHbA")

	t.Run("an imported unclaimed device is refreshed and stays unclaimed", func(t *testing.T) {
		sn := unclaimedDevice(t, pool)
		if env.do(http.MethodGet, admsPath+"?SN="+sn, "").Code != http.StatusOK {
			t.Fatal("heartbeat failed")
		}
		var company *string
		var status string
		var fresh bool
		if err := pool.QueryRow(context.Background(),
			`SELECT company_id::text, status, last_seen > now() - interval '1 minute' FROM zkteco_devices WHERE serial_number = $1`, sn,
		).Scan(&company, &status, &fresh); err != nil {
			t.Fatalf("device row: %v", err)
		}
		if company != nil || status != "online" || !fresh {
			t.Fatalf("company=%v status=%s fresh=%v, want NULL / online / true", company, status, fresh)
		}
	})

	t.Run("a lower-case sn parameter is accepted", func(t *testing.T) {
		sn := unclaimedDevice(t, pool)
		if env.do(http.MethodGet, admsPath+"?sn="+sn, "").Code != http.StatusOK || deviceCount(t, pool, sn) != 1 {
			t.Fatal("lower-case sn not accepted")
		}
	})

	t.Run("the heartbeat of a claimed device keeps its company and refreshes status", func(t *testing.T) {
		sn := claimedDevice(t, pool, a)
		for _, target := range []string{admsPath + "?SN=" + sn, admsGetPath + "?SN=" + sn} {
			if _, err := pool.Exec(context.Background(), `UPDATE zkteco_devices SET status = 'offline' WHERE serial_number = $1`, sn); err != nil {
				t.Fatal(err)
			}
			if env.do(http.MethodGet, target, "").Code != http.StatusOK {
				t.Fatalf("%s failed", target)
			}
			var company, status string
			if err := pool.QueryRow(context.Background(),
				`SELECT company_id::text, status FROM zkteco_devices WHERE serial_number = $1`, sn).Scan(&company, &status); err != nil {
				t.Fatal(err)
			}
			if company != a.CompanyID || status != "online" {
				t.Fatalf("%s: company=%s status=%s, want %s / online", target, company, status, a.CompanyID)
			}
		}
	})

	t.Run("missing, overlong or malformed serials are 400 and write nothing", func(t *testing.T) {
		bad := map[string]string{
			"missing":   "",
			"overlong":  "A" + strings.Repeat("B", 64),
			"space":     "AB%20CD",
			"semicolon": "AB;DROP",
			"slash":     "AB/CD",
			"unicode":   "S%C3%91N",
		}
		for name, sn := range bad {
			// The serial as the server decodes it: the no-write check below
			// must look for exactly this value, whatever characters it holds.
			decoded, err := url.QueryUnescape(sn)
			if err != nil {
				t.Fatalf("%s: bad fixture %q: %v", name, sn, err)
			}
			for _, target := range []string{admsPath, admsGetPath} {
				u := target
				if name != "missing" {
					u += "?SN=" + sn
				}
				rec := env.do(http.MethodGet, u, "")
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("%s GET %s got %d, want 400", name, u, rec.Code)
				}
				if code := testutil.ErrCode(t, rec); code != "bad_request" {
					t.Fatalf("%s: error.code = %q, want bad_request", name, code)
				}
			}
			rec := env.do(http.MethodPost, admsPath+"?table=ATTLOG&SN="+sn, line("1", admsDay+" 08:00:00", 0))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s POST got %d, want 400", name, rec.Code)
			}
			if name != "missing" {
				if n := deviceCount(t, pool, decoded); n != 0 {
					t.Fatalf("%s: rejected serial %q created %d device rows", name, decoded, n)
				}
			}
		}
		// The longest valid serial (64) is accepted once registered, so the
		// boundary is exact.
		long := "S" + strings.Repeat("9", 63)
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_devices WHERE serial_number = $1`, long)
		})
		if _, err := pool.Exec(context.Background(), `INSERT INTO zkteco_devices (serial_number) VALUES ($1)`, long); err != nil {
			t.Fatalf("register long serial: %v", err)
		}
		if env.do(http.MethodGet, admsPath+"?SN="+long, "").Code != http.StatusOK || deviceCount(t, pool, long) != 1 {
			t.Fatal("a registered 64 character serial must be accepted")
		}
	})
}

// A serial nobody registered is refused on every ADMS endpoint and leaves no
// trace: no device row, no rejection counter, no unknown pin (runbook B2/B3).
func TestADMS_UnknownSerialIsRefusedAndWritesNothing(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	sn := admsSerial(t, pool)

	for _, c := range []struct{ method, target, body string }{
		{http.MethodGet, admsPath + "?SN=" + sn + "&options=all", ""},
		{http.MethodGet, admsGetPath + "?SN=" + sn, ""},
		{http.MethodPost, admsPath + "?table=ATTLOG&SN=" + sn, line("1234", admsDay+" 08:00:00", 0)},
		{http.MethodPost, admsPath + "?table=OPERLOG&SN=" + sn, "OPLOG 1\n"},
		{http.MethodPost, admsCmdPath + "?SN=" + sn, "ID=1&Return=0&CMD=DATA"},
	} {
		rec := env.do(c.method, c.target, c.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s got %d (%s), want 403", c.method, c.target, rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "forbidden" {
			t.Fatalf("%s %s error.code = %q, want forbidden", c.method, c.target, code)
		}
	}
	if n := deviceCount(t, pool, sn); n != 0 {
		t.Fatalf("an unknown serial created %d device rows", n)
	}
	wantCounters(t, pool, sn, map[string]int64{})
	if _, exists := unknownPinAttempts(t, pool, sn, "1234"); exists {
		t.Fatal("an unknown serial must not write unknown pins")
	}
}

func TestADMS_UnclaimedDeviceNeverCreatesAttendance(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpUnclaimed")
	pin := "UC-" + uuid.NewString()[:8]
	emp := admsEmployee(t, pool, a, pin)
	sn := unclaimedDevice(t, pool)

	rec := env.push(sn, batch(
		line(pin, admsDay+" 08:00:00", 0),
		line(pin, admsDay+" 17:00:00", 1),
		"garbage",
	))
	wantBody(t, rec, http.StatusOK, "OK: 0")

	if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 0 {
		t.Fatalf("unclaimed device created %d attendance rows", got.Count)
	}
	wantCounters(t, pool, sn, map[string]int64{"device_unclaimed": 2, "malformed": 1})
	if _, exists := unknownPinAttempts(t, pool, sn, pin); exists {
		t.Fatal("an unclaimed device must not write unknown pins")
	}
	if deviceCount(t, pool, sn) != 1 {
		t.Fatal("the POST must keep exactly the one imported device row")
	}

	// Claiming it later makes the same punch count.
	if _, err := pool.Exec(context.Background(), `UPDATE zkteco_devices SET company_id = $1 WHERE serial_number = $2`, a.CompanyID, sn); err != nil {
		t.Fatal(err)
	}
	wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 1")
	if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 1 {
		t.Fatalf("after the claim the punch must count, rows = %d", got.Count)
	}
}

func TestADMS_TenantScopedCedulaResolution(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpResA")
	b := testutil.Company(t, pool, "AdmsHttpResB")

	t.Run("a cedula that exists only in another company is persisted exactly like not_found", func(t *testing.T) {
		pin := "XT-" + uuid.NewString()[:8]
		empB := admsEmployee(t, pool, b, pin)
		sn := claimedDevice(t, pool, a)
		env.logs.Reset()

		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 0")

		if got := readAttendance(t, pool, empB.ID, admsDay); got.Count != 0 {
			t.Fatal("B's employee received attendance from A's device")
		}
		wantCounters(t, pool, sn, map[string]int64{"not_found": 1})
		if n, ok := unknownPinAttempts(t, pool, sn, pin); !ok || n != 1 {
			t.Fatalf("unknown pin attempts = %d exists=%v, want 1 / true", n, ok)
		}
		logs := env.logs.String()
		// The key is logged on every batch (with 0 when nothing crossed a
		// tenant), so only the exact count proves the lookup detected it.
		if !strings.Contains(logs, `"tenant_mismatch":1`) {
			t.Fatalf("the distinction must appear in the server log as tenant_mismatch=1, got %s", logs)
		}
		if strings.Contains(logs, pin) {
			t.Fatalf("the log line leaks the cedula: %s", logs)
		}
	})

	t.Run("a cedula that exists nowhere is the same not_found with no tenant_mismatch in the log", func(t *testing.T) {
		pin := "NX-" + uuid.NewString()[:8]
		sn := claimedDevice(t, pool, a)
		env.logs.Reset()
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 0")
		wantCounters(t, pool, sn, map[string]int64{"not_found": 1})
		if n, ok := unknownPinAttempts(t, pool, sn, pin); !ok || n != 1 {
			t.Fatalf("unknown pin attempts = %d exists=%v", n, ok)
		}
		if !strings.Contains(env.logs.String(), `"tenant_mismatch":0`) {
			t.Fatalf("nothing crossed a tenant here, want tenant_mismatch=0 in %s", env.logs.String())
		}
	})

	t.Run("a cedula shared by both companies resolves to the device company's employee only", func(t *testing.T) {
		pin := "SH-" + uuid.NewString()[:8]
		empA := admsEmployee(t, pool, a, pin)
		empB := admsEmployee(t, pool, b, pin)
		sn := claimedDevice(t, pool, a)
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 1")
		if got := readAttendance(t, pool, empA.ID, admsDay); got.Count != 1 {
			t.Fatalf("A's employee rows = %d, want 1", got.Count)
		}
		if got := readAttendance(t, pool, empB.ID, admsDay); got.Count != 0 {
			t.Fatalf("B's employee rows = %d, want 0", got.Count)
		}
	})

	t.Run("two employees of the same company sharing the cedula are ambiguous", func(t *testing.T) {
		pin := "AM-" + uuid.NewString()[:8]
		e1 := admsEmployee(t, pool, a, pin)
		e2 := admsEmployee(t, pool, a, pin)
		sn := claimedDevice(t, pool, a)
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 0")
		for _, e := range []string{e1.ID, e2.ID} {
			if got := readAttendance(t, pool, e, admsDay); got.Count != 0 {
				t.Fatalf("ambiguous cedula wrote %d rows", got.Count)
			}
		}
		wantCounters(t, pool, sn, map[string]int64{"ambiguous": 1})
		if _, ok := unknownPinAttempts(t, pool, sn, pin); ok {
			t.Fatal("an ambiguous cedula is not an unknown pin")
		}
	})
}

func TestADMS_CheckInOutSemanticsAndIdempotency(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpInOut")
	sn := claimedDevice(t, pool, a)

	t.Run("08:00 in and 17:00 out make one row", func(t *testing.T) {
		pin := "IO-" + uuid.NewString()[:8]
		emp := admsEmployee(t, pool, a, pin)
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0), line(pin, admsDay+" 17:00:00", 1))), http.StatusOK, "OK: 2")
		got := readAttendance(t, pool, emp.ID, admsDay)
		if got.Count != 1 || orNil(got.TimeIn) != "08:00:00" || orNil(got.TimeOut) != "17:00:00" || orNil(got.Status) != "present" {
			t.Fatalf("row = %+v in=%s out=%s", got, orNil(got.TimeIn), orNil(got.TimeOut))
		}
	})

	t.Run("the same pair in reverse line order gives the same row", func(t *testing.T) {
		pin := "RV-" + uuid.NewString()[:8]
		emp := admsEmployee(t, pool, a, pin)
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 17:00:00", 1), line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 2")
		got := readAttendance(t, pool, emp.ID, admsDay)
		if got.Count != 1 || orNil(got.TimeIn) != "08:00:00" || orNil(got.TimeOut) != "17:00:00" {
			t.Fatalf("reverse order: in=%s out=%s count=%d", orNil(got.TimeIn), orNil(got.TimeOut), got.Count)
		}
		wantCounters(t, pool, sn, map[string]int64{})
	})

	t.Run("re-pushing the check-in after the check-out keeps time_out and one row", func(t *testing.T) {
		pin := "RP-" + uuid.NewString()[:8]
		emp := admsEmployee(t, pool, a, pin)
		env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0), line(pin, admsDay+" 17:00:00", 1)))
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0))), http.StatusOK, "OK: 1")
		got := readAttendance(t, pool, emp.ID, admsDay)
		if got.Count != 1 || orNil(got.TimeIn) != "08:00:00" || orNil(got.TimeOut) != "17:00:00" {
			t.Fatalf("after re-push: in=%s out=%s count=%d", orNil(got.TimeIn), orNil(got.TimeOut), got.Count)
		}
	})

	t.Run("a check-out without a check-in is rejected as no_checkin and creates no row", func(t *testing.T) {
		pin := "NO-" + uuid.NewString()[:8]
		emp := admsEmployee(t, pool, a, pin)
		sn := claimedDevice(t, pool, a)
		wantBody(t, env.push(sn, batch(line(pin, admsDay+" 17:00:00", 1))), http.StatusOK, "OK: 0")
		if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 0 {
			t.Fatalf("check-out alone created %d rows", got.Count)
		}
		wantCounters(t, pool, sn, map[string]int64{"no_checkin": 1})
	})

	t.Run("posting the same body twice leaves identical attendance state", func(t *testing.T) {
		pin := "ID-" + uuid.NewString()[:8]
		emp := admsEmployee(t, pool, a, pin)
		body := batch(line(pin, admsDay+" 08:05:00", 0), line(pin, admsDay+" 16:55:00", 1))
		env.push(sn, body)
		first := readAttendance(t, pool, emp.ID, admsDay)
		wantBody(t, env.push(sn, body), http.StatusOK, "OK: 2")
		second := readAttendance(t, pool, emp.ID, admsDay)
		if first.Count != 1 || second.Count != 1 ||
			orNil(first.TimeIn) != orNil(second.TimeIn) || orNil(first.TimeOut) != orNil(second.TimeOut) || orNil(first.Status) != orNil(second.Status) {
			t.Fatalf("state changed on re-push: %+v -> %+v", first, second)
		}
	})
}

func TestADMS_UnknownPinsCountEveryAttemptButResolvedPunchesDoNot(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpUnknown")
	sn := claimedDevice(t, pool, a)
	known := "KN-" + uuid.NewString()[:8]
	emp := admsEmployee(t, pool, a, known)
	unknown := "UN-" + uuid.NewString()[:8]

	body := batch(line(unknown, admsDay+" 08:00:00", 0), line(known, admsDay+" 08:00:00", 0))
	for i := 0; i < 3; i++ {
		wantBody(t, env.push(sn, body), http.StatusOK, "OK: 1")
	}
	if n, ok := unknownPinAttempts(t, pool, sn, unknown); !ok || n != 3 {
		t.Fatalf("unknown pin attempts = %d exists=%v, want 3", n, ok)
	}
	if _, ok := unknownPinAttempts(t, pool, sn, known); ok {
		t.Fatal("a resolved pin must never reach the unknown pins table")
	}
	if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 1 {
		t.Fatalf("resolved punch rows = %d, want 1", got.Count)
	}
	wantCounters(t, pool, sn, map[string]int64{"not_found": 3})

	// Several lines of one pin in a single batch also count one attempt each.
	other := "U2-" + uuid.NewString()[:8]
	env.push(sn, batch(line(other, admsDay+" 08:00:00", 0), line(other, admsDay+" 09:00:00", 0)))
	if n, _ := unknownPinAttempts(t, pool, sn, other); n != 2 {
		t.Fatalf("two lines of one unknown pin: attempts = %d, want 2", n)
	}
}

func TestADMS_MixedBatchCountsEveryReason(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpMixed")
	sn := claimedDevice(t, pool, a)
	p1, p2 := "M1-"+uuid.NewString()[:8], "M2-"+uuid.NewString()[:8]
	admsEmployee(t, pool, a, p1)
	admsEmployee(t, pool, a, p2)
	amb := "MA-" + uuid.NewString()[:8]
	admsEmployee(t, pool, a, amb)
	admsEmployee(t, pool, a, amb)

	body := batch(
		line(p1, admsDay+" 08:00:00", 0), // processed
		"not a punch line",               // malformed
		line(p2, admsDay+" 09:00:00", 0), // processed
		line(p2, admsDay+" 10:00:00", 2), // unsupported_code
		line("GHOST", admsDay+" 08:00:00", 0),
		line(amb, admsDay+" 08:00:00", 0),
		line("NOIN-"+p1, admsDay+" 18:00:00", 1),
	)
	wantBody(t, env.push(sn, body), http.StatusOK, "OK: 2")
	// "NOIN-"+p1 matches no employee, so it is not_found, not no_checkin.
	wantCounters(t, pool, sn, map[string]int64{"malformed": 1, "unsupported_code": 1, "not_found": 2, "ambiguous": 1})

	// Second push: counters count events, so each rejection doubles while the
	// two processed punches stay idempotent.
	wantBody(t, env.push(sn, body), http.StatusOK, "OK: 2")
	wantCounters(t, pool, sn, map[string]int64{"malformed": 2, "unsupported_code": 2, "not_found": 4, "ambiguous": 2})

	// no_checkin has its own reason, distinct from not_found.
	only := "NC-" + uuid.NewString()[:8]
	admsEmployee(t, pool, a, only)
	env.push(sn, batch(line(only, admsDay+" 18:00:00", 1)))
	if got := counters(t, pool, sn); got["no_checkin"] != 1 {
		t.Fatalf("no_checkin counter = %d, want 1 (%v)", got["no_checkin"], got)
	}
}

func TestADMS_NonATTLOGTablesAreAckedWithoutProcessing(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpOper")
	sn := claimedDevice(t, pool, a)
	pin := "OP-" + uuid.NewString()[:8]
	emp := admsEmployee(t, pool, a, pin)

	for _, table := range []string{"OPERLOG", "operlog", "BIODATA", ""} {
		target := admsPath + "?SN=" + sn
		if table != "" {
			target += "&table=" + table
		}
		wantBody(t, env.do(http.MethodPost, target, line(pin, admsDay+" 08:00:00", 0)), http.StatusOK, "OK")
	}
	if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 0 {
		t.Fatalf("a non-ATTLOG table created %d attendance rows", got.Count)
	}
	wantCounters(t, pool, sn, map[string]int64{})

	// The table name is case-insensitive for ATTLOG itself.
	wantBody(t, env.do(http.MethodPost, admsPath+"?SN="+sn+"&table=attlog", line(pin, admsDay+" 08:00:00", 0)), http.StatusOK, "OK: 1")
}

func TestADMS_BodyAndLineCapsRefuseBeforeAnyWrite(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpCaps")
	pin := "CP-" + uuid.NewString()[:8]
	emp := admsEmployee(t, pool, a, pin)

	t.Run("a body over 2 MiB is 413 and nothing is written", func(t *testing.T) {
		sn := admsSerial(t, pool)
		body := line(pin, admsDay+" 08:00:00", 0) + "\n" + strings.Repeat("x", 2<<20)
		rec := env.push(sn, body)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got %d, want 413", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want the JSON envelope", code)
		}
		if deviceCount(t, pool, sn) != 0 {
			t.Fatal("a 413 must not even self-register the device")
		}
	})

	t.Run("a body of exactly 2 MiB is accepted", func(t *testing.T) {
		sn := claimedDevice(t, pool, a)
		first := line(pin, admsDay+" 08:00:00", 0) + "\n"
		body := first + strings.Repeat(" ", (2<<20)-len(first))
		if len(body) != 2<<20 {
			t.Fatalf("test body is %d bytes", len(body))
		}
		wantBody(t, env.push(sn, body), http.StatusOK, "OK: 1")
	})

	t.Run("more than 10,000 non-empty lines is 413 and nothing is written", func(t *testing.T) {
		sn := claimedDevice(t, pool, a)
		var b strings.Builder
		for i := 0; i < 10001; i++ {
			b.WriteString(line(pin, fmt.Sprintf("2026-04-01 08:%02d:%02d", (i/60)%60, i%60), 0))
			b.WriteString("\n")
		}
		before := readAttendance(t, pool, emp.ID, "2026-04-01")
		rec := env.push(sn, b.String())
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got %d, want 413", rec.Code)
		}
		if got := readAttendance(t, pool, emp.ID, "2026-04-01"); got.Count != before.Count {
			t.Fatalf("the refused batch wrote attendance: %d -> %d rows", before.Count, got.Count)
		}
		wantCounters(t, pool, sn, map[string]int64{})
		var seen *time.Time
		if err := pool.QueryRow(context.Background(), `SELECT last_seen FROM zkteco_devices WHERE serial_number = $1`, sn).Scan(&seen); err != nil || seen == nil || time.Since(*seen) < time.Hour {
			t.Fatalf("a 413 must not even refresh last_seen (seen=%v err=%v)", seen, err)
		}
	})

	t.Run("exactly 10,000 lines is processed", func(t *testing.T) {
		sn := claimedDevice(t, pool, a)
		var b strings.Builder
		for i := 0; i < 10000; i++ {
			b.WriteString("noise\n") // malformed, but counted as one line each
		}
		wantBody(t, env.push(sn, b.String()), http.StatusOK, "OK: 0")
		wantCounters(t, pool, sn, map[string]int64{"malformed": 10000})
	})
}

func TestADMS_RateLimitedPerClientWith429(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMS(t, pool, 3, 3)
	sn := unclaimedDevice(t, pool)

	for i := 1; i <= 3; i++ {
		if rec := env.do(http.MethodGet, admsGetPath+"?SN="+sn, ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d got %d, want 200 inside the burst", i, rec.Code)
		}
	}
	rec := env.do(http.MethodGet, admsGetPath+"?SN="+sn, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if code := testutil.ErrCode(t, rec); code != "rate_limited" {
		t.Fatalf("error.code = %q, want rate_limited", code)
	}
	// Every iclock route shares the one budget.
	for _, r := range []struct{ m, p string }{{http.MethodGet, admsPath}, {http.MethodPost, admsPath}, {http.MethodPost, admsCmdPath}} {
		if got := env.do(r.m, r.p+"?SN="+sn, "").Code; got != http.StatusTooManyRequests {
			t.Fatalf("%s %s got %d, want 429", r.m, r.p, got)
		}
	}
	// A limited request never reaches the handler: nothing was written for it.
	other := admsSerial(t, pool)
	if env.do(http.MethodGet, admsPath+"?SN="+other, "").Code != http.StatusTooManyRequests || deviceCount(t, pool, other) != 0 {
		t.Fatal("a 429 request must not reach the database")
	}

	env.clk.Advance(time.Minute)
	if got := env.do(http.MethodGet, admsGetPath+"?SN="+sn, "").Code; got != http.StatusOK {
		t.Fatalf("after the window got %d, want 200", got)
	}
}

// A database failure in the middle of a batch rolls the whole request back:
// no attendance, no counters, no heartbeat; the device retries and idempotency
// makes the retry safe. The failure is injected in an isolated schema with a
// trigger that fails the LAST write of the request (the counters flush), so
// the earlier writes must be undone by the rollback.
func TestADMS_DatabaseErrorMidBatchRollsBackEverything(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	env := newADMSDefault(t, pool)
	a := testutil.Company(t, pool, "AdmsHttpRollback")
	pin := "RB-" + uuid.NewString()[:8]
	emp := admsEmployee(t, pool, a, pin)
	sn := claimedDevice(t, pool, a)

	// Healthy control: the same shape of request succeeds first.
	wantBody(t, env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0), "garbage")), http.StatusOK, "OK: 1")
	if _, err := pool.Exec(context.Background(), `DELETE FROM attendance_logs WHERE employee_id = $1`, emp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM zkteco_ingest_rejections`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE zkteco_devices SET status = 'offline', last_seen = now() - interval '1 day' WHERE serial_number = $1`, sn); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION fail_rejections() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected failure'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_rejections BEFORE INSERT OR UPDATE ON zkteco_ingest_rejections
		FOR EACH ROW EXECUTE FUNCTION fail_rejections();`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	rec := env.push(sn, batch(line(pin, admsDay+" 08:00:00", 0), "garbage"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "internal" {
		t.Fatalf("error.code = %q, want internal", code)
	}
	if got := readAttendance(t, pool, emp.ID, admsDay); got.Count != 0 {
		t.Fatalf("attendance survived the rollback: %d rows", got.Count)
	}
	var status string
	var stale bool
	if err := pool.QueryRow(context.Background(),
		`SELECT status, last_seen < now() - interval '1 hour' FROM zkteco_devices WHERE serial_number = $1`, sn).Scan(&status, &stale); err != nil {
		t.Fatal(err)
	}
	if status != "offline" || !stale {
		t.Fatalf("the heartbeat survived the rollback: status=%s stale=%v", status, stale)
	}
	if n := len(counters(t, pool, sn)); n != 0 {
		t.Fatalf("%d counters survived the rollback", n)
	}
}

func TestADMS_ReadBodyErrorsDoNotEscapeAsSuccess(t *testing.T) {
	pool := testutil.Pool(t)
	env := newADMSDefault(t, pool)
	sn := admsSerial(t, pool)

	req := httptest.NewRequest(http.MethodPost, admsPath+"?SN="+sn+"&table=ATTLOG", io.NopCloser(errReader{}))
	req.RemoteAddr = "192.0.2.10:5000"
	rec := httptest.NewRecorder()
	env.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable body got %d, want 400", rec.Code)
	}
	if deviceCount(t, pool, sn) != 0 {
		t.Fatal("an unreadable body must not write")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
