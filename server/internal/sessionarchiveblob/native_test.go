package sessionarchiveblob

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func nativeCaptureConfig(t *testing.T) sessionarchive.Config {
	t.Helper()
	c := sessionarchive.Config{Enabled: true, NativeProducer: true, Namespace: "private",
		Repositories: []string{"octo/api"},
		Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"user.message": {"content"}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func nativeSource(message string) []byte {
	return []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
		"{\"type\":\"session.collection\",\"data\":{\"complete\":true}}\n" +
		"{\"type\":\"user.message\",\"data\":{\"content\":\"" + message + "\",\"token\":\"private-token\"}}\n")
}

func nativeProtocol(t *testing.T) (*Native, *blobProtocol) {
	t.Helper()
	a, s, _ := newProtocol(t)
	a.native = true
	a.converter = nil // Native publication must never consult the file verifier.
	return &Native{Archive: a, capture: nativeCaptureConfig(t)}, s
}

func restartNative(t *testing.T, previous *Native) *Native {
	t.Helper()
	if err := previous.Close(); err != nil {
		t.Fatal(err)
	}
	a := &Archive{native: true, config: previous.config, blob: previous.blob, queryHTTP: previous.queryHTTP, now: previous.now}
	if err := a.open(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return &Native{Archive: a, capture: previous.capture}
}

func TestNativeBlobCaptureCorrectionRestartWithdrawal(t *testing.T) {
	n, s := nativeProtocol(t)
	completed := s.now.Add(-time.Hour)
	source := nativeSource("approved private evidence")
	capture := func(content []byte) error {
		return n.CaptureSession(t.Context(), "octo/api", "123", 2, completed, content, "")
	}
	if err := capture(source); err != nil {
		t.Fatal(err)
	}
	if n.Status().ActiveTraces != 1 || n.Status().Generation != 1 || len(n.state.NativeRecords) != 1 {
		t.Fatal("native capture did not durably publish complete membership")
	}
	file := n.state.Manifest.Files[0]
	incomplete := n.state
	incomplete.NativeRecords = nil
	if err := n.validateState(incomplete); !errors.Is(err, ErrIntegrity) {
		t.Fatal("native state accepted membership without a durable admission receipt")
	}
	if file.Members[0].Attempt != 2 || !bytes.HasPrefix(s.objects[file.Path], []byte("PAR1")) {
		t.Fatal("exact attempt or real native Parquet was lost")
	}
	for key, object := range s.objects {
		if bytes.Contains(object, []byte("private-token")) || key == "active.json" && bytes.Contains(object, source) {
			t.Fatal("unapproved raw session escaped private projection")
		}
		if len(key) > 10 && key[:10] == "manifests/" && bytes.Contains(object, []byte("approved private evidence")) {
			t.Fatal("raw content entered receipt metadata")
		}
	}
	if err := capture(source); err != nil || n.Status().Generation != 1 {
		t.Fatalf("admission retry was not idempotent: %v", err)
	}
	s.mu.Lock()
	s.now = s.now.Add(time.Second)
	s.mu.Unlock()
	n = restartNative(t, n)
	if err := capture(source); err != nil || n.Status().Generation != 1 {
		t.Fatal("restart lost durable native receipt")
	}
	correction := nativeSource("corrected")
	if err := capture(correction); err != nil || n.Status().Generation != 2 {
		t.Fatalf("correction failed: %v", err)
	}
	if err := capture(source); !errors.Is(err, sessionarchive.ErrReplay) {
		t.Fatalf("superseded version resurrected: %v", err)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	readerCtx, _, _, _, reader, err := n.pin(t.Context(), scope, "octo/api", "123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Withdraw(t.Context(), "octo/api", "123", 2); err != nil {
		t.Fatal(err)
	}
	if readerCtx.Err() == nil || !errors.Is(reader.Deliver(func() error { t.Fatal("withdrawn delivery"); return nil }), ErrWithdrawn) {
		t.Fatal("withdrawal did not override pinned access")
	}
	_ = reader.Close()
	if err := capture(correction); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("withdrawn attempt admitted")
	}
	n = restartNative(t, n)
	if err := capture(correction); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("restart lost permanent tombstone")
	}
	if n.Status().ActiveTraces != 0 || len(n.state.NativeRecords) != 0 {
		t.Fatal("withdrawal retained active native receipt")
	}
}

func TestNativeBlobGapRetryCapacityRetentionAndIntegrity(t *testing.T) {
	n, s := nativeProtocol(t)
	completed := s.now.Add(-time.Hour)
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, nil, "missing-session"); err == nil || n.Status().Gaps != 1 {
		t.Fatal("source gap was acknowledged or not durably recorded")
	}
	n = restartNative(t, n)
	if n.Status().Gaps != 1 {
		t.Fatal("restart lost explicit gap")
	}
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, nativeSource("retry"), ""); err != nil {
		t.Fatal(err)
	}
	if n.Status().Gaps != 0 || n.Status().ActiveTraces != 1 {
		t.Fatal("retry did not replace gap with complete admission")
	}
	s.corruptKey = n.state.Manifest.Files[0].Path
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, nativeSource("retry"), ""); err == nil {
		t.Fatal("corrupt immutable receipt acknowledged")
	}
	s.corruptKey = ""
	n.capture.MaxEntries = 1
	if err := n.CaptureSession(t.Context(), "octo/api", "456", 1, completed, nil, "missing-session"); !errors.Is(err, sessionarchive.ErrCapacity) {
		t.Fatalf("metadata capacity ignored: %v", err)
	}
	s.mu.Lock()
	s.now = s.now.Add(31 * 24 * time.Hour)
	s.expires = s.now.Add(time.Minute)
	s.mu.Unlock()
	if err := n.Flush(t.Context()); err != nil || n.Status().ActiveTraces != 0 || len(n.state.Manifest.Files) != 0 {
		t.Fatalf("retention did not logically retire file: %v", err)
	}
	if len(n.state.Retired) == 0 || !slices.ContainsFunc(n.state.Fences, func(f sessionarchive.Fence) bool { return f.Withdrawn }) {
		t.Fatal("retention lost storage inventory or resurrection fence")
	}
}

