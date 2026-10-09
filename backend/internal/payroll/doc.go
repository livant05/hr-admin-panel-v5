// Package payroll is a pure, dependency-free port of the two JS payroll
// calculation functions in hr_admin_panel.html (read-only): prCalc (the
// live monthly payroll calculation, slice 3b -- this package's first
// contents) and calcLiq (the termination liquidación calculation, slice
// 3c). It has no I/O: no net/http, no pgx/pgtype, no generated db package.
// Every money and ratio value is a Num -- an immutable wrapper over
// math/big.Rat, never a float64 -- so the whole package, including both
// golden-fixture suites, builds and tests with nothing but `go test` and a
// directory of JSON: no database, no TEST_DB_URL, and it cannot be disabled
// by SKIP_DB_TESTS=1.
//
// # Five porting hazards
//
// These were found reading the JS source character-by-character and are
// recorded here so a future reader of this package sees the complete list,
// even in a slice (3b) that implements none of them:
//
//  1. Two different weeks-per-month divisors in calcLiq: preaviso divides by
//     4.333 (three 3s); prima de antigüedad and indemnización divide by
//     4.3333 (four 3s). Neither is named in the JS. (calcLiq only, slice 3c)
//  2. calcLiq's floorYears is Math.round(totalMonths/12), not a floor,
//     despite its name. It feeds prima, indemnización weeks and the Art. 701
//     discount. (calcLiq only, slice 3c)
//  3. calcLiq computes isrRate = isrAnual/annual where annual = sal*13. The
//     live JS source (hr_admin_panel.html:3669) already guards this with
//     `annual>0 ? isrAnual/annual : 0`, so at salary=0 it returns 0, not
//     NaN -- confirmed against the real source during slice 3a; there is no
//     deviation to encode. (calcLiq only, slice 3c)
//  4. calcLiq's primaMonths is a divisor (primaMensual = primaTotal /
//     primaMonths), clamped by the JS to a minimum of 1 -- the only thing
//     preventing a second division by zero. (calcLiq only, slice 3c)
//  5. work_type = 0 is falsy in JS: loadPayroll reads
//     `l.work_type ? l.work_type===28 : (l.status==='absent')`, so a row
//     with work_type=0 falls back to the legacy status check. A SQL
//     COALESCE(work_type, ...) port handles NULL but not 0. This is a
//     SQL-layer concern implemented in slice 3e's ListPayrollRunInputs, not
//     in this package's Calculate, which only receives the already-counted
//     AbsentDays as an int.
//
// prCalc's port (this slice) carries none of the five hazards above.
//
// # Arithmetic
//
// Every statutory constant lives in constants.go as an exact decimal string
// literal (never a float64), each commented with its hr_admin_panel.html
// source line. CalcVersion tags every persisted liquidación breakdown
// (slice 3g) with the formula version that produced it.
package payroll
