package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type recordingStore struct {
	events []operational.AuditEvent
	err    error
	append func(context.Context, operational.AuditEvent) (operational.AuditEvent, error)
}

func (r *recordingStore) AppendAudit(ctx context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
	if r.append != nil {
		return r.append(ctx, event)
	}
	if r.err != nil {
		return operational.AuditEvent{}, r.err
	}
	event.Sequence = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	return event, nil
}
func (r *recordingStore) ListAudit(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error) {
	return r.events, nil
}
func (r *recordingStore) VerifyAudit(context.Context, ...operational.AuditAnchor) (operational.AuditVerification, error) {
	return operational.AuditVerification{}, nil
}

func TestRecordStoresEventBeforeMirroring(t *testing.T) {
	var buffer bytes.Buffer
	store := &recordingStore{}
	logger := New(&buffer).WithStore(store)
	if !logger.Durable() || New(&buffer).Durable() {
		t.Fatal("Durable must reflect the store sink")
	}
	at := time.Date(2026, 10, 14, 1, 2, 3, 0, time.FixedZone("x", 3600))
	if err := logger.Record(context.Background(), "token.minted", Success, slog.String("actor", "mona"),
		slog.Any("permissions", map[string]string{"contents": "write"}), slog.Time("expires_at", at), slog.Int64("installation_id", 7)); err != nil {
		t.Fatal(err)
	}
	stored := store.events[0]
	if stored.Actor != "mona" || stored.Attributes["permissions"] != `{"contents":"write"}` ||
		stored.Attributes["expires_at"] != "2026-10-14T00:02:03Z" || stored.Attributes["installation_id"] != "7" || stored.Attributes["actor"] != "" {
		t.Fatalf("stored event = %+v", stored)
	}
	var mirrored map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &mirrored); err != nil {
		t.Fatal(err)
	}
	if mirrored["stored"] != true || mirrored["sequence"] != float64(1) {
		t.Fatalf("mirrored event = %v", mirrored)
	}
}

func TestRecordReportsStorageFailureAndStillMirrors(t *testing.T) {
	var buffer bytes.Buffer
	logger := New(&buffer).WithStore(&recordingStore{err: errors.New("down")})
	if err := logger.Record(context.Background(), "cli_command.admitted", Success); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Contains(buffer.Bytes(), []byte(`"stored":false`)) {
		t.Fatalf("failure must be mirrored: %s", buffer.String())
	}
	var nilLogger *Logger
	if err := nilLogger.WithStore(&recordingStore{}).Record(context.Background(), "x", Success); err != nil {
		t.Fatal(err)
	}
}

func TestRecordDetachedCleanupHasFiniteBudget(t *testing.T) {
	caller, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	cancel()
	var buffer bytes.Buffer
	started := time.Now()
	logger := New(&buffer).WithStore(&recordingStore{
		append: func(ctx context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
			deadline, ok := ctx.Deadline()
			if !ok || ctx.Done() == nil || deadline.After(started.Add(operational.AuditWriteTimeout+time.Second)) {
				t.Fatal("detached audit persistence has no bounded deadline")
			}
			if ctx.Err() != nil {
				t.Fatal("cleanup inherited caller cancellation")
			}
			<-ctx.Done()
			return operational.AuditEvent{}, ctx.Err()
		},
	})
	err := logger.Record(caller, "token.revoke_failed", Failure)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline must remain observable: %v", err)
	}
	if elapsed := time.Since(started); elapsed > operational.AuditWriteTimeout+time.Second {
		t.Fatalf("audit exceeded its budget: %v", elapsed)
	}
	if !bytes.Contains(buffer.Bytes(), []byte(`"stored":false`)) {
		t.Fatal("timed-out storage was not mirrored as unconfirmed")
	}
}

func TestRecordAtomicHonorsCallerAndMirrorsCommittedEvent(t *testing.T) {
	var buffer bytes.Buffer
	store := &recordingStore{}
	logger := New(&buffer).WithStore(store)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	err := logger.RecordAtomic(ctx, "cli_command.admitted", Success,
		func(ctx context.Context, _ operational.AuditEvent) (operational.AuditEvent, error) {
			<-ctx.Done()
			return operational.AuditEvent{}, ctx.Err()
		})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mutation did not honor caller deadline: %v", err)
	}
	buffer.Reset()
	err = logger.RecordAtomic(t.Context(), "cli_command.admitted", Success,
		func(_ context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
			event.Sequence = 42
			event.Attributes = map[string]string{"command_id": "7"}
			return event, nil
		})
	if err != nil || len(store.events) != 0 {
		t.Fatalf("atomic event must not be appended again: %v", err)
	}
	if !bytes.Contains(buffer.Bytes(), []byte(`"sequence":42`)) || !bytes.Contains(buffer.Bytes(), []byte(`"command_id":"7"`)) {
		t.Fatalf("committed event not mirrored: %s", buffer.String())
	}
	if err := New(&buffer).RecordAtomic(t.Context(), "x", Success, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("atomic mutation without durable audit = %v", err)
	}
}

func TestRecordWritesStructuredEvent(t *testing.T) {
	var buffer bytes.Buffer
	if err := New(&buffer).Record(context.Background(), "cli_command.admitted", Success, slog.String("actor", "mona")); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["audit"] != "cao" || event["event"] != "cli_command.admitted" || event["outcome"] != Success || event["actor"] != "mona" || event["time"] == nil {
		t.Fatalf("unexpected event %v", event)
	}
	var nilLogger *Logger
	if err := nilLogger.Record(context.Background(), "ignored", Success); err != nil {
		t.Fatal(err)
	}
}

func TestFromEnvAppendsWithPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_AUDIT_LOG", path)
	logger, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(context.Background(), "admin.denied", Denied); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path) // #nosec G304 -- test temporary file
	if !bytes.HasPrefix(content, []byte("existing\n")) || !bytes.Contains(content, []byte(`"admin.denied"`)) {
		t.Fatalf("audit log must append: %q", content)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestWithStoredAttrsKeepsCallerValuesAndAddsMissing(t *testing.T) {
	caller := []slog.Attr{slog.String("command", "caller"), slog.Int("n", 1)}
	got := withStoredAttrs(caller, map[string]string{"command": "store", "queue": "q1"})
	values := map[string]string{}
	for _, attr := range got {
		values[attr.Key] = attr.Value.String()
	}
	if len(got) != 3 || values["command"] != "caller" || values["queue"] != "q1" || values["n"] != "1" {
		t.Fatalf("attrs = %v", got)
	}
}

func TestWithStoredAttrsWithoutStoreAttributes(t *testing.T) {
	caller := []slog.Attr{slog.String("a", "b")}
	if got := withStoredAttrs(caller, nil); len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("attrs = %v", got)
	}
}
