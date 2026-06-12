// Package observability provides minimal structured logging + trace-id
// propagation for the Phase 0 spine.
//
// This is deliberately lightweight: a W3C-ish trace id is generated at the
// edge (webhook gateway) and propagated through Kafka headers so a single
// request can be followed across services. Phase 0 exit criterion ("with a
// trace") is satisfied by this correlation id.
//
// UPGRADE PATH (Phase 0+): replace NewTraceID/logger wiring with OpenTelemetry
// — set up a TracerProvider with an OTLP exporter and use otel propagation.
// The seam is intentionally small: only this package and the header constant
// in libs/go/events change.
package observability

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
)

// Logger returns a JSON structured logger for a service.
func Logger(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h).With("service", service)
}

// NewTraceID returns a 16-byte hex trace id (32 chars), W3C trace-id shaped.
func NewTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is effectively impossible; fall back to zeros.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b)
}
