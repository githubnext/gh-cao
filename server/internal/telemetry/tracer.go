package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the standardized instrumentation scope name for spans the
// dashboard server starts directly (as opposed to spans created by
// otelhttp's automatic HTTP server instrumentation).
const tracerName = "github.com/githubnext/gh-cao/server"

// Span names are centralized so every application-created span has a stable,
// low-cardinality name. HTTP request attributes and metrics are supplied by
// otelhttp's OpenTelemetry semantic-convention instrumentation.
const (
	SpanHTTPServer          = ServiceName
	SpanIngestRun           = "cao_dashboard.ingest.run"
	SpanQueryExecute        = "cao_dashboard.query.execute"
	SpanPostgresQuery       = "cao_dashboard.postgres.query"
	SpanQueryCacheLookup    = "cao_dashboard.query.cache.lookup"
	SpanQueryCacheStore     = "cao_dashboard.query.cache.store"
	SpanBackfillRun         = "cao_dashboard.collection.backfill"
	SpanCollectionAdmission = "cao_dashboard.collection.webhook.admit"
	SpanCollectionRefresh   = "cao_dashboard.collection.refresh"
	SpanAdmissionIdentity   = "cao_dashboard.admission.identity.verify"
	SpanAdmissionPolicy     = "cao_dashboard.admission.policy.evaluate"
	SpanAdmissionDecision   = "cao_dashboard.admission.decision.resolve"
	SpanAdmissionReplay     = "cao_dashboard.admission.replay.check"
	SpanAdmissionKeys       = "cao_dashboard.admission.keys.refresh"
	SpanAdmissionRun        = "cao_dashboard.admission.run.publish"
	SpanConclusionPolicy    = "cao_dashboard.admission.conclusion.authorize"
	SpanConclusionRecord    = "cao_dashboard.admission.conclusion.record"
	SpanConclusionPublish   = "cao_dashboard.admission.conclusion.publish"
	SpanTokenBrokerIdentity = "cao_dashboard.token_broker.identity.verify"
	SpanTokenBrokerPolicy   = "cao_dashboard.token_broker.policy.evaluate"
	SpanTokenBrokerIssue    = "cao_dashboard.token_broker.credential.issue"
	SpanTokenBrokerRevoke   = "cao_dashboard.token_broker.credential.revoke"
)

// Tracer returns the dashboard server's instrumentation-scoped tracer. It
// always resolves against the current global TracerProvider, so it reflects
// whichever provider Setup installed (or the no-op default).
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// ExtractTraceparent preserves trace correlation and cancellation but drops
// inherited baggage and tracestate, including metadata from an external host.
func ExtractTraceparent(ctx context.Context, value string) context.Context {
	ctx = baggage.ContextWithBaggage(ctx, baggage.Baggage{})
	if parent := trace.SpanContextFromContext(ctx); parent.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, parent.WithTraceState(trace.TraceState{}))
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": value})
}

// TraceIDHeader and SpanIDHeader expose the active W3C Trace Context
// identifiers on API responses so operators can correlate a client-visible
// request with exported spans even when the client did not send its own
// traceparent header.
const (
	TraceIDHeader = "X-Trace-Id"
	SpanIDHeader  = "X-Span-Id"
)

// SetResponseTraceHeaders writes the current span's trace and span ids, when
// valid, onto the response so operators can correlate a request with
// exported telemetry. It is a no-op when the context carries no valid span
// context, which keeps it safe to call unconditionally.
func SetResponseTraceHeaders(response http.ResponseWriter, spanContext trace.SpanContext) {
	if !spanContext.IsValid() {
		return
	}
	response.Header().Set(TraceIDHeader, spanContext.TraceID().String())
	response.Header().Set(SpanIDHeader, spanContext.SpanID().String())
}
