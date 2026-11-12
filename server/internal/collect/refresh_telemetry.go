package collect

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

func refreshTraceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

func admissionOutcome(admission Admission, err error) string {
	switch {
	case err != nil:
		return "failed"
	case admission.Duplicate:
		return "duplicate"
	case admission.Kind == IntentIgnore:
		return "ignored"
	case admission.Enqueued:
		return "queued"
	default:
		return "coalesced"
	}
}

func startRefreshTelemetry(ctx context.Context, stage, kind string) (context.Context, func(string, error)) {
	started := time.Now()
	name := telemetry.SpanCollectionRefresh
	if stage == "admission" {
		name = telemetry.SpanCollectionAdmission
	}
	ctx, span := telemetry.Tracer().Start(ctx, name)
	return ctx, func(outcome string, err error) {
		attrs := []attribute.KeyValue{
			attribute.String("cao_dashboard.collection.stage", stage),
			attribute.String("cao_dashboard.collection.kind", kind),
			attribute.String("cao_dashboard.collection.outcome", outcome),
		}
		span.SetAttributes(attrs...)
		if err != nil {
			span.SetStatus(codes.Error, "collection refresh failed")
		}
		meter := otel.Meter(backfillMeterName)
		if counter, err := meter.Int64Counter("cao_dashboard.collection.refresh.count"); err == nil {
			counter.Add(ctx, 1, metric.WithAttributes(attrs...))
		} else {
			workerLog.Printf("refresh counter creation failed")
		}
		if duration, err := meter.Float64Histogram("cao_dashboard.collection.refresh.duration", metric.WithUnit("s")); err == nil {
			duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attrs...))
		} else {
			workerLog.Printf("refresh duration creation failed")
		}
		span.End()
	}
}
