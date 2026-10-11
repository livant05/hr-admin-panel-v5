package handlers

import (
	"net/http"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/companies — any authenticated role. Returns a one-element array
// with the caller's own company (design D7). The id filter exists only for
// adapter compatibility: another company's id yields [] and no query ever
// reads a company other than the token's.
func (a *API) ListCompanies(w http.ResponseWriter, r *http.Request) {
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
	if id.Valid && id != companyID {
		writeRows(w, http.StatusOK, []db.Company{})
		return
	}

	company, err := a.Queries.GetCompanyByID(r.Context(), companyID) // from ctx, never from the request
	if err != nil {
		a.writeDBErr(w, err, "list companies")
		return
	}
	writeRows(w, http.StatusOK, []db.Company{company})
}

// GET /api/companies/{id} — the caller's own company, otherwise 404 (never
// 403: that would disclose that another company exists).
func (a *API) GetCompany(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil || id != companyID {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	company, err := a.Queries.GetCompanyByID(r.Context(), companyID)
	if err != nil {
		a.writeDBErr(w, err, "get company")
		return
	}
	writeJSON(w, http.StatusOK, company)
}
