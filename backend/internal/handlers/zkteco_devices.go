package handlers

import (
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
