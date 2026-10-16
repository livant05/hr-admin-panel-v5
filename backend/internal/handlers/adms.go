package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// ZKTeco ADMS push endpoints (ING-1, ING-2, ING-3, ING-5, ING-8, ING-9).
//
// PROVISIONAL: the paths, the response bodies, the option text and the
// /iclock/devicecmd route mirror the retired Supabase Edge Function and have
// not been checked against a real device (OPS-1). The request caps and the
// 600/min rate limit are provisional too. What is binding is the behavior:
// tenant-scoped resolution, claimed-only attendance, per-reason rejection
// counters, idempotency and one transaction per request.
//
// The routes are public (a device sends no JWT) and live on the outer mux, so
// they sit outside the /api/ subtree and cannot weaken its auth.

// maxADMSBody caps one request body (ING-8). nginx enforces the same figure
// with client_max_body_size; this is the backend's own bound.
const maxADMSBody = 2 << 20

// limiterADMS names the ADMS budget in rate-limit log lines.
const limiterADMS = "adms"

// admsSerialPattern is the accepted device serial number.
var admsSerialPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// attlogTable is the only ADMS table that carries punches. Anything else
// (OPERLOG, BIODATA, ...) is acknowledged without processing: the Edge
// Function parsed tab lines of any table and would have read OPERLOG lines as
// punches.
const attlogTable = "ATTLOG"

// admsRoutes registers the device endpoints on the outer mux, each behind the
// ADMS limiter.
func (a *API) admsRoutes(mux *http.ServeMux) {
	limited := func(h http.HandlerFunc) http.Handler { return a.rateLimited(limiterADMS, a.ADMSLimiter, h) }
	mux.Handle("GET /iclock/cdata", limited(a.ADMSOptions))
	mux.Handle("POST /iclock/cdata", limited(a.ADMSData))
	mux.Handle("GET /iclock/getrequest", limited(a.ADMSGetRequest))
	mux.Handle("POST /iclock/devicecmd", limited(a.ADMSDeviceCmd))
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// admsSerial reads and validates the device serial (SN, or sn). On failure it
// writes the 400 itself: nothing may be written for an invalid serial.
func admsSerial(w http.ResponseWriter, r *http.Request) (string, bool) {
	q := r.URL.Query()
	sn := q.Get("SN")
	if sn == "" {
		sn = q.Get("sn")
	}
	if !admsSerialPattern.MatchString(sn) {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid device serial")
		return "", false
	}
	return sn, true
}

// admsOptionText is the registration reply that tells the device what to push.
// PROVISIONAL (parity with the Edge Function, including ATTLOGStamp, whose
// semantics need a real-device check).
func admsOptionText(now time.Time) string {
	stamp := strconv.FormatInt(now.Unix(), 10)
	return strings.Join([]string{
		"GET OPTION",
		"ATTLOGStamp=" + stamp,
		"OPERATORStamp=None",
		"ATTPHOTOStamp=None",
		"ErrorDelay=30",
		"Delay=10",
		"TransTimes=00:00;14:05",
		"TransInterval=1",
		"TransFlag=TransData AttLog",
		"TimeLimit=0",
		"BioDataStamp=" + stamp,
		"OPTIONINFO Size=0",
	}, "\n") + "\n"
}

// GET /iclock/cdata — device registration / heartbeat. A registered serial is
// refreshed; an unknown one is refused (403) and nothing is written.
// company_id is never touched (ING-2).
func (a *API) ADMSOptions(w http.ResponseWriter, r *http.Request) {
	sn, ok := admsSerial(w, r)
	if !ok {
		return
	}
	if _, ok := a.admsTouch(r.Context(), w, a.Queries, sn); !ok {
		return
	}
	writeText(w, http.StatusOK, admsOptionText(time.Now()))
}

// GET /iclock/getrequest — command polling. No command is ever pending; the
// poll doubles as the heartbeat (a device polls about every 10 seconds).
func (a *API) ADMSGetRequest(w http.ResponseWriter, r *http.Request) {
	sn, ok := admsSerial(w, r)
	if !ok {
		return
	}
	if _, ok := a.admsTouch(r.Context(), w, a.Queries, sn); !ok {
		return
	}
	writeText(w, http.StatusOK, "OK")
}

// POST /iclock/devicecmd — the device's reply to a command. PROVISIONAL: no
// command is ever issued, so the reply of a registered device is validated,
// drained within the body cap and acknowledged; the only write is the
// heartbeat refresh. An unknown serial is refused (403).
func (a *API) ADMSDeviceCmd(w http.ResponseWriter, r *http.Request) {
	sn, ok := admsSerial(w, r)
	if !ok {
		return
	}
	if _, ok := a.admsTouch(r.Context(), w, a.Queries, sn); !ok {
		return
	}
	if _, ok := readADMSBody(w, r); !ok {
		return
	}
	writeText(w, http.StatusOK, "OK")
}

// admsTouch refreshes the heartbeat of a registered device and returns its
// row. An unknown serial matches no row: it is answered 403 and nothing is
// written, so a stranger can not fill the device list (runbook B2/B3). It is
// not logged per request, so the refusals can not flood the log either.
func (a *API) admsTouch(ctx context.Context, w http.ResponseWriter, q *db.Queries, sn string) (db.TouchDeviceHeartbeatRow, bool) {
	device, err := q.TouchDeviceHeartbeat(ctx, sn)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErrCode(w, http.StatusForbidden, codeForbidden, "unknown device")
		return device, false
	}
	if err != nil {
		a.admsFail(w, "adms heartbeat", sn, err)
		return device, false
	}
	return device, true
}

