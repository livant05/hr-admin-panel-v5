package handlers

import (
	"strconv"
	"strings"
	"time"
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
	reasonTenantMismatch  = "tenant_mismatch"
	reasonAmbiguous       = "ambiguous"
	reasonNoCheckin       = "no_checkin"
	reasonMalformed       = "malformed"
	reasonUnsupportedCode = "unsupported_code"
)

const (
	// maxATTLOGLines caps the non-empty lines accepted in one request.
	maxATTLOGLines = 10000
	// maxPINLength is the longest accepted employee PIN (cedula).
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
	for _, raw := range strings.Split(body, "\n") {
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

func parseATTLOGLine(line string) (punch, reject, bool) {
	fields := strings.Split(line, "\t")
	pin := strings.TrimSpace(fields[0])
	malformed := reject{Reason: reasonMalformed, PIN: pin}

	if len(fields) < attlogMinFields || pin == "" || len(pin) > maxPINLength {
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
