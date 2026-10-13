package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/testutil"
)

func admsUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func admsDate(t *testing.T, s string) pgtype.Date {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.Date{Time: d, Valid: true}
}

func admsTime(t *testing.T, s string) pgtype.Time {
	t.Helper()
	v, err := time.Parse("15:04:05", s)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.Time{Microseconds: int64(v.Hour()*3600+v.Minute()*60+v.Second()) * 1_000_000, Valid: true}
}

// admsEmployee inserts an employee with the given cedula for the tenant.
func admsEmployee(t *testing.T, pool *pgxpool.Pool, tn testutil.Tenant, cedula string) testutil.EmployeeFixture {
	t.Helper()
	emp := testutil.Employee(t, pool, tn, testutil.EmployeeDepartment("Ventas"))
	if _, err := pool.Exec(context.Background(), `UPDATE employees SET cedula = $1 WHERE id = $2`, cedula, emp.ID); err != nil {
		t.Fatalf("set cedula: %v", err)
	}
	return emp
}

// admsSerial returns a unique device serial and removes the device and any
// counters written for it afterwards (devices survive company deletion as
// unclaimed rows, which other tests read).
func admsSerial(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	sn := "ADMSQ-" + uuid.NewString()[:12]
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM zkteco_devices WHERE serial_number = $1`, sn)
		_, _ = pool.Exec(ctx, `DELETE FROM zkteco_ingest_rejections WHERE serial_number = $1`, sn)
	})
	return sn
}

func attendanceRow(t *testing.T, pool *pgxpool.Pool, empID, date string) (count int, timeIn, timeOut, status, name, dept *string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM attendance_logs WHERE employee_id = $1 AND date = $2`, empID, date).Scan(&count); err != nil {
		t.Fatalf("count attendance: %v", err)
	}
	if count == 0 {
		return
	}
	if err := pool.QueryRow(ctx,
		`SELECT time_in::text, time_out::text, status, employee_name, department
		 FROM attendance_logs WHERE employee_id = $1 AND date = $2`, empID, date,
	).Scan(&timeIn, &timeOut, &status, &name, &dept); err != nil {
		t.Fatalf("read attendance: %v", err)
	}
	return
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestUpsertDeviceHeartbeat_CreatesUnclaimedAndNeverChangesOwner(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsHbA")
	sn := admsSerial(t, pool)

	first, err := q.UpsertDeviceHeartbeat(ctx, sn)
	if err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if first.CompanyID.Valid {
		t.Fatalf("new device must be unclaimed, company_id = %v", first.CompanyID)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM zkteco_devices WHERE id = $1`, first.ID).Scan(&status); err != nil || status != "online" {
		t.Fatalf("status = %q err=%v, want online", status, err)
	}

	// Claim, age the row, heartbeat again: ownership must survive and
	// last_seen/status must refresh.
	if _, err := pool.Exec(ctx,
		`UPDATE zkteco_devices SET company_id = $1, status = 'offline', last_seen = now() - interval '1 day' WHERE id = $2`,
		a.CompanyID, first.ID); err != nil {
		t.Fatalf("claim device: %v", err)
	}
	second, err := q.UpsertDeviceHeartbeat(ctx, sn)
	if err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("heartbeat created a second row: %v vs %v", second.ID, first.ID)
	}
	if !second.CompanyID.Valid || second.CompanyID != admsUUID(t, a.CompanyID) {
		t.Fatalf("heartbeat changed ownership: %v, want %s", second.CompanyID, a.CompanyID)
	}
	var recent bool
	if err := pool.QueryRow(ctx,
		`SELECT status = 'online' AND last_seen > now() - interval '1 minute' FROM zkteco_devices WHERE id = $1`,
		first.ID).Scan(&recent); err != nil || !recent {
		t.Fatalf("heartbeat did not refresh status/last_seen: recent=%v err=%v", recent, err)
	}
}

func TestResolveEmployeesByCedula_IsCompanyScopedWithLimit2(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsResA")
	b := testutil.Company(t, pool, "AdmsResB")
	shared := "SH-" + uuid.NewString()[:8]
	onlyB := "OB-" + uuid.NewString()[:8]
	dup := "DUP-" + uuid.NewString()[:8]

	empA := admsEmployee(t, pool, a, shared)
	admsEmployee(t, pool, b, shared)
	admsEmployee(t, pool, b, onlyB)
	admsEmployee(t, pool, a, dup)
	admsEmployee(t, pool, a, dup)
	admsEmployee(t, pool, a, dup)

	// Shared cedula resolves to A's employee only.
	rows, err := q.ResolveEmployeesByCedula(ctx, db.ResolveEmployeesByCedulaParams{CompanyID: admsUUID(t, a.CompanyID), Cedula: shared})
	if err != nil {
		t.Fatalf("resolve shared: %v", err)
	}
	if len(rows) != 1 || rows[0] != admsUUID(t, empA.ID) {
		t.Fatalf("shared cedula for A: got %v, want exactly [%s]", rows, empA.ID)
	}

	// A cedula that exists only in B does not resolve for A.
	rows, err = q.ResolveEmployeesByCedula(ctx, db.ResolveEmployeesByCedulaParams{CompanyID: admsUUID(t, a.CompanyID), Cedula: onlyB})
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-company cedula resolved for A: rows=%v err=%v", rows, err)
	}

	// Three matches inside A: LIMIT 2 is enough to detect ambiguity.
	rows, err = q.ResolveEmployeesByCedula(ctx, db.ResolveEmployeesByCedulaParams{CompanyID: admsUUID(t, a.CompanyID), Cedula: dup})
	if err != nil || len(rows) != 2 {
		t.Fatalf("ambiguous cedula: want 2 rows (LIMIT 2), got %d err=%v", len(rows), err)
	}
}

func TestCedulaExistsElsewhere(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsElseA")
	b := testutil.Company(t, pool, "AdmsElseB")
	onlyB := "EB-" + uuid.NewString()[:8]
	onlyA := "EA-" + uuid.NewString()[:8]
	admsEmployee(t, pool, b, onlyB)
	admsEmployee(t, pool, a, onlyA)

	cases := []struct {
		name   string
		cedula string
		want   bool
	}{
		{"exists only in another company", onlyB, true},
		{"exists only in own company", onlyA, false},
		{"exists nowhere", "NOPE-" + uuid.NewString()[:8], false},
	}
	for _, tc := range cases {
		got, err := q.CedulaExistsElsewhere(ctx, db.CedulaExistsElsewhereParams{CompanyID: admsUUID(t, a.CompanyID), Cedula: tc.cedula})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestUpsertUnknownPin_IncrementsAttemptsPerDevice(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	pin := "UP-" + uuid.NewString()[:10]
	snA, snB := "SN-A-"+uuid.NewString()[:8], "SN-B-"+uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_unknown_pins WHERE pin = $1`, pin) })

	upsert := func(sn string) {
		t.Helper()
		if err := q.UpsertUnknownPin(ctx, db.UpsertUnknownPinParams{Pin: pin, SerialNumber: pgtype.Text{String: sn, Valid: true}}); err != nil {
			t.Fatalf("upsert %s: %v", sn, err)
		}
	}
	for i := 0; i < 3; i++ {
		upsert(snA)
	}
	upsert(snB)

	attemptsOf := func(sn string) (attempts int, seen bool) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT attempts, last_seen > now() - interval '1 minute' FROM zkteco_unknown_pins WHERE pin = $1 AND serial_number = $2`,
			pin, sn).Scan(&attempts, &seen); err != nil {
			t.Fatalf("read pin on %s: %v", sn, err)
		}
		return attempts, seen
	}
	if n, seen := attemptsOf(snA); n != 3 || !seen {
		t.Errorf("device A: attempts = %d seen = %v, want 3 and a fresh last_seen (the literal-1 bug)", n, seen)
	}
	if n, _ := attemptsOf(snB); n != 1 {
		t.Errorf("device B: attempts = %d, want 1: another device's counter must not be shared", n)
	}
}

func TestIngestCheckIn_EarliestWinsNeverClearsTimeOut(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsInA")
	b := testutil.Company(t, pool, "AdmsInB")
	emp := admsEmployee(t, pool, a, "CI-"+uuid.NewString()[:8])
	const day = "2026-03-02"

	checkIn := func(company testutil.Tenant, clock string) int64 {
		n, err := q.IngestCheckIn(ctx, db.IngestCheckInParams{
			CompanyID: admsUUID(t, company.CompanyID), EmployeeID: admsUUID(t, emp.ID),
			Date: admsDate(t, day), TimeIn: admsTime(t, clock),
		})
		if err != nil {
			t.Fatalf("IngestCheckIn %s: %v", clock, err)
		}
		return n
	}

	if n := checkIn(a, "08:00:00"); n != 1 {
		t.Fatalf("first check-in rows = %d, want 1", n)
	}
	cnt, in, out, status, name, dept := attendanceRow(t, pool, emp.ID, day)
	if cnt != 1 || str(in) != "08:00:00" || out != nil || str(status) != "present" {
		t.Fatalf("after first: count=%d in=%s out=%s status=%s", cnt, str(in), str(out), str(status))
	}
	if str(name) != emp.Name || str(dept) != "Ventas" {
		t.Fatalf("denormalized fields: name=%s dept=%s, want %s / Ventas", str(name), str(dept), emp.Name)
	}

	// Earliest check-in wins in both directions.
	checkIn(a, "09:30:00")
	if _, in, _, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(in) != "08:00:00" {
		t.Fatalf("later check-in overwrote time_in: %s", str(in))
	}
	checkIn(a, "07:45:00")
	if _, in, _, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(in) != "07:45:00" {
		t.Fatalf("earlier check-in did not win: %s", str(in))
	}

	// A re-pushed check-in after the check-out keeps time_out and one row.
	if _, err := pool.Exec(ctx, `UPDATE attendance_logs SET time_out = '17:00:00', notes = 'keep', work_type = 3 WHERE employee_id = $1 AND date = $2`, emp.ID, day); err != nil {
		t.Fatal(err)
	}
	checkIn(a, "07:45:00")
	cnt, in, out, _, _, _ = attendanceRow(t, pool, emp.ID, day)
	if cnt != 1 || str(in) != "07:45:00" || str(out) != "17:00:00" {
		t.Fatalf("re-push: count=%d in=%s out=%s, want 1 / 07:45:00 / 17:00:00", cnt, str(in), str(out))
	}
	var notes string
	var wt int
	if err := pool.QueryRow(ctx, `SELECT notes, work_type FROM attendance_logs WHERE employee_id = $1 AND date = $2`, emp.ID, day).Scan(&notes, &wt); err != nil || notes != "keep" || wt != 3 {
		t.Fatalf("notes/work_type touched: notes=%q work_type=%d err=%v", notes, wt, err)
	}

	// absent becomes present; other statuses are preserved.
	for _, tc := range []struct{ from, want string }{{"absent", "present"}, {"late", "late"}} {
		if _, err := pool.Exec(ctx, `UPDATE attendance_logs SET status = $1 WHERE employee_id = $2 AND date = $3`, tc.from, emp.ID, day); err != nil {
			t.Fatal(err)
		}
		checkIn(a, "07:45:00")
		if _, _, _, st, _, _ := attendanceRow(t, pool, emp.ID, day); str(st) != tc.want {
			t.Errorf("status %s after check-in = %s, want %s", tc.from, str(st), tc.want)
		}
	}

	// An employee of A cannot be checked in under company B: no row written.
	other := admsEmployee(t, pool, a, "CX-"+uuid.NewString()[:8])
	n, err := q.IngestCheckIn(ctx, db.IngestCheckInParams{
		CompanyID: admsUUID(t, b.CompanyID), EmployeeID: admsUUID(t, other.ID),
		Date: admsDate(t, day), TimeIn: admsTime(t, "08:00:00"),
	})
	if err != nil || n != 0 {
		t.Fatalf("cross-company check-in: rows=%d err=%v, want 0", n, err)
	}
	if cnt, _, _, _, _, _ := attendanceRow(t, pool, other.ID, day); cnt != 0 {
		t.Fatalf("cross-company check-in wrote %d rows", cnt)
	}
}

func TestIngestCheckOut_LatestWinsAndRequiresCheckIn(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsOutA")
	b := testutil.Company(t, pool, "AdmsOutB")
	emp := admsEmployee(t, pool, a, "CO-"+uuid.NewString()[:8])
	const day = "2026-03-02"

	checkOut := func(company testutil.Tenant, clock string) int64 {
		n, err := q.IngestCheckOut(ctx, db.IngestCheckOutParams{
			CompanyID: admsUUID(t, company.CompanyID), EmployeeID: admsUUID(t, emp.ID),
			Date: admsDate(t, day), TimeOut: admsTime(t, clock),
		})
		if err != nil {
			t.Fatalf("IngestCheckOut %s: %v", clock, err)
		}
		return n
	}

	if n := checkOut(a, "17:00:00"); n != 0 {
		t.Fatalf("check-out without check-in updated %d rows, want 0", n)
	}
	if cnt, _, _, _, _, _ := attendanceRow(t, pool, emp.ID, day); cnt != 0 {
		t.Fatalf("check-out without check-in created %d rows", cnt)
	}

	if _, err := q.IngestCheckIn(ctx, db.IngestCheckInParams{
		CompanyID: admsUUID(t, a.CompanyID), EmployeeID: admsUUID(t, emp.ID),
		Date: admsDate(t, day), TimeIn: admsTime(t, "08:00:00"),
	}); err != nil {
		t.Fatal(err)
	}

	if n := checkOut(a, "17:00:00"); n != 1 {
		t.Fatalf("check-out rows = %d, want 1", n)
	}
	if _, in, out, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(in) != "08:00:00" || str(out) != "17:00:00" {
		t.Fatalf("in=%s out=%s, want 08:00:00 / 17:00:00", str(in), str(out))
	}

	// Latest check-out wins; an older one never regresses it.
	checkOut(a, "18:30:00")
	if _, _, out, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(out) != "18:30:00" {
		t.Fatalf("later check-out did not win: %s", str(out))
	}
	checkOut(a, "16:00:00")
	if _, _, out, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(out) != "18:30:00" {
		t.Fatalf("earlier check-out regressed time_out: %s", str(out))
	}

	// Wrong company touches nothing.
	if n := checkOut(b, "23:00:00"); n != 0 {
		t.Fatalf("cross-company check-out updated %d rows", n)
	}
	if _, _, out, _, _, _ := attendanceRow(t, pool, emp.ID, day); str(out) != "18:30:00" {
		t.Fatalf("cross-company check-out changed time_out: %s", str(out))
	}
}

// CO-S5a-4: a row without time_in (absent or manually created) is not a
// check-in; a check-out must not turn it into a punched day.
func TestIngestCheckOut_RequiresTimeIn(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	a := testutil.Company(t, pool, "AdmsOutNoIn")
	emp := admsEmployee(t, pool, a, "NI-"+uuid.NewString()[:8])
	const day = "2026-03-03"

	if _, err := pool.Exec(ctx,
		`INSERT INTO attendance_logs (company_id, employee_id, employee_name, date, status)
		 VALUES ($1, $2, $3, $4, 'absent')`, a.CompanyID, emp.ID, emp.Name, day); err != nil {
		t.Fatalf("seed row without time_in: %v", err)
	}
	checkOut := func(clock string) int64 {
		n, err := q.IngestCheckOut(ctx, db.IngestCheckOutParams{
			CompanyID: admsUUID(t, a.CompanyID), EmployeeID: admsUUID(t, emp.ID),
			Date: admsDate(t, day), TimeOut: admsTime(t, clock),
		})
		if err != nil {
			t.Fatalf("IngestCheckOut: %v", err)
		}
		return n
	}

	if n := checkOut("17:00:00"); n != 0 {
		t.Fatalf("check-out on a row without time_in updated %d rows, want 0 (no_checkin)", n)
	}
	if cnt, in, out, st, _, _ := attendanceRow(t, pool, emp.ID, day); cnt != 1 || in != nil || out != nil || str(st) != "absent" {
		t.Fatalf("row changed: count=%d in=%s out=%s status=%s", cnt, str(in), str(out), str(st))
	}

	// Triangulation: once a check-in exists the same check-out is accepted.
	if _, err := pool.Exec(ctx, `UPDATE attendance_logs SET time_in = '08:00:00' WHERE employee_id = $1 AND date = $2`, emp.ID, day); err != nil {
		t.Fatal(err)
	}
	if n := checkOut("17:00:00"); n != 1 {
		t.Fatalf("check-out after a check-in updated %d rows, want 1", n)
	}
}

func TestAddIngestRejections_AccumulatesPerSerialAndReason(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	q := db.New(pool)
	sn := admsSerial(t, pool)
	other := admsSerial(t, pool)

	add := func(serial, reason string, n int64, pin string) {
		t.Helper()
		if err := q.AddIngestRejections(ctx, db.AddIngestRejectionsParams{
			SerialNumber: serial, Reason: reason, Count: n, LastPin: pgtype.Text{String: pin, Valid: pin != ""},
		}); err != nil {
			t.Fatalf("AddIngestRejections %s/%s: %v", serial, reason, err)
		}
	}
	read := func(serial, reason string) (int64, string) {
		t.Helper()
		var n int64
		var pin *string
		if err := pool.QueryRow(ctx,
			`SELECT count, last_pin FROM zkteco_ingest_rejections WHERE serial_number = $1 AND reason = $2`, serial, reason,
		).Scan(&n, &pin); err != nil {
			t.Fatalf("read counter %s/%s: %v", serial, reason, err)
		}
		return n, str(pin)
	}

	add(sn, "malformed", 2, "1001")
	add(sn, "malformed", 3, "1002")
	add(sn, "no_checkin", 1, "")
	add(other, "malformed", 4, "9")

	if n, pin := read(sn, "malformed"); n != 5 || pin != "1002" {
		t.Errorf("malformed counter = %d last_pin=%s, want 5 / 1002", n, pin)
	}
	if n, _ := read(sn, "no_checkin"); n != 1 {
		t.Errorf("no_checkin counter = %d, want 1", n)
	}
	if n, _ := read(other, "malformed"); n != 4 {
		t.Errorf("other device counter = %d, want 4 (counters are per serial)", n)
	}

	// An empty last_pin must not erase the previous one.
	add(sn, "malformed", 1, "")
	if n, pin := read(sn, "malformed"); n != 6 || pin != "1002" {
		t.Errorf("after pinless add: count=%d last_pin=%s, want 6 / 1002", n, pin)
	}
}
