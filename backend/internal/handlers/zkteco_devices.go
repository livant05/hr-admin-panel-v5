package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

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
	sent, ok := decodeRecruitmentPatch(w, r, &req)
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

// GET /api/zkteco_devices/unclaimed — the pool of devices that heartbeated
// without belonging to any company. Admin only (ZKM-3, XC-4). The pool is
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

	row, err := a.Queries.ClaimZktecoDevice(r.Context(), db.ClaimZktecoDeviceParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "claim zkteco device")
		return
	}
	writeRows(w, http.StatusOK, []db.ZktecoDevice{row})
}
