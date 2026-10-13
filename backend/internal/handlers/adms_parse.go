package handlers

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ATTLOG parsing for the ZKTeco ADMS push protocol. The wire format is
// PROVISIONAL (parity with the retired Supabase Edge Function, pending a
// real-device check): one punch per line, tab separated,
//
//	PIN \t YYYY-MM-DD HH:MM:SS \t <unused> \t code [\t ...]
//
// where code 0/4 is a check-in and 1/5 a check-out.

// Rejection reasons. They mirror the CHECK constraint on
// zkteco_ingest_rejections (migration 0011).
const (
	reasonDeviceUnclaimed = "device_unclaimed"
	reasonNotFound        = "not_found"
	// reasonTenantMismatch stays allowed by the CHECK constraint but is never
	// persisted: a cedula that exists only in another company is stored exactly
	// like reasonNotFound (CO-S5a-1), so counters visible to a tenant never
	// reveal that a cedula exists elsewhere. The distinction lives only in a
	// server log line.
	reasonTenantMismatch  = "tenant_mismatch"
	reasonAmbiguous       = "ambiguous"
	reasonNoCheckin       = "no_checkin"
	reasonMalformed       = "malformed"
	reasonUnsupportedCode = "unsupported_code"
)

// rejectionReasons is the closed set of reason values, in a stable order.
var rejectionReasons = []string{
	reasonDeviceUnclaimed, reasonNotFound, reasonTenantMismatch, reasonAmbiguous,
	reasonNoCheckin, reasonMalformed, reasonUnsupportedCode,
}

// RejectionReasons returns the reasons a rejection counter can carry. It is
// exported so the migration test can check the CHECK constraint against the Go
// constants instead of repeating the list by hand.
func RejectionReasons() []string {
	return append([]string(nil), rejectionReasons...)
}

// Request limits (design D11, PROVISIONAL until a real-device check). They are
// sized together: a legitimate device pushes the punches recorded since its
// last acknowledgement, normally a handful of lines and a few thousand after a
// long outage. A line is about 40-60 bytes, so maxATTLOGLines lines are well
// under the 2 MiB body cap enforced by the HTTP handler; the byte cap is the
// outer bound, the line cap keeps per-request database work bounded.
const (
	// maxATTLOGLines caps the non-empty lines accepted in one request.
	maxATTLOGLines = 10000
	// maxPINLength is the longest accepted employee PIN (cedula), in BYTES:
	// cedulas are ASCII, so a multibyte PIN is short on characters but long on
	// bytes on purpose. The value leaves margin over the longest cedula
	// formats in use (a dashed Panamanian cedula is about 9-13 bytes). It also
	// bounds the pin persisted as last_pin.
	maxPINLength = 32
	// attlogMinFields is the minimum tab-separated field count of a line.
	attlogMinFields = 4
	// attlogCodeIndex is the position of the in/out code in a line.
	attlogCodeIndex  = 3
	attlogTimeLayout = "2006-01-02 15:04:05"
)

type punchKind int

const (
	punchIn punchKind = iota + 1
	punchOut
)

// punch is one parsed, well-formed attendance event.
type punch struct {
	PIN  string
	At   time.Time
	Kind punchKind
}

// reject is a line that parsed into no punch. PIN is whatever could be read
// from the first field (possibly empty) and feeds the last_pin counter column.
type reject struct {
	Reason string
	PIN    string
}

// parseATTLOG classifies every non-empty line of an ATTLOG body. It never
// fails a batch for a bad line: bad lines become rejects. When the body has
// more than maxATTLOGLines non-empty lines it returns tooMany=true and nothing
// else, so the caller can refuse the request before any write.
func parseATTLOG(body string) (punches []punch, rejects []reject, tooMany bool) {
	lines := 0
	// Walk the body one line at a time (no up-front split) so a request far
	// over the cap is abandoned right after line maxATTLOGLines+1 instead of
	// materializing every line first.
	for rest := body; rest != ""; {
		var raw string
		raw, rest, _ = strings.Cut(rest, "\n")
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		if lines > maxATTLOGLines {
			return nil, nil, true
		}
		p, rej, ok := parseATTLOGLine(line)
		if ok {
			punches = append(punches, p)
		} else {
			rejects = append(rejects, rej)
		}
	}
	return punches, rejects, false
}

// boundedPIN makes the first field of a line safe to persist in the TEXT
// last_pin column of zkteco_ingest_rejections: invalid UTF-8 and NUL bytes
// (both rejected by PostgreSQL, which would fail the whole batch) are dropped,
// and the result is cut to maxPINLength BYTES, backing off to a rune boundary
// so a multibyte character is never split. A line without a tab is the whole
// garbage line in its first field, which is why the bound matters.
func boundedPIN(s string) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
	if len(s) <= maxPINLength {
		return s
	}
	cut := maxPINLength
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func parseATTLOGLine(line string) (punch, reject, bool) {
	fields := strings.Split(line, "\t")
	pin := strings.TrimSpace(fields[0])
	malformed := reject{Reason: reasonMalformed, PIN: boundedPIN(pin)}

	// maxPINLength counts bytes (cedulas are ASCII). A pin that boundedPIN
	// would alter (invalid UTF-8, NUL, too long) is malformed, never resolved.
	if len(fields) < attlogMinFields || pin == "" || boundedPIN(pin) != pin {
		return punch{}, malformed, false
	}
	at, err := time.Parse(attlogTimeLayout, strings.TrimSpace(fields[1]))
	if err != nil {
		return punch{}, malformed, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(fields[attlogCodeIndex]))
	if err != nil {
		return punch{}, malformed, false
	}
	switch code {
	case 0, 4:
		return punch{PIN: pin, At: at, Kind: punchIn}, reject{}, true
	case 1, 5:
		return punch{PIN: pin, At: at, Kind: punchOut}, reject{}, true
	default:
		return punch{}, reject{Reason: reasonUnsupportedCode, PIN: pin}, false
	}
}