func TestNativeBlobFailureBeforeAcknowledgementAndRepositoryWithdrawal(t *testing.T) {
	n, s := nativeProtocol(t)
	completed := s.now.Add(-time.Hour)
	s.failKey = "active.json"
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, nativeSource("retry"), ""); err == nil {
		t.Fatal("failed durability read was acknowledged")
	}

	s.failKey = ""
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, nativeSource("retry"), ""); err != nil {
		t.Fatal(err)
	}
	if err := n.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if len(n.state.Manifest.Files) != 0 || n.Status().ActiveTraces != 0 {
		t.Fatal("repository withdrawal retained logical membership or unretirable objects")
	}
	n = restartNative(t, n)
	if err := n.CaptureSession(t.Context(), "octo/api", "456", 1, completed, nativeSource("retry"), ""); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("repository tombstone did not survive restart")
	}
	if err := n.Publish(t.Context(), sessionarchive.Manifest{}); err == nil {
		t.Fatal("native producer silently selected local file publication")
	}
	if _, err := NewNative(context.Background(), nativeCaptureConfig(t), validConfig()); !errors.Is(err, ErrConformance) {
		t.Fatalf("legacy acceptance activated native producer: %v", err)
	}
}

func TestNativeBlobInterruptedUploadRecoversWithoutLocalStaging(t *testing.T) {
	n, s := nativeProtocol(t)
	completed := s.now.Add(-time.Hour)
	source := nativeSource("approved retry content")
	projection, err := sessionarchive.Project(n.traceConfig(), n.capture.Namespace, "octo/api", "123",
		completed.Format(time.DateOnly), 1, source, s.now)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(projection.Events[2].PayloadJSON), []byte("private-token")) {
		t.Fatal("closed projection leaked unapproved source field")
	}
	_, file, err := sessionarchive.NativeParquet(t.Context(), projection, n.config.MaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	key := objectKey(file)
	s.loseKey = key
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, source, ""); err == nil {
		t.Fatal("ambiguous upload was acknowledged before publication")
	}
	if n.Status().ActiveTraces != 0 || len(n.state.Pending) != 1 || n.Status().StagingBytes == 0 {
		t.Fatal("interrupted object upload lost its durable reservation")
	}
	n = restartNative(t, n)
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, completed, source, ""); err != nil {
		t.Fatal(err)
	}
	if n.Status().ActiveTraces != 1 || len(n.state.Pending) != 0 || s.uploads[key] != 1 {
		t.Fatal("restart did not deduplicate uploaded immutable bytes")
	}
	s.loseKey = "active.json"
	if err := n.CaptureSession(t.Context(), "octo/api", "456", 1, completed, source, ""); err != nil {
		t.Fatal("ambiguous active-pointer write was not resolved under the lease")
	}
	n = restartNative(t, n)
	if n.Status().ActiveTraces != 2 {
		t.Fatal("restart lost a resolved publication receipt")
	}
}