// admsFail logs a server-side failure and answers 500 in the A2 envelope. The
// device only reads the status; a 5xx makes it retry, which idempotency makes
// safe. The log carries the serial and the error, never a request body.
func (a *API) admsFail(w http.ResponseWriter, op, sn string, err error) {
	a.Log.Error(op, "serial", sn, "err", err)
	writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
}

// readADMSBody reads the request body within maxADMSBody. Over the cap it
// answers 413 (A2 envelope) and any other read error answers 400; in both
// cases it has written the response and nothing has touched the database.
func readADMSBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxADMSBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErrCode(w, http.StatusRequestEntityTooLarge, codeBadRequest, "request body too large")
			return "", false
		}
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "unreadable request body")
		return "", false
	}
	return string(b), true
}

// POST /iclock/cdata — a data push. Everything is validated and parsed before
// the transaction opens, so a refused request (bad serial, body or line cap)
// writes nothing at all. All database writes of an accepted request happen in
// ONE transaction: any error rolls the whole request back and answers 500.
func (a *API) ADMSData(w http.ResponseWriter, r *http.Request) {
	sn, ok := admsSerial(w, r)
	if !ok {
		return
	}
	body, ok := readADMSBody(w, r)
	if !ok {
		return
	}

	isATTLOG := strings.EqualFold(r.URL.Query().Get("table"), attlogTable)
	var (
		punches []punch
		rejects []reject
	)
	if isATTLOG {
		var tooMany bool
		punches, rejects, tooMany = parseATTLOG(body)
		if tooMany {
			writeErrCode(w, http.StatusRequestEntityTooLarge, codeBadRequest, "too many lines in one request")
			return
		}
	}

	ctx := r.Context()
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		a.admsFail(w, "adms begin", sn, err)
		return
	}
	defer tx.Rollback(ctx) // no-op after Commit
	q := a.Queries.WithTx(tx)

	device, ok := a.admsTouch(ctx, w, q, sn)
	if !ok {
		return
	}
	if !isATTLOG {
		if err := tx.Commit(ctx); err != nil {
			a.admsFail(w, "adms commit", sn, err)
			return
		}
		writeText(w, http.StatusOK, "OK")
		return
	}

	res, err := ingestBatch(ctx, q, sn, device.CompanyID, punches, rejects)
	if err != nil {
		a.admsFail(w, "adms ingest", sn, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.admsFail(w, "adms commit", sn, err)
		return
	}

	// One structured line per accepted batch. It carries counts only: never a
	// PIN or cedula. tenant_mismatch is the cross-tenant lookup count that is
	// deliberately NOT persisted as its own reason (CO-S5a-1).
	a.Log.Info("adms_batch",
		"serial", sn,
		"processed", res.processed,
		"rejected_by_reason", res.counts(),
		"tenant_mismatch", res.tenantMismatch,
	)
	writeText(w, http.StatusOK, "OK: "+strconv.Itoa(res.processed))
}

// rejectionTally accumulates rejected punches per reason for one batch, so each
// (serial, reason) is written once per request.
type rejectionTally struct {
	count   map[string]int64
	lastPIN map[string]string
}

func newTally() *rejectionTally {
	return &rejectionTally{count: map[string]int64{}, lastPIN: map[string]string{}}
}

func (t *rejectionTally) add(reason, pin string) {
	t.count[reason]++
	if pin != "" {
		t.lastPIN[reason] = pin
	}
}

// batchResult is the outcome of ingestBatch.
type batchResult struct {
	processed int
	tally     *rejectionTally
	// tenantMismatch counts distinct cedulas that exist only in
	// another company. Log only; see ADMSData.
	tenantMismatch int
}

