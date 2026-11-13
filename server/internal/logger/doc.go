// Package logger provides namespace-based debug logging copied from
// github/gh-aw/pkg/logger at e6374a3 and adapted to mirror enabled records to
// the global OpenTelemetry log provider while preserving stderr output.
//
// Set DEBUG to a comma-separated list of namespace patterns to enable logs:
//
//	DEBUG=cao:server,cao:query
//	DEBUG=cao:*
//	DEBUG=*,-cao:operational
//
// ACTIONS_RUNNER_DEBUG=true enables all namespaces when DEBUG is unset.
// OTLP export additionally requires telemetry.Setup, CAO_OTEL_LOGS_ENABLED=true,
// and OTEL_EXPORTER_OTLP_LOGS_ENDPOINT or OTEL_EXPORTER_OTLP_ENDPOINT.
package logger
