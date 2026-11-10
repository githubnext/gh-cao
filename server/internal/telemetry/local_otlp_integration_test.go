package telemetry

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

func TestLocalOTLPExport(t *testing.T) {
	for _, backend := range []struct {
		name string
		flag string
	}{
		{name: "aspire", flag: "CAO_LOCAL_ASPIRE_INTEGRATION"},
		{name: "lgtm", flag: "CAO_LOCAL_LGTM_INTEGRATION"},
	} {
		t.Run(backend.name, func(t *testing.T) {
			if os.Getenv(backend.flag) != "1" {
				t.Skip("set " + backend.flag + "=1 with the corresponding local Compose stack running")
			}
			testLocalOTLPExport(t, backend.name)
		})
	}
}

func testLocalOTLPExport(t *testing.T, backend string) {
	t.Helper()
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousLogger := global.GetLoggerProvider()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		global.SetLoggerProvider(previousLogger)
	})
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
		"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	serviceName := "cao-" + backend + "-integration-test"
	t.Setenv("OTEL_SERVICE_NAME", serviceName)
	t.Setenv("CAO_OTEL_LOGS_ENABLED", "true")

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	shutdown, err := Setup(ctx, "local-integration-test")
	if err != nil {
		t.Fatal("could not initialize local OTLP exporters")
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := shutdown(stopCtx); err != nil {
			t.Error("local OTLP export failed")
		}
	}()

	spanCtx, span := Tracer().Start(ctx, serviceName)
	defer span.End()
	if !span.SpanContext().IsValid() || !span.IsRecording() {
		t.Fatal("trace exporter is not recording")
	}
	counter, err := otel.GetMeterProvider().Meter(serviceName).
		Int64Counter("cao_dashboard.local_otlp_integration_test.count")
	if err != nil {
		t.Fatal("could not create local test metric")
	}
	counter.Add(spanCtx, 1)

	var record otelLog.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(otelLog.SeverityInfo)
	record.SetBody(attribute.StringValue("local OTLP integration test"))
	global.GetLoggerProvider().Logger("cao:local-otlp-test").Emit(spanCtx, record)
}
