package server

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var queryCacheLog = logger.New("cao:server:query-cache")

const queryCacheMetricPrefix = "cao_dashboard.query.cache."

type queryCacheTelemetry struct {
	operations metric.Int64Counter
	duration   metric.Float64Histogram
	payload    metric.Int64Histogram
	memory     metric.Int64Gauge
	entries    metric.Int64Gauge
	retired    metric.Int64Counter
}

func (a *App) cacheTelemetry() (*queryCacheTelemetry, error) {
	a.cacheOnce.Do(func() {
		a.cacheMetrics, a.cacheError = newQueryCacheTelemetry()
	})
	return a.cacheMetrics, a.cacheError
}

func newQueryCacheTelemetry() (*queryCacheTelemetry, error) {
	meter := otel.Meter("github.com/githubnext/gh-cao/server")
	instruments := &queryCacheTelemetry{}
	var err error
	instruments.operations, err = meter.Int64Counter(queryCacheMetricPrefix+"operations",
		metric.WithUnit("{operation}"), metric.WithDescription("Query cache lookups, admissions and bypass decisions by fixed outcome"))
	if err != nil {
		return nil, err
	}
	instruments.duration, err = meter.Float64Histogram(queryCacheMetricPrefix+"duration",
		metric.WithUnit("s"), metric.WithDescription("Query cache operation latency including encoding or decoding"))
	if err != nil {
		return nil, err
	}
	instruments.payload, err = meter.Int64Histogram(queryCacheMetricPrefix+"result.bytes",
		metric.WithUnit("By"), metric.WithDescription("Compact query cache document size"))
	if err != nil {
		return nil, err
	}
	instruments.memory, err = meter.Int64Gauge(queryCacheMetricPrefix+"memory.bytes",
		metric.WithUnit("By"), metric.WithDescription("Last allocator-reported Redis result and index memory"))
	if err != nil {
		return nil, err
	}
	instruments.entries, err = meter.Int64Gauge(queryCacheMetricPrefix+"entries",
		metric.WithUnit("{entry}"), metric.WithDescription("Last Redis query cache entry count"))
	if err != nil {
		return nil, err
	}
	instruments.retired, err = meter.Int64Counter(queryCacheMetricPrefix+"retired",
		metric.WithUnit("{entry}"), metric.WithDescription("Query cache entries retired by expiry or oldest-first eviction"))
	if err != nil {
		return nil, err
	}
	return instruments, nil
}

// Only fixed decisions and numeric resource measurements cross this boundary.
// Never add query identities, fields, parameters, result values, or Redis errors.
func (instruments *queryCacheTelemetry) observe(
	ctx context.Context, span trace.Span, operation, outcome string,
	started time.Time, payloadBytes int, stats *operational.QueryCacheStats,
) {
	duration := time.Since(started)
	attributes := []attribute.KeyValue{
		attribute.String(queryCacheMetricPrefix+"operation", operation),
		attribute.String(queryCacheMetricPrefix+"outcome", outcome),
	}
	options := metric.WithAttributes(attributes...)
	instruments.operations.Add(ctx, 1, options)
	instruments.duration.Record(ctx, duration.Seconds(), options)
	if payloadBytes > 0 {
		instruments.payload.Record(ctx, int64(payloadBytes), options)
		attributes = append(attributes, attribute.Int(queryCacheMetricPrefix+"result.bytes", payloadBytes))
	}
	if stats != nil {
		instruments.memory.Record(ctx, stats.MemoryBytes)
		instruments.entries.Record(ctx, stats.Entries)
		for reason, count := range map[string]int64{"expired": stats.Expired, "evicted": stats.Evicted} {
			if count > 0 {
				instruments.retired.Add(ctx, count, metric.WithAttributes(attribute.String(queryCacheMetricPrefix+"reason", reason)))
			}
		}
		attributes = append(attributes,
			attribute.Int64(queryCacheMetricPrefix+"memory.bytes", stats.MemoryBytes),
			attribute.Int64(queryCacheMetricPrefix+"entries", stats.Entries),
			attribute.Int64(queryCacheMetricPrefix+"expired", stats.Expired),
			attribute.Int64(queryCacheMetricPrefix+"evicted", stats.Evicted),
		)
		queryCacheLog.PrintfContext(ctx, "operation=%s outcome=%s duration_ms=%d result_bytes=%d memory_bytes=%d entries=%d expired=%d evicted=%d",
			operation, outcome, duration.Milliseconds(), payloadBytes, stats.MemoryBytes, stats.Entries, stats.Expired, stats.Evicted)
	} else {
		queryCacheLog.PrintfContext(ctx, "operation=%s outcome=%s duration_ms=%d result_bytes=%d",
			operation, outcome, duration.Milliseconds(), payloadBytes)
	}
	if span != nil {
		span.SetAttributes(attributes...)
		if outcome == "error" {
			span.SetStatus(codes.Error, "query cache operation failed")
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}
}

func (a *App) recordQueryCacheBypass(ctx context.Context, reason string) error {
	instruments, err := a.cacheTelemetry()
	if err != nil {
		return fmt.Errorf("initialize query cache telemetry: %w", err)
	}
	instruments.observe(ctx, nil, "bypass", reason, time.Now(), 0, nil)
	return nil
}
