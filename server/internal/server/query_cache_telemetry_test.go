package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestQueryCacheTelemetryTracksUsageWithoutPrivateContent(t *testing.T) {
	previousTracer, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	exporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(t.Context())
		_ = traceProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})
	const secret = "private-query-content"
	client := &queryCacheClient{
		stats: operational.QueryCacheStats{MemoryBytes: 4096, Entries: 2, Expired: 1, Evicted: 2},
	}
	app := &App{services: newTestOperationalStore(t, client).OperationalServices()}
	config, err := (QueryCacheConfig{}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	key, err := app.queryCacheIdentity(queryRequest{SourceNames: []string{secret}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, hit, err := app.loadCachedQuery(t.Context(), key, config); err != nil || hit {
		t.Fatalf("cache miss: hit=%t err=%v", hit, err)
	}
	result := compactQueryResult()
	result.Sources[secret] = model.Source{Source: secret, Rows: []model.Row{{secret: secret}}}
	if err := app.storeCachedQuery(t.Context(), key, result, config); err != nil {
		t.Fatal(err)
	}
	client.value = string(client.put)
	if _, hit, err := app.loadCachedQuery(t.Context(), key, config); err != nil || !hit {
		t.Fatalf("cache hit: hit=%t err=%v", hit, err)
	}
	small := config
	small.MaxResultBytes = 1
	if err := app.storeCachedQuery(t.Context(), key, result, small); err != nil {
		t.Fatal(err)
	}
	client.err = errors.New(secret)
	if _, _, err := app.loadCachedQuery(t.Context(), key, config); err == nil {
		t.Fatal("cache error was hidden")
	}
	for _, reason := range []string{"disabled", "cheap-query"} {
		if err := app.recordQueryCacheBypass(t.Context(), reason); err != nil {
			t.Fatal(err)
		}
	}

	spans := exporter.GetSpans()
	if len(spans) != 5 {
		t.Fatalf("cache spans=%d, want 5", len(spans))
	}
	wantNames := []string{
		telemetry.SpanQueryCacheLookup, telemetry.SpanQueryCacheStore, telemetry.SpanQueryCacheLookup,
		telemetry.SpanQueryCacheStore, telemetry.SpanQueryCacheLookup,
	}
	for i, span := range spans {
		if span.Name != wantNames[i] {
			t.Fatalf("span %d name=%q, want %q", i, span.Name, wantNames[i])
		}
		if i == 4 && (span.Status.Code != codes.Error || span.Status.Description != "query cache operation failed") {
			t.Fatalf("cache failure status was missing or unsafe: %+v", span.Status)
		}
		encoded, err := json.Marshal(span.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{secret, key, "private-namespace"} {
			if strings.Contains(string(encoded), private) || strings.Contains(span.Status.Description, private) {
				t.Fatalf("cache span exposed private content: %q", private)
			}
		}
		if len(span.Events) != 0 {
			t.Fatal("cache spans must not record raw errors or query events")
		}
	}

	var measured metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &measured); err != nil {
		t.Fatal(err)
	}
	operations := map[string]int64{}
	retired := map[string]int64{}
	gauges := map[string]int64{}
	histograms := map[string]bool{}
	for _, scope := range measured.ScopeMetrics {
		for _, measured := range scope.Metrics {
			switch data := measured.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					for _, attr := range point.Attributes.ToSlice() {
						if strings.Contains(attr.Value.String(), secret) || strings.Contains(attr.Value.String(), key) {
							t.Fatal("cache metrics exposed private content")
						}
					}
					switch measured.Name {
					case queryCacheMetricPrefix + "operations":
						operation, _ := point.Attributes.Value(attribute.Key(queryCacheMetricPrefix + "operation"))
						outcome, _ := point.Attributes.Value(attribute.Key(queryCacheMetricPrefix + "outcome"))
						operations[operation.AsString()+"/"+outcome.AsString()] = point.Value
					case queryCacheMetricPrefix + "retired":
						reason, _ := point.Attributes.Value(attribute.Key(queryCacheMetricPrefix + "reason"))
						retired[reason.AsString()] = point.Value
					}
				}
			case metricdata.Gauge[int64]:
				if len(data.DataPoints) != 1 {
					t.Fatalf("gauge %s has unexpected cardinality", measured.Name)
				}
				gauges[measured.Name] = data.DataPoints[0].Value
			case metricdata.Histogram[float64]:
				histograms[measured.Name] = len(data.DataPoints) > 0
			case metricdata.Histogram[int64]:
				histograms[measured.Name] = len(data.DataPoints) > 0
			}
		}
	}
	expected := map[string]int64{
		"lookup/miss": 1, "lookup/hit": 1, "lookup/error": 1,
		"admission/stored": 1, "admission/result-size": 1,
		"bypass/disabled": 1, "bypass/cheap-query": 1,
	}
	if !reflect.DeepEqual(operations, expected) {
		t.Fatalf("cache operation metrics: got=%v want=%v", operations, expected)
	}
	if retired["expired"] != 3 || retired["evicted"] != 6 ||
		gauges[queryCacheMetricPrefix+"memory.bytes"] != 4096 ||
		gauges[queryCacheMetricPrefix+"entries"] != 2 ||
		!histograms[queryCacheMetricPrefix+"duration"] || !histograms[queryCacheMetricPrefix+"result.bytes"] {
		t.Fatalf("missing cache resource metrics: retired=%v gauges=%v histograms=%v", retired, gauges, histograms)
	}
}
