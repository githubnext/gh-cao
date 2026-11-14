// Package telemetry configures standardized OpenTelemetry tracing for the
// dashboard server. It intentionally follows upstream OpenTelemetry
// conventions rather than a vendor-specific SDK: the exporter, protocol, and
// resource attributes are all controlled through the standard OTEL_*
// environment variables (https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/),
// span identifiers are W3C Trace Context trace/span ids, and HTTP server
// spans use the semantic conventions implemented by
// go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp. Deployments
// that need Azure Monitor / Application Insights ingestion point
// OTEL_EXPORTER_OTLP_ENDPOINT at an OpenTelemetry Collector configured with
// the azuremonitorexporter; the server never links an Azure-specific SDK.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

// ServiceName is the standardized service.name resource attribute reported by
// every dashboard server process, regardless of hosting mode.
const ServiceName = "cao-dashboard"

var setupLog = logger.New("cao:telemetry")

// Shutdown flushes and stops any exporter started by Setup. It is always
// non-nil and safe to call even when Setup did not configure an exporter.
type Shutdown func(context.Context) error

// exporterDecision identifies why Setup did or did not start an OTLP signal
// exporter. It is useful for diagnosing a deployment that unexpectedly has
// no exported telemetry, without logging endpoint, header, or version values.
type exporterDecision string

const (
	exporterDecisionDisabled   exporterDecision = "sdk-disabled"
	exporterDecisionNoEndpoint exporterDecision = "no-endpoint"
	exporterDecisionConfigured exporterDecision = "configured"
)

// resolveExporterDecision applies the standard priority for whether Setup
// starts a real OTLP signal exporter: an explicit OTEL_SDK_DISABLED override,
// then a signal-specific endpoint, then the general OTLP endpoint. It returns the
// resolved endpoint (empty unless exporterDecisionConfigured) and which
// input decided the outcome, so callers can log the decision without
// exposing the endpoint value. It is a pure function so this priority is
// testable without installing a global TracerProvider.
func resolveExporterDecision(sdkDisabledEnv, signalEndpointEnv, endpointEnv string) (string, exporterDecision) {
	if strings.EqualFold(strings.TrimSpace(sdkDisabledEnv), "true") {
		return "", exporterDecisionDisabled
	}
	endpoint := firstNonEmpty(signalEndpointEnv, endpointEnv)
	if endpoint == "" {
		return "", exporterDecisionNoEndpoint
	}
	return endpoint, exporterDecisionConfigured
}

// Setup installs global trace, metric, and opt-in log providers and a W3C Trace
// Context propagator for the dashboard server. Each signal requires its standard
// OTLP endpoint or the shared endpoint; logs also require CAO_OTEL_LOGS_ENABLED.
// When OTEL_SDK_DISABLED is "true" or no OTLP endpoint is configured, the
// global propagator is still installed but no exporter is started.
func Setup(ctx context.Context, version string) (Shutdown, error) {
	noop := func(context.Context) error { return nil }
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))
	traceEndpoint, traceDecision := resolveExporterDecision(
		os.Getenv("OTEL_SDK_DISABLED"),
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	metricEndpoint, metricDecision := resolveExporterDecision(
		os.Getenv("OTEL_SDK_DISABLED"),
		os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	logEndpoint, logDecision := resolveExporterDecision(
		os.Getenv("OTEL_SDK_DISABLED"),
		os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CAO_OTEL_LOGS_ENABLED")), "true") {
		logDecision = exporterDecisionDisabled
	}
	setupLog.Printf("telemetry setup traces=%s metrics=%s logs=%s", traceDecision, metricDecision, logDecision)
	if traceDecision != exporterDecisionConfigured && metricDecision != exporterDecisionConfigured && logDecision != exporterDecisionConfigured {
		return noop, nil
	}
	serviceName := firstNonEmpty(os.Getenv("OTEL_SERVICE_NAME"), ServiceName)
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(version),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return noop, fmt.Errorf("build OpenTelemetry resource: %w", err)
	}

	var traceProvider *sdktrace.TracerProvider
	if traceDecision == exporterDecisionConfigured {
		var options []otlptracehttp.Option
		if strings.HasPrefix(traceEndpoint, "http://") {
			options = append(options, otlptracehttp.WithInsecure())
		}
		exporter, exporterErr := otlptracehttp.New(ctx, options...)
		if exporterErr != nil {
			return noop, fmt.Errorf("create OTLP trace exporter: %w", exporterErr)
		}
		traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
	}

	var meterProvider *sdkmetric.MeterProvider
	if metricDecision == exporterDecisionConfigured {
		var options []otlpmetrichttp.Option
		if strings.HasPrefix(metricEndpoint, "http://") {
			options = append(options, otlpmetrichttp.WithInsecure())
		}
		exporter, exporterErr := otlpmetrichttp.New(ctx, options...)
		if exporterErr != nil {
			if traceProvider != nil {
				_ = traceProvider.Shutdown(ctx)
			}
			return noop, fmt.Errorf("create OTLP metric exporter: %w", exporterErr)
		}
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
			sdkmetric.WithResource(res),
		)
		if runtimeErr := otelruntime.Start(otelruntime.WithMeterProvider(meterProvider)); runtimeErr != nil {
			_ = meterProvider.Shutdown(ctx)
			if traceProvider != nil {
				_ = traceProvider.Shutdown(ctx)
			}
			return noop, fmt.Errorf("start Go runtime metric instrumentation: %w", runtimeErr)
		}
	}

	var logProvider *sdklog.LoggerProvider
	if logDecision == exporterDecisionConfigured {
		var options []otlploghttp.Option
		if strings.HasPrefix(logEndpoint, "http://") {
			options = append(options, otlploghttp.WithInsecure())
		}
		exporter, exporterErr := otlploghttp.New(ctx, options...)
		if exporterErr != nil {
			if meterProvider != nil {
				_ = meterProvider.Shutdown(ctx)
			}
			if traceProvider != nil {
				_ = traceProvider.Shutdown(ctx)
			}
			return noop, fmt.Errorf("create OTLP log exporter: %w", exporterErr)
		}
		logProvider = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
			sdklog.WithResource(res),
		)
	}

	if traceProvider != nil {
		otel.SetTracerProvider(traceProvider)
	}
	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}
	if logProvider != nil {
		otel.SetLoggerProvider(logProvider)
	}
	return func(shutdownCtx context.Context) error {
		var shutdowns []Shutdown
		if logProvider != nil {
			shutdowns = append(shutdowns, logProvider.Shutdown)
		}
		if meterProvider != nil {
			shutdowns = append(shutdowns, meterProvider.Shutdown)
		}
		if traceProvider != nil {
			shutdowns = append(shutdowns, traceProvider.Shutdown)
		}
		return shutdownExporters(shutdownCtx, shutdowns...)
	}, nil
}

func shutdownExporters(ctx context.Context, shutdowns ...Shutdown) error {
	var group sync.WaitGroup
	results := make([]error, len(shutdowns))
	for i, shutdown := range shutdowns {
		group.Add(1)
		go func() {
			defer group.Done()
			results[i] = shutdown(ctx)
		}()
	}
	group.Wait()
	return errors.Join(results...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
