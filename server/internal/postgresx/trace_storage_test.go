package postgresx_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func sharedTraceConfig() adxtrace.Config {
	return adxtrace.Config{Enabled: true, SharedStorage: true, Repositories: []string{"octo/api"},
		Mode: "full", Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "user.message": {"content"}}}}
}

func TestNativeTraceTransactionsRollbackAndExcludeConcurrentOwners(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	a, err := first.TraceStorage("destination", 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.TraceStorage("destination", 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64) + ".trace"
	failed := errors.New("crash before commit")
	if err := a.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		if err := objects.Write(key, []byte("approved")); err != nil {
			return err
		}
		if err := b.Transaction(t.Context(), func(adxtrace.Objects) error { return nil }); !errors.Is(err, adxtrace.ErrBusy) {
			t.Fatalf("separate replica entered transaction: %v", err)
		}
		return failed
	}); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if err := b.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		if _, err := objects.Read(key, 1024); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("uncommitted content survived: %v", err)
		}
		if err := objects.Write(key, []byte("approved")); err != nil {
			return err
		}
		if _, err := objects.Read(key, 1); !errors.Is(err, adxtrace.ErrCapacity) {
			t.Fatalf("read fetched an oversized object: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		content, err := objects.Read(key, 1024)
		if string(content) != "approved" || err != nil {
			t.Fatalf("committed handoff failed: %q %v", content, err)
		}
		return objects.Remove(key)
	}); err != nil {
		t.Fatal(err)
	}
}

type sharedTraceSink struct {
	mu       sync.Mutex
	digests  map[string]bool
	failOnce bool
	ready    int
}

func (*sharedTraceSink) Capacity(context.Context) error { return nil }
func (s *sharedTraceSink) Write(_ context.Context, table string, rows []model.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		s.digests[row["RowDigest"].(string)] = true
		if table == "TraceManifest" && row["State"] == "ready" {
			s.ready++
		}
	}
	if s.failOnce {
		s.failOnce = false
		return errors.New("ambiguous remote write")
	}
	return nil
}
func (s *sharedTraceSink) VerifyEvidence(_ context.Context, _, _, _ string, tables map[string][]model.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rows := range tables {
		for _, row := range rows {
			if !s.digests[row["RowDigest"].(string)] {
				return errors.New("remote evidence not visible")
			}
		}
	}
	return nil
}

func TestNativeTraceSeparateReplicaDeliveryRecoveryWithoutFiles(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	c := sharedTraceConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	a, err := first.TraceStorage("destination", c.MaxSpoolBytes, c.MaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.TraceStorage("destination", c.MaxSpoolBytes, c.MaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	sink := &sharedTraceSink{digests: map[string]bool{}, failOnce: true}
	w1, err := adxtrace.NewShared(t.Context(), c, "destination", sink, a)
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().Add(-72 * time.Hour)
	raw := []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
		"{\"type\":\"user.message\",\"data\":{\"content\":\"approved content\",\"password\":\"PRIVATE\"}}\n")
	t.Setenv("PATH", "/nonexistent")
	if err := w1.CaptureSession(t.Context(), "octo/api", "303", 1, completed, raw, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Step(t.Context()); err == nil {
		t.Fatal("ambiguous destination failure was not surfaced")
	}
	w2, err := adxtrace.NewShared(t.Context(), c, "destination", sink, b)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		more, err := w2.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if sink.ready != 1 {
		t.Fatalf("expected one verified ready manifest, got %d", sink.ready)
	}
	if err := a.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		list, err := objects.List()
		if err != nil {
			return err
		}
		for _, item := range list {
			content, err := objects.Read(item.Key, 128<<20)
			if err != nil {
				return err
			}
			if strings.Contains(string(content), "PRIVATE") {
				t.Fatal("unapproved raw content reached durable trace storage")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w1.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if err := w2.CaptureSession(t.Context(), "octo/api", "303", 1, completed, raw, ""); err == nil {
		t.Fatal("another replica revived withdrawn trace evidence")
	}
}
