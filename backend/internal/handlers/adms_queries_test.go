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
		_, _ = pool.Exec(ctx, `DELETE FROM zkteco_unknown_pins WHERE serial_number = $1`, sn)
	})
	return sn
}

// attendanceSnapshot is one attendance_logs row as read straight from the
// table (never through the code under test). Count is the number of rows for
// the employee and date; the other fields are nil when Count is 0 or the
// column is NULL.
type attendanceSnapshot struct {
	Count                                     int
	TimeIn, TimeOut, Status, Name, Department *string
}

func readAttendance(t *testing.T, pool *pgxpool.Pool, empID, date string) attendanceSnapshot {
	t.Helper()
	var snap attendanceSnapshot
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM attendance_logs WHERE employee_id = $1 AND date = $2`, empID, date).Scan(&snap.Count); err != nil {
		t.Fatalf("count attendance: %v", err)
	}
	if snap.Count == 0 {
		return snap
	}
	if err := pool.QueryRow(ctx,
		`SELECT time_in::text, time_out::text, status, employee_name, department
		 FROM attendance_logs WHERE employee_id = $1 AND date = $2`, empID, date,
	).Scan(&snap.TimeIn, &snap.TimeOut, &snap.Status, &snap.Name, &snap.Department); err != nil {
		t.Fatalf("read attendance: %v", err)
	}
	return snap
}

// orNil renders an optional column for comparisons and failure messages.
func orNil(p *string) string {
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
	got := readAttendance(t, pool, emp.ID, day)
	if got.Count != 1 || orNil(got.TimeIn) != "08:00:00" || got.TimeOut != nil || orNil(got.Status) != "present" {
		t.Fatalf("after first: count=%d in=%s out=%s status=%s", got.Count, orNil(got.TimeIn), orNil(got.TimeOut), orNil(got.Status))
	}
	if orNil(got.Name) != emp.Name || orNil(got.Department) != "Ventas" {
		t.Fatalf("denormalized fields: name=%s dept=%s, want %s / Ventas", orNil(got.Name), orNil(got.Department), emp.Name)
	}

	// Earliest check-in wins in both directions.
	checkIn(a, "09:30:00")
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeIn) != "08:00:00" {
		t.Fatalf("later check-in overwrote time_in: %s", orNil(got.TimeIn))
	}
	checkIn(a, "07:45:00")
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeIn) != "07:45:00" {
		t.Fatalf("earlier check-in did not win: %s", orNil(got.TimeIn))
	}

	// A re-pushed check-in after the check-out keeps time_out and one row.
	if _, err := pool.Exec(ctx, `UPDATE attendance_logs SET time_out = '17:00:00', notes = 'keep', work_type = 3 WHERE employee_id = $1 AND date = $2`, emp.ID, day); err != nil {
		t.Fatal(err)
	}
	checkIn(a, "07:45:00")
	got = readAttendance(t, pool, emp.ID, day)
	if got.Count != 1 || orNil(got.TimeIn) != "07:45:00" || orNil(got.TimeOut) != "17:00:00" {
		t.Fatalf("re-push: count=%d in=%s out=%s, want 1 / 07:45:00 / 17:00:00", got.Count, orNil(got.TimeIn), orNil(got.TimeOut))
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
		if got := readAttendance(t, pool, emp.ID, day); orNil(got.Status) != tc.want {
			t.Errorf("status %s after check-in = %s, want %s", tc.from, orNil(got.Status), tc.want)
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
	if got := readAttendance(t, pool, other.ID, day); got.Count != 0 {
		t.Fatalf("cross-company check-in wrote %d rows", got.Count)
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
	if got := readAttendance(t, pool, emp.ID, day); got.Count != 0 {
		t.Fatalf("check-out without check-in created %d rows", got.Count)
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
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeIn) != "08:00:00" || orNil(got.TimeOut) != "17:00:00" {
		t.Fatalf("in=%s out=%s, want 08:00:00 / 17:00:00", orNil(got.TimeIn), orNil(got.TimeOut))
	}

	// Latest check-out wins; an older one never regresses it.
	checkOut(a, "18:30:00")
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeOut) != "18:30:00" {
		t.Fatalf("later check-out did not win: %s", orNil(got.TimeOut))
	}
	checkOut(a, "16:00:00")
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeOut) != "18:30:00" {
		t.Fatalf("earlier check-out regressed time_out: %s", orNil(got.TimeOut))
	}

	// Wrong company touches nothing.
	if n := checkOut(b, "23:00:00"); n != 0 {
		t.Fatalf("cross-company check-out updated %d rows", n)
	}
	if got := readAttendance(t, pool, emp.ID, day); orNil(got.TimeOut) != "18:30:00" {
		t.Fatalf("cross-company check-out changed time_out: %s", orNil(got.TimeOut))
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
	if got := readAttendance(t, pool, emp.ID, day); got.Count != 1 || got.TimeIn != nil || got.TimeOut != nil || orNil(got.Status) != "absent" {
		t.Fatalf("row changed: count=%d in=%s out=%s status=%s", got.Count, orNil(got.TimeIn), orNil(got.TimeOut), orNil(got.Status))
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
		return n, orNil(pin)
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
