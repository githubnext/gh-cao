package sessionarchiveblob

import (
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func TestNativeSourceEpochVisibilityWithdrawalAndReenrollment(t *testing.T) {
	n, s := nativeProtocol(t)
	if err := n.RequireSourceFencing(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	next := sessionarchive.Source{Repository: "octo/api", InstallationID: 8, Epoch: 3}
	completed := s.now.Add(-time.Hour)
	source := nativeSource("approved source evidence")
	if err := n.CaptureSessionAt(t.Context(), old, "123", 1, completed, source, ""); err != nil {
		t.Fatal(err)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	if _, _, _, _, reader, err := n.pin(t.Context(), scope, "octo/api", "123", 1); !errors.Is(err, ErrScope) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatal("source-fenced archive allowed an unscoped read")
	}
	if _, _, _, _, reader, err := n.pinSource(t.Context(), scope, "octo/api", "123", 1, &next); !errors.Is(err, sessionarchive.ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatal("reenrollment exposed a previous source epoch before Blob cleanup")
	}
	pinned, _, _, members, reader, err := n.pinSource(t.Context(), scope, "octo/api", "123", 1, &old)
	if err != nil || len(members) != 1 {
		t.Fatal("current source could not pin its exact archive membership")
	}
	if err := n.WithdrawSource(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if pinned.Err() == nil || !errors.Is(reader.Deliver(func() error { t.Fatal("withdrawn source delivered"); return nil }), ErrWithdrawn) {
		t.Fatal("source tombstone did not override pinned access")
	}
	_ = reader.Close()
	generation := n.Status().Generation
	if err := n.WithdrawSource(t.Context(), old); err != nil || n.Status().Generation != generation {
		t.Fatal("outbox cleanup was not idempotent")
	}
	n = restartNative(t, n)
	if err := n.CaptureSessionAt(t.Context(), old, "456", 1, completed, source, ""); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("late old-source admission bypassed a recovered tombstone")
	}
	if err := n.CaptureSession(t.Context(), "octo/api", "456", 1, completed, source, ""); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("unscoped admission bypassed source fencing")
	}
	s.mu.Lock()
	s.now = s.now.Add(time.Second)
	s.mu.Unlock()
	if err := n.CaptureSessionAt(t.Context(), next, "123", 1, completed, source, ""); err != nil {
		t.Fatalf("fresh authorized reacquisition under reenrollment failed: %v", err)
	}
	_, _, files, members, reader, err := n.pinSource(t.Context(), scope, "octo/api", "123", 1, &next)
	if err != nil || len(files) != 1 || len(members) != 1 {
		t.Fatal("reenrollment did not select only its fresh source admission")
	}
	_ = reader.Close()
	if n.state.NativeRecords[0].Source != next || len(n.state.Retired) == 0 {
		t.Fatal("source identity or retired object inventory was lost")
	}
}

func TestNativeSourceProfileRejectsUnscopedArchiveMigration(t *testing.T) {
	n, s := nativeProtocol(t)
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, s.now.Add(-time.Hour), nativeSource("unscoped"), ""); err != nil {
		t.Fatal(err)
	}
	if err := n.RequireSourceFencing(t.Context()); !errors.Is(err, ErrConformance) {
		t.Fatal("unscoped old evidence was silently assigned a new source epoch")
	}
}

func TestNativeSourceWithdrawalFailureRemainsRetryableAfterRestart(t *testing.T) {
	n, s := nativeProtocol(t)
	if err := n.RequireSourceFencing(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	if err := n.CaptureSessionAt(t.Context(), source, "123", 1, s.now.Add(-time.Hour), nativeSource("approved"), ""); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.failKey = "active.json"
	s.mu.Unlock()
	if err := n.WithdrawSource(t.Context(), source); err == nil {
		t.Fatal("unavailable Blob falsely acknowledged the durable cleanup callback")
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	if _, _, _, _, reader, err := n.pinSource(t.Context(), scope, source.Repository, "123", 1, &source); !errors.Is(err, ErrLease) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatal("failed withdrawal left the lease owner serving stale evidence")
	}
	s.mu.Lock()
	s.failKey = ""
	s.mu.Unlock()
	n = restartNative(t, n)
	if err := n.WithdrawSource(t.Context(), source); err != nil {
		t.Fatalf("durable outbox redelivery could not finish cleanup after restart: %v", err)
	}
	n = restartNative(t, n)
	if err := n.CaptureSessionAt(t.Context(), source, "456", 1, s.now.Add(-time.Hour), nativeSource("late"), ""); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("completed cleanup lost its permanent old-source admission fence")
	}
	if n.Status().ActiveTraces != 0 || len(n.state.Retired) != 1 {
		t.Fatal("completed cleanup lost logical exclusion or durable physical retirement")
	}
}

func TestNativeSourceRepeatedCleanupCannotWithdrawReenrollment(t *testing.T) {
	n, s := nativeProtocol(t)
	if err := n.RequireSourceFencing(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	current := sessionarchive.Source{Repository: old.Repository, InstallationID: 8, Epoch: 3}
	// A gap is still source-owned evidence and must get a permanent tombstone,
	// even though it never published a Parquet member.
	if err := n.CaptureSessionAt(t.Context(), old, "123", 1, s.now.Add(-time.Hour), nil, "missing-session"); err == nil {
		t.Fatal("a gap was acknowledged as complete capture")
	}
	if err := n.WithdrawSource(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if err := n.CaptureSessionAt(t.Context(), current, "123", 1, s.now.Add(-time.Hour), nativeSource("fresh"), ""); err != nil {
		t.Fatal(err)
	}
	n = restartNative(t, n)
	generation := n.Status().Generation
	if err := n.WithdrawSource(t.Context(), old); err != nil || n.Status().Generation != generation {
		t.Fatal("redelivered old-epoch cleanup mutated the reenrolled generation")
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{current.Repository}}
	_, _, _, members, reader, err := n.pinSource(t.Context(), scope, current.Repository, "123", 1, &current)
	if err != nil || len(members) != 1 {
		t.Fatal("redelivered old-epoch cleanup revoked current-source evidence")
	}
	_ = reader.Close()
}
