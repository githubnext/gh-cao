package logger

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestPatterns(t *testing.T) {
	original := debugEnv
	t.Cleanup(func() { debugEnv = original })

	tests := []struct {
		pattern   string
		namespace string
		enabled   bool
	}{
		{"", "cao:server", false},
		{"*", "cao:server", true},
		{"cao:*", "cao:server", true},
		{"*:server", "cao:server", true},
		{"cao:*,-cao:redis", "cao:redis", false},
		{"cao:*,-cao:redis", "cao:query", true},
	}
	for _, test := range tests {
		debugEnv = test.pattern
		if enabled := New(test.namespace).Enabled(); enabled != test.enabled {
			t.Errorf("New(%q) with DEBUG=%q enabled=%v, want %v", test.namespace, test.pattern, enabled, test.enabled)
		}
	}
}

func TestOutput(t *testing.T) {
	originalDebug := debugEnv
	originalColors := debugColors
	debugEnv = "*"
	debugColors = false
	t.Cleanup(func() {
		debugEnv = originalDebug
		debugColors = originalColors
	})

	output := captureStderr(t, func() {
		New("cao:test").Printf("processed %d items", 3)
	})
	if !strings.Contains(output, "cao:test processed 3 items +") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestSlogAdapter(t *testing.T) {
	originalDebug := debugEnv
	originalColors := debugColors
	debugEnv = "*"
	debugColors = false
	t.Cleanup(func() {
		debugEnv = originalDebug
		debugColors = originalColors
	})

	handler := NewSlogHandler(New("cao:slog"))
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("enabled logger returned a disabled slog handler")
	}
	output := captureStderr(t, func() {
		NewSlogLoggerWithHandler(handler.logger).Info("request complete", "status", 200)
	})
	if !strings.Contains(output, "· request complete status=200") {
		t.Fatalf("unexpected slog output: %q", output)
	}
}

type recordExporter struct {
	records []sdklog.Record
}

func (e *recordExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range records {
		e.records = append(e.records, record.Clone())
	}
	return nil
}

func (*recordExporter) ForceFlush(context.Context) error { return nil }
func (*recordExporter) Shutdown(context.Context) error   { return nil }

func TestOTelLogsRespectDebugCategoriesAndSlogContext(t *testing.T) {
	originalDebug, originalColors := debugEnv, debugColors
	debugEnv, debugColors = "cao:allowed,-cao:blocked", false
	previousProvider := global.GetLoggerProvider()
	exporter := &recordExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	global.SetLoggerProvider(provider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousProvider)
		debugEnv, debugColors = originalDebug, originalColors
		_ = provider.Shutdown(context.Background())
	})

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	captureStderr(t, func() {
		New("cao:allowed").Printf("processed %d items", 3)
		New("cao:blocked").Print("not exported")
		New("cao:blocked").PrintfContext(ctx, "not exported")
		New("cao:blocked").PrintContext(ctx, "not exported")
		New("cao:allowed").PrintfContext(ctx, "handled %d items", 2)
		New("cao:allowed").PrintContext(ctx, "request complete")
		NewSlogLoggerWithHandler(New("cao:allowed")).ErrorContext(ctx, "operation failed", "status", 500)
		New("cao:allowed").Print("background complete")
	})
	if len(exporter.records) != 5 {
		t.Fatalf("exported %d records, want 5", len(exporter.records))
	}
	if got := exporter.records[0]; got.Body().AsString() != "processed 3 items" ||
		got.Severity() != otelLog.SeverityDebug || got.InstrumentationScope().Name != "cao:allowed" {
		t.Fatalf("unexpected debug record: body=%q severity=%v scope=%q",
			got.Body().AsString(), got.Severity(), got.InstrumentationScope().Name)
	}
	for index, message := range []string{"handled 2 items", "request complete"} {
		got := exporter.records[index+1]
		if got.Body().AsString() != message || got.Severity() != otelLog.SeverityDebug ||
			got.TraceID() != traceID || got.SpanID() != spanID || got.TraceFlags() != trace.FlagsSampled {
			t.Errorf("contextual debug record lost its message, severity, or trace context: %v", got)
		}
	}
	if got := exporter.records[3]; got.Body().AsString() != "✗ operation failed status=500" ||
		got.Severity() != otelLog.SeverityError || got.TraceID() != traceID || got.SpanID() != spanID {
		t.Fatalf("unexpected error record: body=%q severity=%v trace=%s span=%s",
			got.Body().AsString(), got.Severity(), got.TraceID(), got.SpanID())
	}
	for _, index := range []int{0, 4} {
		if got := exporter.records[index]; got.TraceID().IsValid() || got.SpanID().IsValid() {
			t.Error("context-free logging inherited another call's trace context")
		}
	}
	global.SetLoggerProvider(noop.NewLoggerProvider())
	captureStderr(t, func() { New("cao:allowed").Print("stderr only") })
	if len(exporter.records) != 5 {
		t.Fatal("disabled export must not forward records")
	}
}

func captureStderr(t *testing.T, run func()) string {
	t.Helper()
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	defer func() { os.Stderr = original }()

	run()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := output.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	return output.String()
}
