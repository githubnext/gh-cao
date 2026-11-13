package postgresx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

type existingQueryTracer struct {
	starts, ends, copyStarts, copyEnds   int
	batchStarts, batchQueries, batchEnds int
}

func (t *existingQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	t.starts++
	return ctx
}
func (t *existingQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
	t.ends++
}

func (t *existingQueryTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	t.copyStarts++
	return ctx
}

func (t *existingQueryTracer) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {
	t.copyEnds++
}

func (t *existingQueryTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	t.batchStarts++
	return ctx
}

func (t *existingQueryTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {
	t.batchQueries++
}

func (t *existingQueryTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {
	t.batchEnds++
}

func installPostgresTelemetry(t *testing.T) (*tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
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
	return exporter, reader
}

func TestPostgresTelemetryPreservesTracerAndOmitsSQL(t *testing.T) {
	exporter, reader := installPostgresTelemetry(t)
	existing := &existingQueryTracer{}
	config, err := pgx.ParseConfig("postgres://localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = existing
	instrumented, err := instrumentConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if config.Tracer != existing {
		t.Fatal("caller configuration was mutated")
	}
	const privateSQL = "SELECT secret FROM sensitive_table WHERE token = $1"
	const privateArg = "private-credential"
	for _, test := range []struct {
		statement string
		err       error
	}{
		{privateSQL, nil},
		{"INSERT INTO sensitive_table VALUES ($1)", errors.New(privateArg)},
	} {
		ctx := instrumented.Tracer.TraceQueryStart(t.Context(), nil,
			pgx.TraceQueryStartData{SQL: test.statement, Args: []any{privateArg}})
		instrumented.Tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: test.err})
	}
	if existing.starts != 2 || existing.ends != 2 {
		t.Fatalf("prior tracer lost events: starts=%d ends=%d", existing.starts, existing.ends)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	for i, span := range spans {
		if span.Name != telemetry.SpanPostgresQuery || span.SpanKind != trace.SpanKindClient {
			t.Fatalf("unexpected span: %+v", span)
		}
		if i == 1 && (span.Status.Code != codes.Error || span.Status.Description != "postgres query failed") {
			t.Fatalf("unsanitized or missing failure status: %+v", span.Status)
		}
		for _, value := range span.Attributes {
			if strings.Contains(value.Value.String(), privateSQL) ||
				strings.Contains(value.Value.String(), privateArg) ||
				strings.Contains(value.Value.String(), "sensitive_table") {
				t.Fatalf("SQL or argument leaked in span attribute %s", value.Key)
			}
		}
	}
	var measured metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &measured); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	durations := map[string]float64{}
	standard := map[string]float64{}
	for _, scope := range measured.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch data := recorded.Data.(type) {
			case metricdata.Sum[int64]:
				if recorded.Name != postgresQueryCount {
					continue
				}
				for _, point := range data.DataPoints {
					op, _ := point.Attributes.Value(attribute.Key("db.operation.name"))
					outcome, _ := point.Attributes.Value(attribute.Key("cao_dashboard.postgres.outcome"))
					counts[op.AsString()+"/"+outcome.AsString()] += point.Value
				}
			case metricdata.Histogram[float64]:
				if recorded.Name == "db.client.operation.duration" {
					if recorded.Unit != "s" {
						t.Fatalf("standard duration unit = %q, want s", recorded.Unit)
					}
					for _, point := range data.DataPoints {
						op, _ := point.Attributes.Value(attribute.Key("db.operation.name"))
						system, _ := point.Attributes.Value(attribute.Key("db.system.name"))
						errorType, failed := point.Attributes.Value(attribute.Key("error.type"))
						if system.AsString() != "postgresql" || (failed && errorType.AsString() != "_OTHER") ||
							point.Count != 1 || !slices.Equal(point.Bounds, []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}) {
							t.Fatalf("invalid standard duration: %+v", point)
						}
						standard[op.AsString()] = point.Sum
					}
				}
				if recorded.Name != postgresQueryDuration {
					continue
				}
				for _, point := range data.DataPoints {
					op, _ := point.Attributes.Value(attribute.Key("db.operation.name"))
					outcome, _ := point.Attributes.Value(attribute.Key("cao_dashboard.postgres.outcome"))
					if point.Count != 1 || point.Sum < 0 {
						t.Fatalf("invalid duration point: %+v", point)
					}
					durations[op.AsString()+"/"+outcome.AsString()] = point.Sum
				}
			}
		}
	}
	if counts["select/success"] != 1 || counts["insert/error"] != 1 ||
		len(counts) != 2 || len(durations) != 2 {
		t.Fatalf("missing postgres metrics: counts=%v durations=%v", counts, durations)
	}
	if len(standard) != 2 || standard["SELECT"] != durations["select/success"] || standard["INSERT"] != durations["insert/error"] {
		t.Fatalf("standard metrics disagree with existing durations: standard=%v legacy=%v", standard, durations)
	}
	for _, span := range spans {
		attrs := attribute.NewSet(span.Attributes...)
		op, _ := attrs.Value(attribute.Key("db.operation.name"))
		if standard[op.AsString()] != span.EndTime.Sub(span.StartTime).Seconds() {
			t.Fatal("standard metric duration must match the client span duration")
		}
	}
}

