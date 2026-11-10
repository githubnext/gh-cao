package logger

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

// Logger represents a debug logger for a specific namespace.
type Logger struct {
	namespace string
	enabled   bool
	lastLog   time.Time
	mu        sync.Mutex
	label     string
}

const colorPaletteSize = 12

var (
	// DEBUG is read once at initialization, matching the gh-aw logger.
	debugEnv = initDebugEnv()

	debugColors = os.Getenv("DEBUG_COLORS") != "0"

	colorPalette = [colorPaletteSize]int{36, 32, 33, 35, 93, 31, 90, 37, 34, 36, 32, 35}
)

// New creates a Logger for namespace.
func New(namespace string) *Logger {
	return &Logger{
		namespace: namespace,
		enabled:   computeEnabled(namespace),
		lastLog:   time.Now(),
		label:     selectNamespaceLabel(namespace),
	}
}

// Enabled reports whether this logger is enabled.
func (l *Logger) Enabled() bool {
	return l.enabled
}

// Printf writes a formatted message when the logger is enabled.
//
//nolint:contextcheck // Background work may log without a caller context.
func (l *Logger) Printf(format string, args ...any) {
	l.PrintfContext(context.Background(), format, args...)
}

// PrintfContext writes a formatted message with the caller's trace context.
func (l *Logger) PrintfContext(ctx context.Context, format string, args ...any) {
	if !l.enabled {
		return
	}
	l.write(ctx, otelLog.SeverityDebug, fmt.Sprintf(format, args...))
}

// Print writes a message when the logger is enabled.
//
//nolint:contextcheck // Background work may log without a caller context.
func (l *Logger) Print(args ...any) {
	l.PrintContext(context.Background(), args...)
}

// PrintContext writes a message with the caller's trace context.
func (l *Logger) PrintContext(ctx context.Context, args ...any) {
	if !l.enabled {
		return
	}
	l.write(ctx, otelLog.SeverityDebug, fmt.Sprint(args...))
}

func (l *Logger) write(ctx context.Context, severity otelLog.Severity, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	diff := now.Sub(l.lastLog)
	l.lastLog = now
	_, _ = fmt.Fprintf(stderrWriter(), "%s %s +%s\n", l.label, message, formatDuration(diff))
	if buffer := activeBuffer.Load(); buffer != nil {
		buffer.append(now, l.namespace, message)
	}

	var record otelLog.Record
	record.SetTimestamp(now)
	record.SetSeverity(severity)
	record.SetBody(attribute.StringValue(message))
	global.GetLoggerProvider().Logger(l.namespace).Emit(ctx, record)
}

func initDebugEnv() string {
	if debug := os.Getenv("DEBUG"); debug != "" {
		return debug
	}
	if os.Getenv("ACTIONS_RUNNER_DEBUG") == "true" {
		return "*"
	}
	return ""
}

func computeEnabled(namespace string) bool {
	enabled := false
	for _, rawPattern := range strings.Split(debugEnv, ",") {
		pattern := strings.TrimSpace(rawPattern)
		if exclude, ok := strings.CutPrefix(pattern, "-"); ok {
			if matchPattern(namespace, exclude) {
				return false
			}
			continue
		}
		if matchPattern(namespace, pattern) {
			enabled = true
		}
	}
	return enabled
}

func matchPattern(namespace, pattern string) bool {
	if pattern == "*" || pattern == namespace {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(namespace, prefix)
	}
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok {
		return strings.HasSuffix(namespace, suffix)
	}
	parts := strings.SplitN(pattern, "*", 2)
	return len(namespace) >= len(parts[0])+len(parts[1]) &&
		strings.HasPrefix(namespace, parts[0]) &&
		strings.HasSuffix(namespace, parts[1])
}

func selectNamespaceLabel(namespace string) string {
	if !debugColors || os.Getenv("NO_COLOR") != "" || !stderrIsTerminal() {
		return namespace
	}
	hash := fnv.New32a()
	if _, err := io.WriteString(hash, namespace); err != nil {
		return namespace
	}
	color := colorPalette[hash.Sum32()%colorPaletteSize]
	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", color, namespace)
}

func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func stderrWriter() io.Writer {
	return os.Stderr
}

func formatDuration(duration time.Duration) string {
	switch {
	case duration < time.Microsecond:
		return fmt.Sprintf("%dns", duration.Nanoseconds())
	case duration < time.Millisecond:
		return fmt.Sprintf("%dµs", duration.Microseconds())
	case duration < time.Second:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	case duration < time.Minute:
		return fmt.Sprintf("%.1fs", duration.Seconds())
	case duration < time.Hour:
		return fmt.Sprintf("%.1fm", duration.Minutes())
	default:
		return fmt.Sprintf("%.1fh", duration.Hours())
	}
}