func (b batchResult) counts() map[string]int64 { return b.tally.count }

// resolution is the outcome of looking a PIN up inside one company.
type resolution struct {
	employeeID pgtype.UUID
	found      bool // exactly one employee
	ambiguous  bool // more than one employee
}

// ingestBatch applies one parsed ATTLOG batch to the database through q, which
// must be bound to the request transaction. An unclaimed device (companyID not
// valid) records every parsed punch as device_unclaimed and writes no
// attendance and no unknown pin (ING-3).
func ingestBatch(ctx context.Context, q *db.Queries, sn string, companyID pgtype.UUID, punches []punch, rejects []reject) (batchResult, error) {
	res := batchResult{tally: newTally()}
	for _, rej := range rejects {
		res.tally.add(rej.Reason, rej.PIN)
	}

	if !companyID.Valid {
		for _, p := range punches {
			res.tally.add(reasonDeviceUnclaimed, p.PIN)
		}
		return res, flushRejections(ctx, q, sn, res.tally)
	}

	resolved := map[string]resolution{}
	resolve := func(pin string) (resolution, error) {
		if r, ok := resolved[pin]; ok {
			return r, nil
		}
		ids, err := q.ResolveEmployeesByCedula(ctx, db.ResolveEmployeesByCedulaParams{CompanyID: companyID, Cedula: pin})
		if err != nil {
			return resolution{}, err
		}
		r := resolution{ambiguous: len(ids) > 1, found: len(ids) == 1}
		if r.found {
			r.employeeID = ids[0]
		}
		if len(ids) == 0 {
			elsewhere, err := q.CedulaExistsElsewhere(ctx, db.CedulaExistsElsewhereParams{Cedula: pin, CompanyID: companyID})
			if err != nil {
				return resolution{}, err
			}
			if elsewhere {
				res.tenantMismatch++
			}
		}
		resolved[pin] = r
		return r, nil
	}

	// Check-ins first, then check-outs: an in/out pair inside one batch never
	// depends on line order.
	for _, kind := range []punchKind{punchIn, punchOut} {
		for _, p := range punches {
			if p.Kind != kind {
				continue
			}
			r, err := resolve(p.PIN)
			if err != nil {
				return res, err
			}
			switch {
			case r.ambiguous:
				res.tally.add(reasonAmbiguous, p.PIN)
				continue
			case !r.found:
				// A cedula that exists only in another company lands here too,
				// exactly like one that exists nowhere (CO-S5a-1).
				res.tally.add(reasonNotFound, p.PIN)
				if err := q.UpsertUnknownPin(ctx, db.UpsertUnknownPinParams{Pin: p.PIN, SerialNumber: pgtype.Text{String: sn, Valid: true}}); err != nil {
					return res, err
				}
				continue
			}

			date := pgtype.Date{Time: time.Date(p.At.Year(), p.At.Month(), p.At.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
			clock := pgtype.Time{Microseconds: int64(p.At.Hour()*3600+p.At.Minute()*60+p.At.Second()) * 1_000_000, Valid: true}
			var rows int64
			if kind == punchIn {
				rows, err = q.IngestCheckIn(ctx, db.IngestCheckInParams{CompanyID: companyID, EmployeeID: r.employeeID, Date: date, TimeIn: clock})
			} else {
				rows, err = q.IngestCheckOut(ctx, db.IngestCheckOutParams{CompanyID: companyID, EmployeeID: r.employeeID, Date: date, TimeOut: clock})
			}
			if err != nil {
				return res, err
			}
			switch {
			case rows > 0:
				res.processed++
			case kind == punchOut:
				res.tally.add(reasonNoCheckin, p.PIN)
			default:
				// The employee vanished between the lookup and the write.
				res.tally.add(reasonNotFound, p.PIN)
			}
		}
	}
	return res, flushRejections(ctx, q, sn, res.tally)
}

// flushRejections writes one counter upsert per reason that occurred, in the
// fixed reason order so the write sequence is deterministic.
func flushRejections(ctx context.Context, q *db.Queries, sn string, t *rejectionTally) error {
	for _, reason := range rejectionReasons {
		n := t.count[reason]
		if n == 0 {
			continue
		}
		pin := t.lastPIN[reason]
		if err := q.AddIngestRejections(ctx, db.AddIngestRejectionsParams{
			SerialNumber: sn,
			Reason:       reason,
			Count:        n,
			LastPin:      pgtype.Text{String: pin, Valid: pin != ""},
		}); err != nil {
			return err
		}
	}
	return nil
}
