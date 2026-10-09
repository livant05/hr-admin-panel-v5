package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/livant05/rrhh-go/internal/payroll"
)

// numeric.go is the ONLY bridge between the pure payroll.Num arithmetic type
// (backend/internal/payroll, R1a -- math/big.Rat, no pgx import) and
// pgtype.Numeric (the wire/scan type every handler request/response struct
// already uses). Both directions go through the exact decimal string form,
// never Float64Value() -- the whole point of payroll.Num is to avoid a
// float64 round-trip anywhere money is computed or persisted (design R8).

// numToNumeric converts a calculated payroll.Num into a valid
// pgtype.Numeric at money precision (2 decimals, matching this phase's
// NUMERIC(_,2) columns), via Num.FloatString(2) -> pgtype.Numeric.Scan. A
// Scan failure here is unreachable: FloatString always produces a plain
// decimal literal pgtype.Numeric's text scanner accepts.
func numToNumeric(n payroll.Num) pgtype.Numeric {
	var pn pgtype.Numeric
	if err := pn.Scan(n.FloatString(2)); err != nil {
		panic(fmt.Sprintf("numToNumeric: unreachable scan failure for %q: %v", n.FloatString(2), err))
	}
	return pn
}

// numericToNum converts an incoming pgtype.Numeric (e.g. employees.salary,
// an aggregated overtime/deduction sum) into a payroll.Num. It goes through
// Numeric.Value()'s decimal string form -- NOT Float64Value(), which would
// reintroduce the float64 imprecision payroll.Num exists to avoid. An
// invalid (NULL) Numeric converts to an exact zero, matching every `||0` the
// JS does at its own read sites.
func numericToNum(pn pgtype.Numeric) (payroll.Num, error) {
	if !pn.Valid {
		return payroll.Zero(), nil
	}
	v, err := pn.Value()
	if err != nil {
		return payroll.Num{}, err
	}
	s, ok := v.(string)
	if !ok {
		return payroll.Num{}, fmt.Errorf("numericToNum: unexpected Value() type %T", v)
	}
	return payroll.ParseDecimal(s)
}

// numToJSONNumber serializes a calculated payroll.Num as a BARE JSON number
// (design R6, task 7.4), built from Num.FloatString(prec) -- never through
// pgtype.Numeric and never through float64. Two intended consequences: the
// liquidación breakdown/inputs JSONB payload keeps printLiq's
// `(n||0).toFixed(2)` template working unchanged once a stored record is
// re-read client-side, and json.Number is string-preserving on decode, so a
// Go re-read of a persisted value is exact -- decoding into float64 would
// silently reintroduce the imprecision payroll.Num exists to avoid.
func numToJSONNumber(n payroll.Num, prec int) json.Number {
	return json.Number(n.FloatString(prec))
}
