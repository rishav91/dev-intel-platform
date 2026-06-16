package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/dev-intel/platform/libs/go/events"
)

func testSpanContext(t *testing.T) (trace.SpanContext, trace.TraceID) {
	t.Helper()
	tid, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	sid, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return sc, tid
}

// Inject→Extract must round-trip the trace through a header map (W3C traceparent)
// — this is the mechanism that keeps one request's spans on one trace across the
// Kafka and outbox hops.
func TestTraceContextRoundTripsThroughHeaders(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	sc, tid := testSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	headers := map[string]string{}
	Inject(ctx, headers)
	if headers[events.HeaderTraceParent] == "" {
		t.Fatalf("traceparent not injected; got headers %v", headers)
	}

	got := Extract(context.Background(), headers)
	gsc := trace.SpanContextFromContext(got)
	if gsc.TraceID() != tid {
		t.Errorf("trace id: want %s got %s", tid, gsc.TraceID())
	}
	if TraceID(got) != tid.String() {
		t.Errorf("TraceID(): want %s got %s", tid.String(), TraceID(got))
	}
}

// The logger must stamp trace_id/span_id when (and only when) the context carries
// a span, so logs cross-link with traces in Grafana.
func TestLoggerStampsTraceIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

	sc, tid := testSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	log.InfoContext(ctx, "with span")
	if !strings.Contains(buf.String(), tid.String()) {
		t.Errorf("log line missing trace_id %s: %s", tid.String(), buf.String())
	}

	buf.Reset()
	log.InfoContext(context.Background(), "no span")
	if strings.Contains(buf.String(), "trace_id") {
		t.Errorf("log line should carry no trace_id without a span: %s", buf.String())
	}
}
