package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/zkteco_unknown_pins — PINs punched on a device that matched no
// employee, with their attempts counter. Admin only (ZKM-5, XC-4).
//
// zkteco_unknown_pins has NO company_id: an unresolved PIN has no matched
// employee, so there is no company to infer. This is the one sanctioned
// exception to the tenant rule (A1 rule 4), which is exactly why the route
// is admin-gated instead of tenant-scoped. Filter: resolved (true|false).
func (a *API) ListZktecoUnknownPins(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.tenant(w, r); !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	p, ok := parseList(w, r, "resolved")
	if !ok {
		return
	}
	var resolved pgtype.Bool
	if v, present := p.Filters["resolved"]; present {
		switch v {
		case "true":
			resolved = pgtype.Bool{Bool: true, Valid: true}
		case "false":
			resolved = pgtype.Bool{Bool: false, Valid: true}
		default:
			// An empty ?resolved= is rejected too, never read as "no filter".
			writeFieldErr(w, map[string]string{"resolved": "must be true or false"})
			return
		}
	}

	rows, err := a.Queries.ListZktecoUnknownPins(r.Context(), db.ListZktecoUnknownPinsParams{
		Resolved: resolved,
		Limit:    p.Limit,
		Offset:   p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list zkteco unknown pins")
		return
	}
	writeRows(w, http.StatusOK, rows)
}
