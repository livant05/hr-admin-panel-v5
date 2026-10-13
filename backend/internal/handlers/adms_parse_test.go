package handlers

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func attTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// TestParseATTLOG_Lines pins the per-line classification of the PROVISIONAL
// ATTLOG wire format: PIN \t datetime \t <unused> \t code [\t ...].
func TestParseATTLOG_Lines(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantPunches []punch
		wantRejects []reject
	}{
		{
			name:        "check-in code 0",
			body:        "1001\t2026-03-02 08:00:00\t0\t0\t0\t0",
			wantPunches: []punch{{PIN: "1001", At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn}},
		},
		{
			name:        "overtime-in code 4",
			body:        "1001\t2026-03-02 18:00:00\t0\t4",
			wantPunches: []punch{{PIN: "1001", At: attTime(t, "2026-03-02 18:00:00"), Kind: punchIn}},
		},
		{
			name:        "check-out code 1",
			body:        "1002\t2026-03-02 17:30:15\t0\t1",
			wantPunches: []punch{{PIN: "1002", At: attTime(t, "2026-03-02 17:30:15"), Kind: punchOut}},
		},
		{
			name:        "overtime-out code 5",
			body:        "1002\t2026-03-02 22:00:00\t0\t5",
			wantPunches: []punch{{PIN: "1002", At: attTime(t, "2026-03-02 22:00:00"), Kind: punchOut}},
		},
		{
			name:        "CRLF line endings and trailing blank lines",
			body:        "1001\t2026-03-02 08:00:00\t0\t0\r\n1002\t2026-03-02 08:05:00\t0\t0\r\n\r\n",
			wantPunches: []punch{{PIN: "1001", At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn}, {PIN: "1002", At: attTime(t, "2026-03-02 08:05:00"), Kind: punchIn}},
		},
		{
			name:        "blank and whitespace-only lines are skipped silently",
			body:        "\n   \n1001\t2026-03-02 08:00:00\t0\t0\n\t\n",
			wantPunches: []punch{{PIN: "1001", At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn}},
		},
		{
			name:        "fewer than 4 fields is malformed",
			body:        "1001\t2026-03-02 08:00:00\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "bad datetime is malformed",
			body:        "1001\t2026-13-45 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "datetime without time part is malformed",
			body:        "1001\t2026-03-02\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "non-numeric code is malformed",
			body:        "1001\t2026-03-02 08:00:00\t0\tX",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "empty pin is malformed",
			body:        "\t2026-03-02 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed}},
		},
		{
			name:        "pin over 32 chars is malformed",
			body:        strings.Repeat("9", 33) + "\t2026-03-02 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: strings.Repeat("9", 32)}},
		},
		{
			name: "garbage line without a tab keeps only its first 32 bytes as the pin",
			body: strings.Repeat("g", 100),
			wantRejects: []reject{
				{Reason: reasonMalformed, PIN: strings.Repeat("g", 32)},
			},
		},
		{
			name:        "a pin is cut at a rune boundary, never inside a multibyte character",
			body:        strings.Repeat("9", 31) + "é\t2026-03-02 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: strings.Repeat("9", 31)}},
		},
		{
			name:        "a multibyte pin that fits in 32 bytes is accepted",
			body:        strings.Repeat("é", 16) + "\t2026-03-02 08:00:00\t0\t0",
			wantPunches: []punch{{PIN: strings.Repeat("é", 16), At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn}},
		},
		{
			name:        "a pin with a NUL byte is malformed and the NUL is dropped from the stored pin",
			body:        "10\x0001\t2026-03-02 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "a pin with invalid UTF-8 is malformed and the bad bytes are dropped",
			body:        "10\xff01\t2026-03-02 08:00:00\t0\t0",
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "1001"}},
		},
		{
			name:        "pin of exactly 32 chars is accepted",
			body:        strings.Repeat("9", 32) + "\t2026-03-02 08:00:00\t0\t0",
			wantPunches: []punch{{PIN: strings.Repeat("9", 32), At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn}},
		},
		{
			name:        "other integer codes are unsupported_code",
			body:        "1001\t2026-03-02 08:00:00\t0\t2\n1001\t2026-03-02 08:01:00\t0\t3\n1001\t2026-03-02 08:02:00\t0\t-1",
			wantRejects: []reject{{Reason: reasonUnsupportedCode, PIN: "1001"}, {Reason: reasonUnsupportedCode, PIN: "1001"}, {Reason: reasonUnsupportedCode, PIN: "1001"}},
		},
		{
			name: "mixed batch: valid, malformed, valid",
			body: "1001\t2026-03-02 08:00:00\t0\t0\n" +
				"garbage\n" +
				"1002\t2026-03-02 17:00:00\t0\t1\n",
			wantPunches: []punch{
				{PIN: "1001", At: attTime(t, "2026-03-02 08:00:00"), Kind: punchIn},
				{PIN: "1002", At: attTime(t, "2026-03-02 17:00:00"), Kind: punchOut},
			},
			wantRejects: []reject{{Reason: reasonMalformed, PIN: "garbage"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			punches, rejects, tooMany := parseATTLOG(tc.body)
			if tooMany {
				t.Fatal("unexpected tooMany")
			}
			if !reflect.DeepEqual(punches, tc.wantPunches) {
				t.Errorf("punches:\n got %+v\nwant %+v", punches, tc.wantPunches)
			}
			if !reflect.DeepEqual(rejects, tc.wantRejects) {
				t.Errorf("rejects:\n got %+v\nwant %+v", rejects, tc.wantRejects)
			}
		})
	}
}

