package handlers

import (
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/livant05/rrhh-go/internal/auth"
)

// RouteGate is the test-visible classification of one protected route.
type RouteGate struct {
	Pattern string
	// Class is "open", "handler" or "perm:<key>[|<key>...]"; anything else
	// means the route was registered without a classification.
	Class string
}

// ProtectedRouteGates lists every registered protected route with its
// authorization classification. No database is needed: handlers are only
// referenced, never invoked.
func ProtectedRouteGates() []RouteGate {
	signer := auth.NewSigner("route-gates-test-secret-0123456789", time.Hour)
	a := New(nil, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var out []RouteGate
	for _, rt := range a.protectedRoutes() {
		class := "unclassified"
		switch rt.gate.kind {
		case gateOpen:
			class = "open"
		case gateHandler:
			class = "handler"
		case gatePermission:
			class = "perm:" + strings.Join(rt.gate.keys, "|")
		}
		out = append(out, RouteGate{Pattern: rt.pattern, Class: class})
	}
	return out
}
