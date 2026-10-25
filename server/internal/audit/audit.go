// Package audit records append-only, structured security audit events for
// administrator and hosted CLI operations. The authoritative trail is the
// operational store's tamper-evident audit table; every event is also mirrored
// as a JSON line carrying a stable "audit" marker and exported as an
// OpenTelemetry log record named cao.audit.<event>, so log pipelines can route
// it to a SIEM. Events carry the active trace_id and span_id. Events never contain credentials; installation tokens appear only
// as GitHub's hashed_token form so they correlate with the GitHub audit log.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var auditLog = logger.New("cao:audit")

// Outcomes recorded on every event.
const (
	Success = "success"
	Denied  = "denied"
	Failure = "failure"
)

// ErrUnavailable reports that an event could not be stored durably.
var ErrUnavailable = errors.New("audit trail unavailable")

// ErrMirrorUnavailable reports an unconfirmed JSON mirror write, not a failed
// durable append. A committed mutation must never be compensated for this.
var ErrMirrorUnavailable = errors.New("audit mirror unavailable")

// Logger records audit events. A nil *Logger discards events.
type Logger struct {
	mirror *mirror
	store  operational.AuditLog
}

// New writes JSON audit events to writer.
func New(writer io.Writer) *Logger {
	return &Logger{mirror: &mirror{writer: writer, active: make(chan struct{}, 1)}}
}

// WithStore returns a logger that appends every event to the durable audit
// table before mirroring it to the JSON stream. A nil receiver stores without
// mirroring.
func (l *Logger) WithStore(store operational.AuditLog) *Logger {
	if l == nil {
		return &Logger{store: store}
	}
	return &Logger{mirror: l.mirror, store: store}
}

// Durable reports whether events are persisted to the audit table.
func (l *Logger) Durable() bool { return l != nil && l.store != nil }

// FromEnv writes to the file named by CAO_AUDIT_LOG (opened append-only with
// mode 0600) or, when unset, to standard error.
func FromEnv() (*Logger, error) {
	path := strings.TrimSpace(os.Getenv("CAO_AUDIT_LOG"))
	if path == "" {
		return New(os.Stderr), nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 G703 -- operator-configured audit path
	if err != nil {
		return nil, errors.New("CAO_AUDIT_LOG could not be opened for appending")
	}
	return New(file), nil
}

// Record stores one event and mirrors it to the JSON stream. It returns
// ErrUnavailable when a durable store is configured and the append fails;
// callers guarding privileged work must then fail closed. The event is still
// mirrored, marked stored=false, so the failure itself remains observable.
// Mirror writes have a separate finite budget and return ErrMirrorUnavailable
// on backpressure or failure, even if durable persistence succeeded.
// Attribute values must not contain secrets.
func (l *Logger) Record(ctx context.Context, event, outcome string, attrs ...slog.Attr) error {
	if l == nil {
		return nil
	}
	var persist func(context.Context, operational.AuditEvent) (operational.AuditEvent, error)
	if l.store != nil {
		persist = l.store.AppendAudit
	}
	return l.record(context.WithoutCancel(ctx), event, outcome, persist, attrs...)
}

// RecordAtomic records a mutation through a store operation that commits the
// mutation and event together. Unlike cleanup Record, it honors caller
// cancellation. A commit error is uncertain, not proof of rollback: callers
// must not compensate or retry without the original idempotency key.
func (l *Logger) RecordAtomic(ctx context.Context, event, outcome string,
	persist func(context.Context, operational.AuditEvent) (operational.AuditEvent, error), attrs ...slog.Attr,
) error {
	if !l.Durable() || persist == nil {
		return ErrUnavailable
	}
	return l.record(ctx, event, outcome, persist, attrs...)
}

func (l *Logger) record(ctx context.Context, event, outcome string,
	persist func(context.Context, operational.AuditEvent) (operational.AuditEvent, error), attrs ...slog.Attr,
) error {
	attrs = append(attrs, traceAttrs(ctx)...)
	started := time.Now()
	var stored operational.AuditEvent
	var storeErr error
	if persist != nil {
		writeCtx, cancel := context.WithTimeout(ctx, operational.AuditWriteTimeout)
		defer cancel()
		stored, storeErr = persist(writeCtx, toEvent(event, outcome, attrs))
		if storeErr == nil {
			attrs = withStoredAttrs(attrs, stored.Attributes)
		} else {
			auditLog.Printf("audit append failed: event=%s outcome=%s", event, outcome)
		}
	}
	observe(context.WithoutCancel(ctx), event, outcome, persist != nil && storeErr == nil, stored.Sequence, started, persist != nil, attrs)
	var mirrorErr error
	if l.mirror != nil {
		fields := make([]slog.Attr, 0, len(attrs)+5)
		fields = append(fields, slog.String("audit", "cao"), slog.String("event", event), slog.String("outcome", outcome))
		switch {
		case storeErr != nil:
			fields = append(fields, slog.Bool("stored", false))
		case persist != nil:
			fields = append(fields, slog.Bool("stored", true), slog.Int64("sequence", stored.Sequence))
		}
		fields = append(fields, attrs...)
		mirrorErr = l.mirror.write(context.WithoutCancel(ctx), fields)
	}
	if storeErr != nil {
		storeErr = fmt.Errorf("%w: %s: %w", ErrUnavailable, event, storeErr)
	}
	return errors.Join(storeErr, mirrorErr)
}

// withStoredAttrs appends authoritative attributes added by the store (such as
// atomic queue command fields) that the caller did not already supply.
func withStoredAttrs(attrs []slog.Attr, stored map[string]string) []slog.Attr {
	known := make(map[string]bool, len(attrs))
	for _, attr := range attrs {
		known[attr.Key] = true
	}
	for key, value := range stored {
		if !known[key] {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	return attrs
}

func toEvent(event, outcome string, attrs []slog.Attr) operational.AuditEvent {
	result := operational.AuditEvent{Event: event, Outcome: outcome}
	for _, attr := range attrs {
		value := attrString(attr.Value)
		if attr.Key == "actor" {
			result.Actor = value
			continue
		}
		if result.Attributes == nil {
			result.Attributes = make(map[string]string, len(attrs))
		}
		result.Attributes[attr.Key] = value
	}
	return result
}

func attrString(value slog.Value) string {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindAny:
		if encoded, err := json.Marshal(value.Any()); err == nil {
			return string(encoded)
		}
	case slog.KindString, slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool,
		slog.KindDuration, slog.KindGroup, slog.KindLogValuer:
	}
	return value.String()
}
