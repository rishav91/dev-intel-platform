// Package observability provides structured logging and OpenTelemetry tracing
// for the platform spine. A request entering at the webhook gateway is traced
// end-to-end across services and the Kafka/outbox hops via W3C trace context
// (the `traceparent` header), so its spans form one waterfall in Tempo and every
// log line it produces carries the same trace_id for correlation in Loki.
//
// Propagation seam: trace context rides Kafka message headers (Inject/Extract)
// and is persisted across the async outbox DB hop (the relay re-extracts it from
// the row). The only header the wire format depends on is W3C `traceparent`
// (events.HeaderTraceParent); a human-readable trace_id header is set alongside
// for convenience but nothing parses it for propagation.
//
// Degraded by design: if no OTLP collector is reachable, spans are still created
// (so logs stay correlated by trace_id) — exports just drop in the background.
// Tracing never blocks the hot path.
package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// tracerName scopes spans to this codebase; instrumentation library name.
const tracerName = "github.com/dev-intel/platform"

// Init wires the global OpenTelemetry tracer provider and W3C propagator for a
// service and returns a shutdown func to flush pending spans on exit. Traces are
// exported over OTLP/HTTP to OTEL_EXPORTER_OTLP_ENDPOINT (host:port, default
// localhost:4318 — the Tempo OTLP receiver in the dev stack).
//
// The exporter connects lazily, so a missing collector does not fail Init or
// stall the service: spans still get ids (keeping logs correlated) and exports
// simply drop until the collector returns.
func Init(ctx context.Context, service string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	endpoint := getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318")
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(), // dev stack is plaintext
	)
	if err != nil {
		return nil, err
	}

	// service.name is the resource attribute Tempo/Grafana key spans by.
	res := resource.NewSchemaless(attribute.String("service.name", service))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // low volume; sample everything in dev
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns the platform tracer for starting spans.
func Tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Inject writes the current trace context into a header map (W3C traceparent),
// for stamping on an outgoing Kafka message or an outbox row.
func Inject(ctx context.Context, headers map[string]string) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
}

// Extract returns a context carrying the trace context found in the header map,
// so a span started from it links to its upstream producer.
func Extract(ctx context.Context, headers map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
}

// TraceID returns the 32-hex trace id of the span in ctx, or "" if none. Used to
// stamp the human-readable trace-id header and for log fields where a span isn't
// on the logging path.
func TraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

// Logger returns a JSON structured logger for a service. When a log call passes
// a context carrying a span (the *Context methods, e.g. InfoContext), the
// handler stamps trace_id and span_id so logs cross-link with traces.
func Logger(service string) *slog.Logger {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(&traceHandler{Handler: base}).With("service", service)
}

// traceHandler decorates each record with the trace_id/span_id from its context,
// bridging slog to OpenTelemetry.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
