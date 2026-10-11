// Package overtime holds the statutory overtime rules shared by the HTTP
// handlers and the Supabase import tool: the recargo table, the default type,
// the per-record hour cap and the amount formula. Pure: stdlib and payroll.Num.
package overtime

import (
	"strings"

	"github.com/livant05/rrhh-go/internal/payroll"
)

// rates mirrors OT_RATES (hr_admin_panel.html:1857) -- statutory Panama
// Código de Trabajo overtime recargos. A package-level table, not tenant
// configuration: these are statutory rates. Values are literal decimal
// strings (never floats) so callers can build exact numerics from them.
var rates = map[string]string{
	"regular": "1.25", // diurna +25%
	"mixed":   "1.50", // mixta +50%
	"night":   "1.75", // nocturna +75%
	"sunday":  "1.50", // descanso +50%
	"holiday": "2.50", // festivo +150%
}

// DefaultType matches the overtime_logs.type column default.
const DefaultType = "regular"

// MaxHoursPerRecord enforces Art. 36 num. 4: at most 3 overtime hours per
// record.
const MaxHoursPerRecord = 3

// Rate resolves an overtime type to its recargo as an exact decimal literal,
// or ok=false for an unknown type.
func Rate(otType string) (rate string, ok bool) {
	rate, ok = rates[otType]
	return rate, ok
}

// Amount is hourly_rate * rate(type) * hours, unrounded; callers round to
// money precision (FloatString(2)) when persisting. ok=false for an unknown
// type.
func Amount(hourlyRate, hours payroll.Num, otType string) (payroll.Num, bool) {
	raw, ok := Rate(otType)
	if !ok {
		return payroll.Num{}, false
	}
	return hourlyRate.Mul(payroll.MustParseDecimal(raw)).Mul(hours), true
}

// typeOrder is the statutory presentation order of the known types.
var typeOrder = []string{"regular", "mixed", "night", "sunday", "holiday"}

// Types lists the known overtime types in statutory order (a fresh slice).
func Types() []string { return append([]string(nil), typeOrder...) }

// TypesList is Types joined for error messages: "regular, mixed, ...".
func TypesList() string { return strings.Join(typeOrder, ", ") }