// TestParseATTLOG_LineCap pins the 10,000 non-empty line cap: exactly the cap
// parses; one more flags tooMany and returns nothing to process. Blank lines
// do not count.
func TestParseATTLOG_LineCap(t *testing.T) {
	build := func(n int, blanks int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "%d\t2026-03-02 08:00:00\t0\t0\n", 1000+i)
		}
		b.WriteString(strings.Repeat("\n", blanks))
		return b.String()
	}

	punches, rejects, tooMany := parseATTLOG(build(maxATTLOGLines, 50))
	if tooMany {
		t.Fatalf("exactly %d lines (plus blanks) must not be flagged", maxATTLOGLines)
	}
	if len(punches) != maxATTLOGLines || len(rejects) != 0 {
		t.Fatalf("got %d punches, %d rejects; want %d, 0", len(punches), len(rejects), maxATTLOGLines)
	}

	punches, rejects, tooMany = parseATTLOG(build(maxATTLOGLines+1, 0))
	if !tooMany {
		t.Fatalf("%d lines must be flagged tooMany", maxATTLOGLines+1)
	}
	if punches != nil || rejects != nil {
		t.Fatalf("tooMany must return nothing to process, got %d punches %d rejects", len(punches), len(rejects))
	}
}

// TestParseATTLOG_StopsScanningAtTheCap pins CO-S5a-6: a body far beyond the
// cap is abandoned as soon as the cap is exceeded, instead of being split
// into one string per line first. The body is 1,000,000 short lines; splitting
// it would allocate a 16 MB slice of line headers alone, while the early stop
// stays within the first 10,001 lines.
func TestParseATTLOG_StopsScanningAtTheCap(t *testing.T) {
	body := strings.Repeat("x\n", 1_000_000)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	punches, rejects, tooMany := parseATTLOG(body)
	runtime.ReadMemStats(&after)

	if !tooMany || punches != nil || rejects != nil {
		t.Fatalf("tooMany=%v punches=%d rejects=%d, want tooMany and nothing to process", tooMany, len(punches), len(rejects))
	}
	const budget = 4 << 20
	if got := after.TotalAlloc - before.TotalAlloc; got > budget {
		t.Fatalf("parsing allocated %d bytes, want under %d: the body is being split before the cap applies", got, budget)
	}
}
