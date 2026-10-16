package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// maxZktecoLabel bounds the two writable device labels (name, location).
const maxZktecoLabel = 200

// zktecoDevicePatch is the writable surface of a device: name and location
// ONLY. serial_number, company_id, status, firmware and the timestamps belong
// to the device protocol or to ownership, so DisallowUnknownFields turns a
// body carrying any of them into a 400 (ZKM-2, A1 rule 2).
type zktecoDevicePatch struct {
	Name     pgtype.Text `json:"name"`
	Location pgtype.Text `json:"location"`
}

// GET /api/zkteco_devices — the caller's devices, newest heartbeat first
// (never-seen last). Unclaimed devices (company_id NULL) never match, so they
// are invisible here (ZKM-1). Any authenticated role. Filter: id.
func (a *API) ListZktecoDevices(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id")
	if !ok {
		return
	}
	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListZktecoDevices(r.Context(), db.ListZktecoDevicesParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		ID:        id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list zkteco devices")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// zktecoLabel validates one optional label. An explicit null (or an absent
// key) is not validated; a present value is trimmed and must be 1-200 runes.
func zktecoLabel(v pgtype.Text) (pgtype.Text, bool) {
	if !v.Valid {
		return pgtype.Text{}, true
	}
	s := strings.TrimSpace(v.String)
	if s == "" || utf8.RuneCountInString(s) > maxZktecoLabel {
		return pgtype.Text{}, false
	}
	return pgtype.Text{String: s, Valid: true}, true
}

// PATCH /api/zkteco_devices/{id} — rename or relocate one of the caller's
// devices; returns a one-element array. A device of another company, an
// unclaimed device and a missing id are all 404 (the UPDATE is scoped by id AND
// company, so the guard and the write are one statement). Any authenticated
// role (ZKM-2).
func (a *API) UpdateZktecoDevice(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req zktecoDevicePatch
	sent, ok := decodeStrictPatch(w, r, &req)
	if !ok {
		return
	}

	fields := map[string]string{}
	name, nameOK := zktecoLabel(req.Name)
	if !nameOK {
		fields["name"] = "must be 1-200 characters or null"
	}
	location, locationOK := zktecoLabel(req.Location)
	if !locationOK {
		fields["location"] = "must be 1-200 characters or null"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	_, setName := sent["name"]
	_, setLocation := sent["location"]

	row, err := a.Queries.UpdateZktecoDevice(r.Context(), db.UpdateZktecoDeviceParams{
		CompanyID:   companyID, // $1 — from ctx, never from req
		ID:          id,
		SetName:     setName,
		Name:        name,
		SetLocation: setLocation,
		Location:    location,
	})
	if err != nil {
		a.writeDBErr(w, err, "update zkteco device")
		return
	}
	writeRows(w, http.StatusOK, []db.ZktecoDevice{row})
}

// zktecoDeviceRegister is the body of a registration. No company_id, status
// or last_seen: DisallowUnknownFields turns a body carrying them into a 400,
// and the company always comes from the token.
type zktecoDeviceRegister struct {
	SerialNumber string      `json:"serial_number"`
	Name         pgtype.Text `json:"name"`
	Location     pgtype.Text `json:"location"`
}

// POST /api/zkteco_devices — register a device for the caller's company before
// it is pointed at the server; ADMS refuses any serial without a row (runbook
// B2/B3). Admin only. 201 with a one-element array. A serial that already has a
// row, registered by any company or imported unclaimed, is 409: an unclaimed
// one is taken with the claim endpoint instead.
func (a *API) RegisterZktecoDevice(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	var req zktecoDeviceRegister
	if !decodeStrictBody(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if !admsSerialPattern.MatchString(req.SerialNumber) {
		fields["serial_number"] = "required, 1-64 letters, digits, _ or -"
	}
	name, nameOK := zktecoLabel(req.Name)
	if !nameOK {
		fields["name"] = "must be 1-200 characters or null"
	}
	location, locationOK := zktecoLabel(req.Location)
	if !locationOK {
		fields["location"] = "must be 1-200 characters or null"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := a.registerDevice(r.Context(), db.RegisterZktecoDeviceParams{
		CompanyID:    companyID, // $1 — from ctx, never from req
		SerialNumber: req.SerialNumber,
		Name:         name,
		Location:     location,
	})
	if err != nil {
		a.writeZktecoRegisterErr(w, err)
		return
	}
	writeRows(w, http.StatusCreated, []db.ZktecoDevice{row})
}

// writeZktecoRegisterErr is writeDBErr plus the one-row-per-serial rule:
// serial_number is UNIQUE across companies, so a 409 confirms only that the
// serial is taken (the owner is never disclosed); the panel shows the Spanish
// message as is.
func (a *API) writeZktecoRegisterErr(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		writeErrCode(w, http.StatusConflict, codeConflict, "Ese número de serie ya está registrado")
		return
	}
	a.writeDBErr(w, err, "register zkteco device")
}

// registerDevice inserts the device and, in the same transaction, deletes any
// rejection counters and unknown PINs left under its serial, so the company
// never inherits data collected before it registered the device (the same rule
// as a claim, CO-S5a-2).
func (a *API) registerDevice(ctx context.Context, p db.RegisterZktecoDeviceParams) (db.ZktecoDevice, error) {
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return db.ZktecoDevice{}, err
	}
	defer tx.Rollback(ctx) // no-op after Commit
	q := a.Queries.WithTx(tx)

	row, err := q.RegisterZktecoDevice(ctx, p)
	if err != nil {
		return db.ZktecoDevice{}, err
	}
	if err := q.DeleteIngestRejectionsBySerial(ctx, row.SerialNumber); err != nil {
		return db.ZktecoDevice{}, err
	}
	if err := q.DeleteUnknownPinsBySerial(ctx, pgtype.Text{String: row.SerialNumber, Valid: true}); err != nil {
		return db.ZktecoDevice{}, err
	}
	return row, tx.Commit(ctx)
}

// GET /api/zkteco_devices/unclaimed — the devices that have no company: loaded
// by the importer, or self-registered before devices were pre-registered.
// Admin only (ZKM-3, XC-4). The pool is
// global by nature (an unclaimed device has no tenant), which is why it is
// gated by role instead of scoped by company.
func (a *API) ListUnclaimedZktecoDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.tenant(w, r); !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	p, ok := parseList(w, r)
	if !ok {
		return
	}

	rows, err := a.Queries.ListUnclaimedZktecoDevices(r.Context(), db.ListUnclaimedZktecoDevicesParams{
		Limit:  p.Limit,
		Offset: p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list unclaimed zkteco devices")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// POST /api/zkteco_devices/{id}/claim — assign an unclaimed device to the
// caller's company (ZKM-4). Admin only. The company comes from the token; the
// body, when present, must be an empty object, so a company_id (or anything
// else) is a 400 instead of being silently dropped.
//
// The ownership check and the write are the single UPDATE ... WHERE
// company_id IS NULL, so concurrent claims cannot both win. A device that is
// missing, claimed by another company, or already claimed by this one all
// answer 404: the response never says who owns a device.
func (a *API) ClaimZktecoDevice(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req struct{}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<10))
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
		// Only an object is a valid body: JSON null would otherwise decode into
		// the empty struct without error.
		if trimmed[0] != '{' {
			writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
			return
		}
		if _, err := dec.Token(); err != io.EOF {
			writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
			return
		}
	}

	row, err := a.claimDevice(r.Context(), companyID, id)
	if err != nil {
		a.writeDBErr(w, err, "claim zkteco device")
		return
	}
	writeRows(w, http.StatusOK, []db.ZktecoDevice{row})
}

// claimDevice claims the device and, in the same transaction, deletes the
// rejection counters and unknown PINs earlier punches left under its serial,
// so the new owner never inherits data collected before the claim (CO-S5a-2).
// A claim that matches no row (pgx.ErrNoRows) rolls back and deletes nothing.
func (a *API) claimDevice(ctx context.Context, companyID, id pgtype.UUID) (db.ZktecoDevice, error) {
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return db.ZktecoDevice{}, err
	}
	defer tx.Rollback(ctx) // no-op after Commit
	q := a.Queries.WithTx(tx)

	row, err := q.ClaimZktecoDevice(ctx, db.ClaimZktecoDeviceParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		ID:        id,
	})
	if err != nil {
		return db.ZktecoDevice{}, err
	}
	if err := q.DeleteIngestRejectionsBySerial(ctx, row.SerialNumber); err != nil {
		return db.ZktecoDevice{}, err
	}
	if err := q.DeleteUnknownPinsBySerial(ctx, pgtype.Text{String: row.SerialNumber, Valid: true}); err != nil {
		return db.ZktecoDevice{}, err
	}
	return row, tx.Commit(ctx)
}
