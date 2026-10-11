// Package evaluation holds the pure, DB-free rules for performance
// evaluations: scores validation, the exact average and the category tier.
// All arithmetic is integer: every score is an integer 1-10, so
// avg = sum/8 is exact in eighths and tiers are decided on the integer sum
// (avg >= t  <=>  sum >= 8t), never on a rounded average.
package evaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
)

// Criteria is EVAL_CRITERIA from hr_admin_panel.html, in UI order.
var Criteria = [8]string{"puntualidad", "calidad", "trabajo_equipo", "iniciativa",
	"comunicacion", "liderazgo", "objetivos", "actitud"}

// Category is the 5-tier performance label.
type Category string

const (
	Sobresaliente Category = "Sobresaliente" // sum >= 72 (avg >= 9)
	Excelente     Category = "Excelente"     // sum >= 60 (avg >= 7.5)
	Bueno         Category = "Bueno"         // sum >= 48 (avg >= 6)
	Regular       Category = "Regular"       // sum >= 32 (avg >= 4)
	Deficiente    Category = "Deficiente"
)

const (
	minScore = 1
	maxScore = 10
	// maxScoreLiteral caps a score's JSON number length; generous for
	// forms like 10.0 or 7e0 while keeping big.Rat parsing trivially cheap.
	maxScoreLiteral = 16
)

// ValidationError carries a field-level message (keyed "scores" or
// "scores.<key>") for the handler's field-error response.
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func fieldErr(key, msg string) error {
	f := "scores"
	if key != "" {
		f += "." + key
	}
	return &ValidationError{Field: f, Msg: msg}
}

// ParseScores accepts ONLY a JSON object with exactly the 8 Criteria keys,
// each an integral JSON number in [1,10] ("7" and "7.0" accepted; "7.5",
// 0, 11 and "\"7\"" rejected). A JSON string -- the legacy double-encoded
// JSON.stringify(scores) payload -- is rejected, never unwrapped.
func ParseScores(raw json.RawMessage) (map[string]int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fieldErr("", "must be an object with the 8 criteria")
	}

	out := make(map[string]int, len(Criteria))
	for _, k := range Criteria {
		v, ok := obj[k]
		if !ok {
			return nil, fieldErr(k, "required")
		}
		n, err := parseScore(v)
		if err != nil {
			return nil, fieldErr(k, err.Error())
		}
		out[k] = n
	}
	if len(obj) != len(Criteria) {
		var extra []string
		for k := range obj {
			if _, ok := out[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		return nil, fieldErr(extra[0], "unknown criterion")
	}
	return out, nil
}

func parseScore(v json.RawMessage) (int, error) {
	// Only a bare JSON number: quoted strings, null, bools, arrays and
	// objects all start with a non-numeric byte.
	if len(v) == 0 || (v[0] != '-' && (v[0] < '0' || v[0] > '9')) {
		return 0, errors.New("must be a number")
	}
	// Bound the literal before big.Rat sees it: a huge exponent such as
	// 1e1000000 is valid JSON but would materialize a million-digit number.
	// ParseFloat is cheap for any exponent (it saturates to Inf or 0), and
	// with the length cap an in-range literal cannot carry a large exponent.
	if len(v) > maxScoreLiteral {
		return 0, fmt.Errorf("must be between %d and %d", minScore, maxScore)
	}
	if f, err := strconv.ParseFloat(string(v), 64); err != nil || f < minScore || f > maxScore {
		return 0, fmt.Errorf("must be between %d and %d", minScore, maxScore)
	}
	r, ok := new(big.Rat).SetString(string(v))
	if !ok || !r.IsInt() {
		return 0, errors.New("must be an integer")
	}
	if r.Cmp(big.NewRat(minScore, 1)) < 0 || r.Cmp(big.NewRat(maxScore, 1)) > 0 {
		return 0, fmt.Errorf("must be between %d and %d", minScore, maxScore)
	}
	return int(r.Num().Int64()), nil
}

// Result is the server-authoritative outcome of an evaluation.
type Result struct {
	Scores     map[string]int
	Sum        int
	Hundredths int // avg * 100, half-up
	Category   Category
}

// Evaluate computes the sum, 2-dp average and category from validated scores.
func Evaluate(scores map[string]int) Result {
	sum := 0
	for _, k := range Criteria {
		sum += scores[k]
	}
	var cat Category
	switch {
	case sum >= 72:
		cat = Sobresaliente
	case sum >= 60:
		cat = Excelente
	case sum >= 48:
		cat = Bueno
	case sum >= 32:
		cat = Regular
	default:
		cat = Deficiente
	}
	// avg*100 = sum*12.5; (sum*25+1)/2 is exact for even sums and rounds the
	// only possible tie (a half hundredth, odd sums) up.
	return Result{Scores: scores, Sum: sum, Hundredths: (sum*25 + 1) / 2, Category: cat}
}

// Avg formats the average with 2 decimals, e.g. "7.63".
func (r Result) Avg() string {
	return fmt.Sprintf("%d.%02d", r.Hundredths/100, r.Hundredths%100)
}

// CanonicalScores is the normalized object that gets persisted: the 8 keys
// with integer values; encoding/json sorts map keys, so it is deterministic.
func (r Result) CanonicalScores() json.RawMessage {
	b, err := json.Marshal(r.Scores)
	if err != nil {
		panic(fmt.Sprintf("evaluation: marshal scores: %v", err)) // map[string]int cannot fail
	}
	return b
}
