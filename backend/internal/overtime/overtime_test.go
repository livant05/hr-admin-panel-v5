package overtime_test

import (
	"testing"

	"github.com/livant05/rrhh-go/internal/overtime"
	"github.com/livant05/rrhh-go/internal/payroll"
)

func TestRate_StatutoryTable(t *testing.T) {
	want := map[string]string{
		"regular": "1.25", "mixed": "1.50", "night": "1.75", "sunday": "1.50", "holiday": "2.50",
	}
	for typ, rate := range want {
		got, ok := overtime.Rate(typ)
		if !ok || got != rate {
			t.Errorf("Rate(%q) = %q, %v; want %q", typ, got, ok, rate)
		}
	}
	if _, ok := overtime.Rate("weekend"); ok {
		t.Error("unknown type must not resolve")
	}
	if overtime.DefaultType != "regular" || overtime.MaxHoursPerRecord != 3 {
		t.Errorf("caps changed: default=%q max=%d", overtime.DefaultType, overtime.MaxHoursPerRecord)
	}
}

func TestAmount_IsHourlyRateTimesRateTimesHours(t *testing.T) {
	hourly := payroll.MustParseDecimal("5.4321")
	hours := payroll.MustParseDecimal("2.5")
	got, ok := overtime.Amount(hourly, hours, "night")
	if !ok {
		t.Fatal("night must be a known type")
	}
	// 5.4321 * 1.75 * 2.5 = 23.7654375 -> 23.77 at money precision.
	if s := got.FloatString(2); s != "23.77" {
		t.Errorf("amount = %s, want 23.77", s)
	}
	if _, ok := overtime.Amount(hourly, hours, "nope"); ok {
		t.Error("unknown type must report ok=false")
	}
}
