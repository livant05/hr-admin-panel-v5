package payroll

import (
	"os"
	"strings"
	"testing"
)

func TestParseDecimal(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"integer", "11000", false},
		{"decimal", "0.0975", false},
		{"four-decimals", "4.3333", false},
		{"negative", "-5000", false},
		{"invalid", "not-a-number", true},
		{"empty", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ParseDecimal(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDecimal(%q) = %v, want error", tc.input, n)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDecimal(%q) returned unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestMustParseDecimal_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustParseDecimal did not panic on an invalid literal")
		}
	}()
	MustParseDecimal("not-a-number")
}

func TestNum_AddSubMul(t *testing.T) {
	a := MustParseDecimal("10.5")
	b := MustParseDecimal("3.25")

	if got, want := a.Add(b).FloatString(2), "13.75"; got != want {
		t.Errorf("Add: got %s, want %s", got, want)
	}
	if got, want := a.Sub(b).FloatString(2), "7.25"; got != want {
		t.Errorf("Sub: got %s, want %s", got, want)
	}
	if got, want := a.Mul(b).FloatString(4), "34.1250"; got != want {
		t.Errorf("Mul: got %s, want %s", got, want)
	}
}

func TestNum_DivByNonZero(t *testing.T) {
	a := MustParseDecimal("1")
	b := MustParseDecimal("3")

	got, err := a.Div(b)
	if err != nil {
		t.Fatalf("Div returned unexpected error: %v", err)
	}
	if want := "0.333333"; got.FloatString(6) != want {
		t.Errorf("Div: got %s, want %s", got.FloatString(6), want)
	}
}

func TestNum_DivByZeroReturnsErrorNeverPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Div by zero must not panic, got panic: %v", r)
		}
	}()

	a := MustParseDecimal("10")
	_, err := a.Div(Zero())
	if err == nil {
		t.Fatal("Div by zero must return an error, got nil")
	}
}

func TestNum_Cmp(t *testing.T) {
	a := MustParseDecimal("5")
	b := MustParseDecimal("5")
	c := MustParseDecimal("7")

	if a.Cmp(b) != 0 {
		t.Errorf("Cmp(5,5) = %d, want 0", a.Cmp(b))
	}
	if a.Cmp(c) >= 0 {
		t.Errorf("Cmp(5,7) = %d, want negative", a.Cmp(c))
	}
	if c.Cmp(a) <= 0 {
		t.Errorf("Cmp(7,5) = %d, want positive", c.Cmp(a))
	}
}

func TestNum_IsZeroIsPositive(t *testing.T) {
	if !Zero().IsZero() {
		t.Error("Zero().IsZero() = false, want true")
	}
	if Zero().IsPositive() {
		t.Error("Zero().IsPositive() = true, want false")
	}
	if !MustParseDecimal("0.01").IsPositive() {
		t.Error("0.01.IsPositive() = false, want true")
	}
	if MustParseDecimal("-0.01").IsPositive() {
		t.Error("-0.01.IsPositive() = true, want false")
	}
	if MustParseDecimal("0.01").IsZero() {
		t.Error("0.01.IsZero() = true, want false")
	}
}

func TestNum_MaxMin(t *testing.T) {
	a := MustParseDecimal("3")
	b := MustParseDecimal("7")

	if got := a.Max(b).FloatString(0); got != "7" {
		t.Errorf("Max(3,7) = %s, want 7", got)
	}
	if got := a.Min(b).FloatString(0); got != "3" {
		t.Errorf("Min(3,7) = %s, want 3", got)
	}
}

func TestNum_RoundHalfUp(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"tie-rounds-toward-positive-infinity", "2.5", "3"},
		{"negative-tie-rounds-toward-positive-infinity", "-2.5", "-2"},
		{"below-half-rounds-down", "2.4", "2"},
		{"above-half-rounds-up", "2.6", "3"},
		{"exact-integer", "4", "4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MustParseDecimal(tc.input).RoundHalfUp().FloatString(0)
			if got != tc.want {
				t.Errorf("RoundHalfUp(%s) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
}

func TestNum_FloatStringAndString(t *testing.T) {
	n, err := MustParseDecimal("1").Div(MustParseDecimal("3"))
	if err != nil {
		t.Fatalf("Div returned unexpected error: %v", err)
	}

	if got, want := n.FloatString(2), "0.33"; got != want {
		t.Errorf("FloatString(2) = %s, want %s", got, want)
	}
	if got, want := n.FloatString(6), "0.333333"; got != want {
		t.Errorf("FloatString(6) = %s, want %s", got, want)
	}
	if got, want := n.String(), n.FloatString(6); got != want {
		t.Errorf("String() = %s, want %s (String must equal FloatString(6))", got, want)
	}
}

// TestNum_NoFloatConstructor mechanically enforces the "never a float64
// literal for money" invariant: the Num type must expose no FromFloat /
// FromFloat64 constructor. Money and ratios are built only from decimal
// string literals via ParseDecimal/MustParseDecimal, mirroring overtime.go's
// existing discipline, extended here to actual arithmetic.
func TestNum_NoFloatConstructor(t *testing.T) {
	src, err := os.ReadFile("decimal.go")
	if err != nil {
		t.Fatalf("reading decimal.go: %v", err)
	}
	for _, forbidden := range []string{"func FromFloat", "func FromFloat64"} {
		if strings.Contains(string(src), forbidden) {
			t.Fatalf("decimal.go must not declare %s -- money/ratios are built only from decimal string literals, never a float literal", forbidden)
		}
	}
}
