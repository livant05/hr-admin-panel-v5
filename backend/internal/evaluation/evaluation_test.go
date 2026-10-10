package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

const validScores = `{"puntualidad":7,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`

func TestParseScores_Rejects(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		field string
	}{
		{"missing key", `{"puntualidad":7,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7}`, "scores.actitud"},
		{"extra key", `{"puntualidad":7,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7,"extra":5}`, "scores.extra"},
		{"zero", `{"puntualidad":0,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`, "scores.puntualidad"},
		{"eleven", `{"puntualidad":7,"calidad":11,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`, "scores.calidad"},
		{"fraction", `{"puntualidad":7,"calidad":7,"trabajo_equipo":7,"iniciativa":7.5,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`, "scores.iniciativa"},
		{"string value", `{"puntualidad":"7","calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`, "scores.puntualidad"},
		{"null value", `{"puntualidad":null,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`, "scores.puntualidad"},
		{"null", `null`, "scores"},
		{"array", `[7,7,7,7,7,7,7,7]`, "scores"},
		{"empty object", `{}`, "scores.puntualidad"},
		{"double-encoded string", `"{\"puntualidad\":7}"`, "scores"},
		{"empty input", ``, "scores"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScores(json.RawMessage(tt.raw))
			if err == nil {
				t.Fatalf("expected error, got %v", got)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if ve.Field != tt.field {
				t.Fatalf("field = %q, want %q", ve.Field, tt.field)
			}
		})
	}
}

func TestParseScores_Accepts(t *testing.T) {
	t.Run("integers", func(t *testing.T) {
		got, err := ParseScores(json.RawMessage(validScores))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range Criteria {
			if got[k] != 7 {
				t.Fatalf("%s = %d", k, got[k])
			}
		}
	})
	t.Run("7.0 accepted as 7", func(t *testing.T) {
		raw := `{"puntualidad":7.0,"calidad":7,"trabajo_equipo":7,"iniciativa":7,"comunicacion":7,"liderazgo":7,"objetivos":7,"actitud":7}`
		got, err := ParseScores(json.RawMessage(raw))
		if err != nil || got["puntualidad"] != 7 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("boundaries 1 and 10", func(t *testing.T) {
		raw := `{"puntualidad":1,"calidad":10,"trabajo_equipo":1,"iniciativa":10,"comunicacion":1,"liderazgo":10,"objetivos":1,"actitud":10}`
		got, err := ParseScores(json.RawMessage(raw))
		if err != nil || got["puntualidad"] != 1 || got["calidad"] != 10 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

// uniform builds a score map whose 8 values add up to sum (each 1..10).
func uniform(sum int) map[string]int {
	m := map[string]int{}
	rem := sum
	for i, k := range Criteria {
		left := len(Criteria) - i - 1
		v := rem - left // leave at least 1 for each remaining key
		if v > 10 {
			v = 10
		}
		if v < 1 {
			v = 1
		}
		m[k] = v
		rem -= v
	}
	return m
}

func TestEvaluate_Tiers(t *testing.T) {
	tests := []struct {
		sum int
		avg string
		cat Category
	}{
		{72, "9.00", Sobresaliente},
		{71, "8.88", Excelente},
		{60, "7.50", Excelente},
		{59, "7.38", Bueno},
		{48, "6.00", Bueno},
		{47, "5.88", Regular},
		{32, "4.00", Regular},
		{31, "3.88", Deficiente},
		{8, "1.00", Deficiente},
		{80, "10.00", Sobresaliente},
		{61, "7.63", Excelente}, // 7.625 half-up
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("sum-%d", tt.sum), func(t *testing.T) {
			r := Evaluate(uniform(tt.sum))
			if r.Sum != tt.sum {
				t.Fatalf("fixture sum = %d, want %d", r.Sum, tt.sum)
			}
			if r.Avg() != tt.avg || r.Category != tt.cat {
				t.Fatalf("got %s %s, want %s %s", r.Avg(), r.Category, tt.avg, tt.cat)
			}
		})
	}
}

// categoryFromAvg reproduces evalCategory on a 2-dp average string.
func categoryFromAvg(avg string) Category {
	var whole, frac int
	fmt.Sscanf(avg, "%d.%d", &whole, &frac)
	h := whole*100 + frac
	switch {
	case h >= 900:
		return Sobresaliente
	case h >= 750:
		return Excelente
	case h >= 600:
		return Bueno
	case h >= 400:
		return Regular
	}
	return Deficiente
}

func TestEvaluate_RoundingNeverCrossesTier(t *testing.T) {
	for sum := 8; sum <= 80; sum++ {
		r := Evaluate(uniform(sum))
		if r.Sum != sum {
			t.Fatalf("fixture sum %d != %d", r.Sum, sum)
		}
		if got := categoryFromAvg(r.Avg()); got != r.Category {
			t.Fatalf("sum %d: category %s but 2-dp avg %s implies %s", sum, r.Category, r.Avg(), got)
		}
	}
}

func TestCanonicalScores_Deterministic(t *testing.T) {
	a, err := ParseScores(json.RawMessage(validScores))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseScores(json.RawMessage(`{"actitud":7.0,"objetivos":7,"liderazgo":7,"comunicacion":7,"iniciativa":7,"trabajo_equipo":7,"calidad":7,"puntualidad":7}`))
	if err != nil {
		t.Fatal(err)
	}
	ca, cb := Evaluate(a).CanonicalScores(), Evaluate(b).CanonicalScores()
	if !bytes.Equal(ca, cb) || !bytes.Equal(ca, Evaluate(a).CanonicalScores()) {
		t.Fatalf("not deterministic: %s vs %s", ca, cb)
	}
	if string(ca) != `{"actitud":7,"calidad":7,"comunicacion":7,"iniciativa":7,"liderazgo":7,"objetivos":7,"puntualidad":7,"trabajo_equipo":7}` {
		t.Fatalf("unexpected canonical form: %s", ca)
	}
}
