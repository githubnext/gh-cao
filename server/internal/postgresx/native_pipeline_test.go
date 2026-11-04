package postgresx_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativePipelineProvider struct {
	run collectnative.Run
}

func (p nativePipelineProvider) Runs(_ context.Context, _ string, _ int64, _ time.Time, visit func(collectnative.Run) error) error {
	return visit(p.run)
}

func (p nativePipelineProvider) Attempt(_ context.Context, identity collectnative.Identity) (collectnative.Run, error) {
	if identity != p.run.Identity {
		return collectnative.Run{}, collectnative.ErrIdentity
	}
	return p.run, nil
}

func (p nativePipelineProvider) Artifacts(_ context.Context, _ collectnative.Run, visit func(collectnative.Artifact) error) error {
	return visit(collectnative.Artifact{Name: "agent-usage", Files: map[string][]byte{
		"aw_info.json": []byte(`{"repository":"octo/api","run_id":303,"run_attempt":1}`),
		"aw_session.jsonl": []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
			"{\"type\":\"workflow.info\",\"data\":{\"repository\":\"octo/api\",\"runId\":\"303\",\"engineId\":\"copilot\"}}\n" +
			"{\"type\":\"user.message\",\"data\":{\"content\":\"approved content\",\"password\":\"PRIVATE\"}}\n"),
	}})
}

type interruptedNativeTrace struct {
	inner adxtrace.Storage
	fail  bool
}

func (s *interruptedNativeTrace) Transaction(ctx context.Context, callback func(adxtrace.Objects) error) error {
	return s.inner.Transaction(ctx, func(objects adxtrace.Objects) error {
		if err := callback(objects); err != nil {
			return err
		}
		if s.fail {
			return errors.New("interrupted durable trace admission")
		}
		return nil
	})
}

func TestNativePipelineTraceAdmissionPrecedesSafeCanonicalPublication(t *testing.T) {
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
	storage, err := first.TraceStorage("native-pipeline", c.MaxSpoolBytes, c.MaxEntries)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := &interruptedNativeTrace{inner: storage}
	sink := &sharedTraceSink{digests: map[string]bool{}}
	trace, err := adxtrace.NewShared(t.Context(), c, "native-pipeline", sink, interrupted)
	if err != nil {
		t.Fatal(err)
	}
	identity := acquisitionIdentity()
	acquisition := first.Acquisition()
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	provider := nativePipelineProvider{run: collectnative.Run{Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml",
		Fields: map[string]any{"status": "completed", "conclusion": "success", "created_at": at, "updated_at": at}}}
	collector := collectnative.Collector{Provider: provider, Store: acquisition,
		Destinations: []collectnative.Destination{collectnative.TraceDestination{Admission: trace}}}
	t.Setenv("PATH", "/nonexistent")
	interrupted.fail = true
	if err := collector.Collect(t.Context(), identity.Repository, identity.InstallationID, time.Time{}); err == nil {
		t.Fatal("acquisition succeeded despite failed durable trace admission")
	}
	if pending, err := second.Acquisition().Publications(t.Context(), 1); err != nil || len(pending) != 0 {
		t.Fatal("safe evidence became publishable before its trace destination committed")
	}
	if err := storage.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		list, err := objects.List()
		if err != nil {
			return err
		}
		for _, object := range list {
			if strings.HasSuffix(object.Key, ".trace") {
				t.Fatal("failed admission left an approved-content object behind")
			}
		}
		_, err = objects.Read(strings.Repeat("a", 64)+".trace", 100)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("nonexistent trace was not reported honestly")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	interrupted.fail = false
	if err := collector.CollectAttempt(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	evidence, _, err := second.Acquisition().Pending(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	content, _, err := evidence.Bytes()
	if err != nil || strings.Contains(string(content), "approved content") || strings.Contains(string(content), "PRIVATE") {
		t.Fatal("raw session content escaped into ordinary safe evidence")
	}
	if count, err := second.Acquisition().Replay(t.Context(), 1); err != nil || count != 1 {
		t.Fatalf("separate replica could not activate canonical facts: %d %v", count, err)
	}
	for range 10 {
		more, err := trace.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if sink.ready != 1 {
		t.Fatalf("approved trace did not reach one verified ready manifest: %d", sink.ready)
	}
}
