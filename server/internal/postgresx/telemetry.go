package postgresx

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const (
	postgresQueryCount    = "cao_dashboard.postgres.query.count"
	postgresQueryDuration = "cao_dashboard.postgres.query.duration"
)

type queryTracer struct {
	count    metric.Int64Counter
	duration metric.Float64Histogram
	standard metric.Float64Histogram
}

type queryTraceKey struct{}

type queryTrace struct {
	started   time.Time
	operation string
	span      trace.Span
	attrs     []attribute.KeyValue
}

func instrumentConfig(config *pgx.ConnConfig) (*pgx.ConnConfig, error) {
	config = config.Copy()
	tracer, err := newQueryTracer()
	if err != nil {
		return nil, err
	}
	if config.Tracer == nil {
		config.Tracer = tracer
	} else {
		config.Tracer = multitracer.New(config.Tracer, tracer)
	}
	return config, nil
}

func newQueryTracer() (*queryTracer, error) {
	meter := otel.Meter("github.com/githubnext/gh-cao/server")
	count, err := meter.Int64Counter(postgresQueryCount,
		metric.WithUnit("{query}"),
		metric.WithDescription("Postgres statements by fixed operation and outcome"))
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram(postgresQueryDuration,
		metric.WithUnit("s"),
		metric.WithDescription("Postgres operation latency, excluding connection-pool wait; pipelined statements are timed as one batch"))
	if err != nil {
		return nil, err
	}
	standard, err := telemetry.NewDatabaseOperationDuration()
	if err != nil {
		return nil, err
	}
	return &queryTracer{count: count, duration: duration, standard: standard}, nil
}

func postgresOperation(statement string) string {
	statement = strings.TrimLeft(statement, " \t\r\n")
	end := 0
	for end < len(statement) && end < 7 && statement[end] >= 'A' && statement[end] <= 'Z' {
		end++
	}
	switch statement[:end] {
	case "SELECT":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	case "COPY":
		return "copy"
	default:
		return "other"
	}
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return t.start(ctx, postgresOperation(data.SQL))
}

func (t *queryTracer) start(ctx context.Context, operation string) context.Context {
	started := time.Now()
	attrs := []attribute.KeyValue{semconv.DBSystemNamePostgreSQL}
	if operation != "other" {
		attrs = append(attrs, semconv.DBOperationName(strings.ToUpper(operation)))
	}
	ctx, span := telemetry.Tracer().Start(ctx, telemetry.SpanPostgresQuery,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(started),
		trace.WithAttributes(attrs...))
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{started: started, operation: operation, span: span, attrs: attrs})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	t.finish(ctx, data.Err, true)
}

func (t *queryTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return t.start(ctx, "copy")
}

func (t *queryTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	t.finish(ctx, data.Err, true)
}

func (t *queryTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return t.start(ctx, "batch")
}

func (t *queryTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	outcome := "success"
	if data.Err != nil {
		outcome = "error"
	}
	t.count.Add(ctx, 1, metric.WithAttributes(
		attribute.String("db.system.name", "postgresql"),
		attribute.String("db.operation.name", postgresOperation(data.SQL)),
		attribute.String("cao_dashboard.postgres.outcome", outcome),
	))
}

func (t *queryTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	t.finish(ctx, data.Err, false)
}

func (t *queryTracer) finish(ctx context.Context, err error, countQuery bool) {
	state, ok := ctx.Value(queryTraceKey{}).(queryTrace)
	if !ok {
		return
	}
	ended := time.Now()
	duration := ended.Sub(state.started).Seconds()
	outcome := "success"
	if err != nil {
		outcome = "error"
		state.attrs = append(state.attrs, postgresErrorAttributes(err)...)
		state.span.SetAttributes(state.attrs...)
		state.span.SetStatus(codes.Error, "postgres query failed")
	} else {
		state.span.SetStatus(codes.Ok, "")
	}
	state.span.SetAttributes(attribute.String("cao_dashboard.postgres.outcome", outcome))
	state.span.End(trace.WithTimestamp(ended))
	attrs := metric.WithAttributes(
		attribute.String("db.system.name", "postgresql"),
		attribute.String("db.operation.name", state.operation),
		attribute.String("cao_dashboard.postgres.outcome", outcome),
	)
	if countQuery {
		t.count.Add(ctx, 1, attrs)
	}
	t.duration.Record(ctx, duration, attrs)
	t.standard.Record(ctx, duration, metric.WithAttributes(state.attrs...))
}

func postgresErrorAttributes(err error) []attribute.KeyValue {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && validSQLState(databaseError.Code) {
		return []attribute.KeyValue{
			semconv.DBResponseStatusCode(databaseError.Code),
			semconv.ErrorTypeKey.String(databaseError.Code),
		}
	}
	return []attribute.KeyValue{semconv.ErrorTypeKey.String(telemetry.DatabaseErrorType(err))}
}

func validSQLState(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, character := range code {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') {
			return false
		}
	}
	return true
}

var _ pgx.QueryTracer = (*queryTracer)(nil)
var _ pgx.CopyFromTracer = (*queryTracer)(nil)
var _ pgx.BatchTracer = (*queryTracer)(nil)
