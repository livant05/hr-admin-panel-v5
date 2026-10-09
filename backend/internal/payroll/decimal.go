package payroll

import (
	"errors"
	"fmt"
	"math/big"
)

// Num is an immutable exact-decimal value backed by math/big.Rat. It is the
// package's only arithmetic type (R1a): every statutory constant and every
// intermediate computed value flows through Num, never a float64 literal and
// never pgtype.Numeric (which has no arithmetic methods -- overtime.go never
// computes with it, it only hands one bind parameter to Postgres).
//
// The zero value of Num is a valid representation of 0. Every method below
// allocates a fresh *big.Rat for its result and never mutates the receiver or
// its argument, because big.Rat's own methods (e.g. (*big.Rat).Add) write
// into the receiver -- passing a bare *big.Rat between dozens of computed
// fields would be an aliasing bug waiting to happen.
//
// There is deliberately NO FromFloat / FromFloat64 constructor. That omission
// is the mechanical enforcement of "a money constant is a decimal string
// literal, never a float64 literal" (the overtime.go principle, extended here
// to actual chained arithmetic rather than a single bind parameter).
type Num struct {
	r *big.Rat
}

// ErrDivideByZero is returned by Num.Div when the divisor is zero. Num never
// panics on a zero divisor -- the caller decides what a zero divisor means
// for its own formula (e.g. calcLiq's primaMonths clamp, isrRate's explicit
// zero-guard).
var ErrDivideByZero = errors.New("payroll: division by zero")

// rat returns the underlying *big.Rat, treating a zero-value Num (nil r) as
// an exact 0 rather than requiring every caller to construct via Zero().
func (n Num) rat() *big.Rat {
	if n.r == nil {
		return new(big.Rat)
	}
	return n.r
}

// Zero returns the exact value 0.
func Zero() Num {
	return Num{r: new(big.Rat)}
}

// FromInt returns the exact integer value v.
func FromInt(v int64) Num {
	return Num{r: new(big.Rat).SetInt64(v)}
}

// FromFrac returns the exact fraction a/b (e.g. FromFrac(1, 12) for the
// décimo provision rate, held exactly -- unlike the JS float64 1/12, which is
// 0.08333333333333333). Panics if b is 0: this is a constructor for a fixed,
// known-nonzero literal fraction, never for a runtime-computed divisor.
func FromFrac(a, b int64) Num {
	if b == 0 {
		panic("payroll: FromFrac called with a zero denominator")
	}
	return Num{r: big.NewRat(a, b)}
}

// ParseDecimal parses an exact decimal string literal (e.g. "0.0975",
// "11000", "-4.3333") into a Num. This is the only way to introduce a money
// or ratio constant into the package -- there is no FromFloat.
func ParseDecimal(s string) (Num, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return Num{}, fmt.Errorf("payroll: invalid decimal literal %q", s)
	}
	return Num{r: r}, nil
}

// MustParseDecimal is ParseDecimal for package-level var initializers, where
// an invalid literal is a programming error, not a runtime condition. It
// panics on error.
func MustParseDecimal(s string) Num {
	n, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return n
}

// Add returns n + o.
func (n Num) Add(o Num) Num {
	return Num{r: new(big.Rat).Add(n.rat(), o.rat())}
}

// Sub returns n - o.
func (n Num) Sub(o Num) Num {
	return Num{r: new(big.Rat).Sub(n.rat(), o.rat())}
}

// Mul returns n * o.
func (n Num) Mul(o Num) Num {
	return Num{r: new(big.Rat).Mul(n.rat(), o.rat())}
}

// Div returns n / o, or ErrDivideByZero if o is zero. It never panics.
func (n Num) Div(o Num) (Num, error) {
	if o.IsZero() {
		return Num{}, ErrDivideByZero
	}
	return Num{r: new(big.Rat).Quo(n.rat(), o.rat())}, nil
}

// Cmp compares n and o, returning -1, 0 or +1 as n is less than, equal to, or
// greater than o.
func (n Num) Cmp(o Num) int {
	return n.rat().Cmp(o.rat())
}

// IsZero reports whether n is exactly 0.
func (n Num) IsZero() bool {
	return n.rat().Sign() == 0
}

// IsPositive reports whether n is strictly greater than 0. This is the exact
// shape of every JS "acumX > 0" branch test (the five independent acumulados
// checks in calcLiq).
func (n Num) IsPositive() bool {
	return n.rat().Sign() > 0
}

// Max returns the greater of n and o.
func (n Num) Max(o Num) Num {
	if n.Cmp(o) >= 0 {
		return n
	}
	return o
}

// Min returns the lesser of n and o.
func (n Num) Min(o Num) Num {
	if n.Cmp(o) <= 0 {
		return n
	}
	return o
}

// RoundHalfUp rounds n to the nearest integer, with exact halves rounding
// toward +Infinity -- this matches JS Math.round, NOT Go's usual
// round-half-away-from-zero and NOT math.Floor. It is computed as
// floor(n + 1/2) using exact big.Int Euclidean division (whose quotient is
// always the floor for a positive divisor, regardless of the sign of the
// numerator), so no intermediate rounding or float64 ever occurs.
func (n Num) RoundHalfUp() Num {
	shifted := new(big.Rat).Add(n.rat(), big.NewRat(1, 2))
	q := new(big.Int)
	m := new(big.Int)
	q.DivMod(shifted.Num(), shifted.Denom(), m)
	return Num{r: new(big.Rat).SetInt(q)}
}

// FloatString returns n as a decimal string with exactly prec digits after
// the radix point. This is the ONLY exit from exact rational arithmetic to a
// fixed decimal -- money uses FloatString(2) (matching NUMERIC(_,2) storage
// precision), ratios use FloatString(6).
func (n Num) FloatString(prec int) string {
	return n.rat().FloatString(prec)
}

// String returns n at 6-decimal precision, for logs.
func (n Num) String() string {
	return n.FloatString(6)
}
