package handlers

import "github.com/jackc/pgx/v5/pgtype"

// otRates mirrors OT_RATES (hr_admin_panel.html:1857) -- statutory Panama
// Código de Trabajo overtime recargos. Design Q6: a package-level map, not a
// config table and not tenant-configurable -- these are statutory rates, and
// a tenant that "configures" them differently would be violating labor law,
// not customizing a feature. Values are literal decimal strings (never a
// float) so overtimeRateNumeric can build an exact pgtype.Numeric from them.
var otRates = map[string]string{
	"regular": "1.25", // diurna +25%
	"mixed":   "1.50", // mixta +50%
	"night":   "1.75", // nocturna +75%
	"sunday":  "1.50", // descanso +50%
	"holiday": "2.50", // festivo +150%
}

// defaultOvertimeType matches the column default ('regular') the DDL already
// declares, applied here because CreateOvertimeLog lists columns explicitly
// rather than relying on the table default (same pattern as
// leaveRequestDefaultType).
const defaultOvertimeType = "regular"

// maxOvertimeHoursPerRecord enforces Art. 36 num. 4 server-side (design Q6,
// spec "Hours over the daily cap rejected") -- saveOT (hr_admin_panel.html:3371)
// enforces the same cap client-side only today.
const maxOvertimeHoursPerRecord = 3

// overtimeRateNumeric resolves an overtime type to its statutory rate as an
// exact pgtype.Numeric built from the literal decimal string in otRates
// (never a float), or ok=false for an unrecognized type (design Q6: "Go
// rejects an unknown type with validation_failed" -- saveOT's own
// OT_RATES[type]||1.25 fallback silently underpays on a typo'd type).
func overtimeRateNumeric(otType string) (pgtype.Numeric, bool) {
	raw, known := otRates[otType]
	if !known {
		return pgtype.Numeric{}, false
	}
	var n pgtype.Numeric
	if err := n.Scan(raw); err != nil {
		// Unreachable: every otRates value is a fixed, valid decimal literal.
		return pgtype.Numeric{}, false
	}
	return n, true
}