func TestPostgresCopyTelemetryAndErrorCodes(t *testing.T) {
	exporter, reader := installPostgresTelemetry(t)
	existing := &existingQueryTracer{}
	config, err := pgx.ParseConfig("postgres://localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = existing
	config, err = instrumentConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	copier, ok := config.Tracer.(pgx.CopyFromTracer)
	if !ok {
		t.Fatal("COPY instrumentation is missing")
	}
	for _, failure := range []error{nil, &pgconn.PgError{Code: "23505", Message: "private database detail"}} {
		ctx := copier.TraceCopyFromStart(t.Context(), nil, pgx.TraceCopyFromStartData{
			TableName: pgx.Identifier{"private_table"}, ColumnNames: []string{"private_column"},
		})
		copier.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{Err: failure})
	}
	if existing.copyStarts != 2 || existing.copyEnds != 2 {
		t.Fatal("existing COPY tracer was not preserved")
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[0].Status.Code != codes.Ok || spans[1].Status.Code != codes.Error {
		t.Fatalf("unexpected COPY spans: %+v", spans)
	}
	attrs := attribute.NewSet(spans[1].Attributes...)
	for key, want := range map[string]string{
		"db.operation.name": "COPY", "db.response.status_code": "23505", "error.type": "23505",
	} {
		value, _ := attrs.Value(attribute.Key(key))
		if value.AsString() != want {
			t.Fatalf("%s = %q, want %q", key, value.AsString(), want)
		}
	}
	var measured metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &measured); err != nil {
		t.Fatal(err)
	}
	var copyCount uint64
	for _, scope := range measured.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name == "db.client.operation.duration" {
				for _, point := range recorded.Data.(metricdata.Histogram[float64]).DataPoints {
					copyCount += point.Count
				}
			}
		}
	}
	if copyCount != 2 {
		t.Fatalf("COPY duration samples = %d, want 2", copyCount)
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, &pgconn.PgError{Code: "private error"}} {
		for _, attr := range postgresErrorAttributes(failure) {
			if strings.Contains(attr.Value.AsString(), "private") {
				t.Fatal("unrecognized database error leaked into telemetry")
			}
		}
	}
}

func TestPostgresOperationIsBounded(t *testing.T) {
	for statement, expected := range map[string]string{
		" SELECT secret FROM table": "select",
		"UPDATE hidden SET x = 1":   "update",
		"WITH private AS (...)":     "other",
		"DROP TABLE private":        "other",
		"private_input":             "other",
		"":                          "other",
	} {
		if got := postgresOperation(statement); got != expected {
			t.Errorf("postgresOperation(%q) = %q, want %q", statement, got, expected)
		}
	}
}

func TestPostgresBatchTelemetryCountsStatementsAndTimesBatch(t *testing.T) {
	exporter, reader := installPostgresTelemetry(t)
	existing := &existingQueryTracer{}
	config, err := pgx.ParseConfig("postgres://localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = existing
	config, err = instrumentConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	batcher, ok := config.Tracer.(pgx.BatchTracer)
	if !ok {
		t.Fatal("batch instrumentation is missing")
	}
	failure := &pgconn.PgError{Code: "23505", Message: "private database detail"}
	for _, batchError := range []error{nil, failure} {
		batch := &pgx.Batch{}
		batch.Queue("SELECT private FROM sensitive_table WHERE token=$1", "private-credential")
		batch.Queue("DELETE FROM sensitive_table WHERE token=$1", "private-credential")
		ctx := batcher.TraceBatchStart(t.Context(), nil, pgx.TraceBatchStartData{Batch: batch})
		for i, queued := range batch.QueuedQueries {
			queryError := batchError
			if i == 0 {
				queryError = nil
			}
			batcher.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{
				SQL: queued.SQL, Args: queued.Arguments, Err: queryError,
			})
		}
		batcher.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{Err: batchError})
	}
	if existing.batchStarts != 2 || existing.batchQueries != 4 || existing.batchEnds != 2 {
		t.Fatalf("existing batch tracer lost events: %+v", existing)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[0].Status.Code != codes.Ok || spans[1].Status.Code != codes.Error {
		t.Fatalf("batch spans = %+v", spans)
	}
	for _, span := range spans {
		attrs := attribute.NewSet(span.Attributes...)
		operation, _ := attrs.Value(attribute.Key("db.operation.name"))
		if operation.AsString() != "BATCH" {
			t.Fatalf("batch operation = %q", operation.AsString())
		}
		for _, value := range span.Attributes {
			if strings.Contains(value.Value.String(), "private") || strings.Contains(value.Value.String(), "sensitive") {
				t.Fatalf("batch telemetry leaked SQL, arguments or error text: %s", value.Key)
			}
		}
	}
	var measured metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &measured); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	var durations uint64
	for _, scope := range measured.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch recorded.Name {
			case postgresQueryCount:
				for _, point := range recorded.Data.(metricdata.Sum[int64]).DataPoints {
					op, _ := point.Attributes.Value(attribute.Key("db.operation.name"))
					outcome, _ := point.Attributes.Value(attribute.Key("cao_dashboard.postgres.outcome"))
					counts[op.AsString()+"/"+outcome.AsString()] += point.Value
				}
			case postgresQueryDuration:
				for _, point := range recorded.Data.(metricdata.Histogram[float64]).DataPoints {
					op, _ := point.Attributes.Value(attribute.Key("db.operation.name"))
					if op.AsString() != "batch" || point.Sum < 0 {
						t.Fatalf("invalid batch duration: %+v", point)
					}
					durations += point.Count
				}
			}
		}
	}
	if counts["select/success"] != 2 || counts["delete/success"] != 1 || counts["delete/error"] != 1 || len(counts) != 3 || durations != 2 {
		t.Fatalf("batch metrics lost or double-counted work: counts=%v duration samples=%d", counts, durations)
	}
}
