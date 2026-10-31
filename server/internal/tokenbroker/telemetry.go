package tokenbroker

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const instrumentationName = "github.com/githubnext/gh-cao/server"

const (
	spanExchange = "cao_tokenbroker.exchange"
	spanIssue    = "cao_tokenbroker.token.issue"
	spanRevoke   = "cao_tokenbroker.token.revoke"
	spanShutdown = "cao_tokenbroker.shutdown"

	metricExchangeCount    = "cao_tokenbroker.exchange.count"
	metricExchangeDuration = "cao_tokenbroker.exchange.duration"
	metricTokenCount       = "cao_tokenbroker.token.operation.count"
	metricTokenDuration    = "cao_tokenbroker.token.operation.duration"
	metricShutdownCount    = "cao_tokenbroker.shutdown.count"
	metricShutdownDuration = "cao_tokenbroker.shutdown.duration"

	attributeOperation = "cao_tokenbroker.operation"
	attributeOutcome   = "cao_tokenbroker.outcome"
)

type operation string
type outcome string

const (
	operationExchange   operation = "exchange"
	operationInvalidate operation = "invalidate"
	operationIssue      operation = "issue"
	operationCleanup    operation = "cleanup"
	operationShutdown   operation = "shutdown"

	outcomeSuccess         outcome = "success"
	outcomeInvalidRequest  outcome = "invalid_request"
	outcomeUnauthenticated outcome = "unauthenticated"
	outcomeDenied          outcome = "denied"
	outcomeUnsupported     outcome = "unsupported"
	outcomeThrottled       outcome = "throttled"
	outcomeUnavailable     outcome = "unavailable"
	outcomeDeliveryFailed  outcome = "delivery_failed"
)

type operationMetrics struct {
	count    metric.Int64Counter
	duration metric.Float64Histogram
}

type brokerTelemetry struct {
	exchange operationMetrics
	token    operationMetrics
	shutdown operationMetrics
}

// Instruments belong to the service, not a cached global provider or a callback
// reading credential state. Exported attributes are exclusively fixed enums.
func newBrokerTelemetry() brokerTelemetry {
	meter := otel.Meter(instrumentationName)
	newMetrics := func(count, duration, unit, description string) operationMetrics {
		counter, _ := meter.Int64Counter(count, metric.WithUnit(unit), metric.WithDescription(description))
		histogram, _ := meter.Float64Histogram(duration, metric.WithUnit("s"),
			metric.WithDescription(description),
			metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.5, 1, 2, 4, 8))
		return operationMetrics{count: counter, duration: histogram}
	}
	return brokerTelemetry{
		exchange: newMetrics(metricExchangeCount, metricExchangeDuration, "{request}", "Token broker requests by operation and outcome."),
		token:    newMetrics(metricTokenCount, metricTokenDuration, "{token}", "Token issuance and per-token revocation attempts by operation and outcome."),
		shutdown: newMetrics(metricShutdownCount, metricShutdownDuration, "{shutdown}", "Token broker shutdown attempts by outcome."),
	}
}

type observation struct {
	ctx     context.Context
	span    trace.Span
	metrics operationMetrics
	started time.Time
}

func observe(ctx context.Context, name string, metrics operationMetrics) (context.Context, observation) {
	// Preserve cancellation and trace correlation without inherited metadata.
	ctx = telemetry.ExtractTraceparent(ctx, "")
	ctx, span := telemetry.Tracer().Start(ctx, name)
	return ctx, observation{ctx: ctx, span: span, metrics: metrics, started: time.Now()}
}

func (o observation) finish(op operation, result outcome) {
	attrs := []attribute.KeyValue{
		attribute.String(attributeOperation, string(op)),
		attribute.String(attributeOutcome, string(result)),
	}
	o.span.SetAttributes(attrs...)
	if result == outcomeUnavailable || result == outcomeDeliveryFailed {
		o.span.SetStatus(codes.Error, "failed")
	}
	options := metric.WithAttributes(attrs...)
	if o.metrics.count != nil {
		o.metrics.count.Add(o.ctx, 1, options)
	}
	if o.metrics.duration != nil {
		o.metrics.duration.Record(o.ctx, time.Since(o.started).Seconds(), options)
	}
	o.span.End()
}
