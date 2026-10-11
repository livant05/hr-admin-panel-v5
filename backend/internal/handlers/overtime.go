package handlers

import (
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/livant05/rrhh-go/internal/overtime"
)

// The statutory rate table, default type and hour cap live in
// internal/overtime so the import tool computes with the same constants.
const (
	// defaultOvertimeType matches the column default ('regular') the DDL already
	// declares, applied here because CreateOvertimeLog lists columns explicitly
	// rather than relying on the table default (same pattern as
	// leaveRequestDefaultType).
	defaultOvertimeType = overtime.DefaultType

	// maxOvertimeHoursPerRecord enforces Art. 36 num. 4 server-side (design Q6,
	// spec "Hours over the daily cap rejected").
	maxOvertimeHoursPerRecord = overtime.MaxHoursPerRecord
)

// overtimeRateNumeric resolves an overtime type to its statutory rate as an
// exact pgtype.Numeric built from the literal decimal string in the overtime
// table (never a float), or ok=false for an unrecognized type (design Q6: "Go
// rejects an unknown type with validation_failed" -- saveOT's own
// OT_RATES[type]||1.25 fallback silently underpays on a typo'd type).
func overtimeRateNumeric(otType string) (pgtype.Numeric, bool) {
	raw, known := overtime.Rate(otType)
	if !known {
		return pgtype.Numeric{}, false
	}
	var n pgtype.Numeric
	if err := n.Scan(raw); err != nil {
		// Unreachable: every rate is a fixed, valid decimal literal.
		return pgtype.Numeric{}, false
	}
	return n, true
}
