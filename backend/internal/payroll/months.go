package payroll

// spanishMonths holds the Spanish month names PayDay's UI expects, 1-indexed
// (index 0 is unused so the array can be indexed directly by month number).
var spanishMonths = [...]string{
	"",
	"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio",
	"Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre",
}

// MonthNameES returns the Spanish name for month (1-12), feeding the
// payroll_history.month_name column (slice 3f) and liquidation_history's
// periodo field (slice 3g). An out-of-range month returns "" rather than
// panicking -- callers validate month ∈ 1..12 before reaching here.
func MonthNameES(month int) string {
	if month < 1 || month >= len(spanishMonths) {
		return ""
	}
	return spanishMonths[month]
}
