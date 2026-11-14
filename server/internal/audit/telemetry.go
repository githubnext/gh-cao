package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/githubnext/gh-cao/server/audit"
	metricEventCount    = "cao_audit.event.count"
	metricAppendTime    = "cao_audit.append.duration"
	metricMirrorCount   = "cao_audit.mirror.count"
	eventNamePrefix     = "cao.audit."
)

var (
	instrumentsOnce sync.Once
	eventCounter    metric.Int64Counter
	appendDuration  metric.Float64Histogram
	mirrorCounter   metric.Int64Counter
)

func instruments() (metric.Int64Counter, metric.Float64Histogram) {
	instrumentsOnce.Do(func() {
		meter := otel.Meter(instrumentationName)
		eventCounter, _ = meter.Int64Counter(metricEventCount,
			metric.WithDescription("Security audit events, by event, outcome, and whether the audit table stored them."),
			metric.WithUnit("{event}"))
		appendDuration, _ = meter.Float64Histogram(metricAppendTime,
			metric.WithDescription("Time to append one event to the tamper-evident audit table."), metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5))
		mirrorCounter, _ = meter.Int64Counter(metricMirrorCount,
			metric.WithDescription("Audit JSON mirror attempts by success, timeout, or error outcome."),
			metric.WithUnit("{event}"))
	})
	return eventCounter, appendDuration
}

func observeMirror(ctx context.Context, outcome string) {
	_, _ = instruments()
	if mirrorCounter != nil {
		mirrorCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("cao_audit.mirror.outcome", outcome)))
	}
}

// traceAttrs correlates an event with the active span so the audit table,
// the JSON mirror, and OTLP logs join to traces.
func traceAttrs(ctx context.Context) []slog.Attr {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}
	return []slog.Attr{slog.String("trace_id", spanContext.TraceID().String()), slog.String("span_id", spanContext.SpanID().String())}
}

// observe exports one event as an OpenTelemetry log record and metric. Event
// names come from a fixed set, so they are safe metric attributes.
func observe(ctx context.Context, event, outcome string, stored bool, sequence int64, started time.Time, durable bool, attrs []slog.Attr) {
	counter, histogram := instruments()
	if counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String("cao_audit.event", event),
			attribute.String("cao_audit.outcome", outcome), attribute.Bool("cao_audit.stored", stored)))
	}
	if durable && histogram != nil {
		histogram.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attribute.Bool("cao_audit.stored", stored)))
	}
	var record otellog.Record
	record.SetEventName(eventNamePrefix + event)
	record.SetTimestamp(time.Now())
	record.SetBody(attribute.StringValue("audit"))
	switch outcome {
	case Failure:
		record.SetSeverity(otellog.SeverityError)
	case Denied:
		record.SetSeverity(otellog.SeverityWarn)
	default:
		record.SetSeverity(otellog.SeverityInfo)
	}
	record.SetSeverityText(outcome)
	record.AddAttributes(attribute.String("audit", "cao"), attribute.String("event", event),
		attribute.String("outcome", outcome), attribute.Bool("stored", stored))
	if stored {
		record.AddAttributes(attribute.Int64("sequence", sequence))
	}
	for _, attr := range attrs {
		record.AddAttributes(attribute.String(attr.Key, attrString(attr.Value)))
	}
	otel.GetLoggerProvider().Logger(instrumentationName).Emit(ctx, record)
}
