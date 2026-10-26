package sessionarchive

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDuckDBArchiveRetiredStagingRecoveryIntegration(t *testing.T) {
	for _, reason := range []string{"expired", "withdrawn"} {
		t.Run(reason, func(t *testing.T) {
			c := testConfig(t)
			c.Retention = 24 * time.Hour
			publisher := &recordingPublisher{fail: true}
			a := openTestArchive(t, c, publisher)
			p := testProjection(t, c, "123", "unpublished approved evidence")
			if reason == "expired" {
				p.CapturedAt = time.Now().Add(-25 * time.Hour)
				for i := range p.Events {
					p.Events[i].CapturedAt = p.CapturedAt.UTC().Format(time.RFC3339Nano)
				}
			}
			if err := a.Admit(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			stage := a.state.Records[memberKey(p.Member())].Stage
			if reason == "withdrawn" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := a.Withdraw(ctx, p.Repository, p.RunID, p.Attempt); !errors.Is(err, context.Canceled) {
					t.Fatalf("withdrawal did not persist before canceled publication: %v", err)
				}
			}
			for range 2 {
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				a = openTestArchive(t, c, publisher)
				if _, err := a.root.Stat(stage); err != nil {
					t.Fatalf("recovery deleted a persisted staging reference: %v", err)
				}
				if err := a.Maintain(t.Context()); err == nil {
					t.Fatal("unavailable destination was falsely acknowledged")
				}
			}
			publisher.fail = false
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			if a.Status().StagedTraces != 0 || a.Status().ActiveTraces != 0 {
				t.Fatal("retired staging was published or retained after acknowledgement")
			}
			if _, err := a.root.Stat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("acknowledged retired staging was not released: %v", err)
			}
			if reason == "withdrawn" {
				if err := a.Admit(t.Context(), p); !errors.Is(err, ErrWithdrawn) {
					t.Fatal("recovery discarded the withdrawal fence")
				}
			}
		})
	}
}

func TestDuckDBArchiveAmbiguousAdmissionIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "complete durable evidence")
	write := a.writeState
	a.writeState = func(content []byte) error {
		if err := write(content); err != nil {
			return err
		}
		// Model the real post-rename failure boundary, not the converter.
		return ErrUncertain
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrUncertain) {
		t.Fatalf("post-rename uncertainty was falsely acknowledged: %v", err)
	}
	r := a.state.Records[memberKey(p.Member())]
	if r.Stage == "" {
		t.Fatal("ambiguous admission discarded evidence reference")
	}
	if _, err := a.root.Stat(r.Stage); err != nil {
		t.Fatal("ambiguous admission deleted a durably referenced stage")
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrUncertain) {
		t.Fatalf("retry acknowledged without recovering durability: %v", err)
	}
	if _, err := a.Pin(t.Context(), Scope{c.Namespace, c.Repositories}); !errors.Is(err, ErrUncertain) {
		t.Fatalf("query admitted before durability reconciliation: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := openTestArchive(t, c, nil)
	if recovered.Status().StagedTraces != 1 {
		t.Fatal("post-rename state could not recover complete staged content")
	}
	if err := recovered.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := pinTestArchive(t, recovered)
	rows, err := s.Timeline(t.Context(), TimelineRequest{"octo/api", "123", 1, 100})
	if err != nil || len(rows) != len(p.Events) {
		t.Fatalf("post-rename recovery lost the complete trace: %v", err)
	}
}

func TestDuckDBArchiveCanceledWithdrawalIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "withdraw this body")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := pinTestArchive(t, a)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Withdraw(ctx, "octo/api", "123", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication reported completed withdrawal: %v", err)
	}
	if _, err := old.Summary(t.Context()); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("old pin ignored durable live attempt fence: %v", err)
	}
	fresh := pinTestArchive(t, a)
	if _, err := fresh.Timeline(t.Context(), TimelineRequest{"octo/api", "123", 1, 100}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fresh pin exposed withdrawn membership before next publication: %v", err)
	}
	rows, err := fresh.Summary(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("summary resurrected a fenced attempt: %#v %v", rows, err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDuckDBArchiveRejectedOutputCleanupIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "whole trace")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	stage := a.state.Records[memberKey(p.Member())].Stage
	budget := a.config.MaxArchiveBytes
	a.config.MaxArchiveBytes = 1
	if err := a.Flush(t.Context()); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized publication was not explicitly rejected: %v", err)
	}
	files, err := diskFiles(a.root, 100)
	if err != nil {
		t.Fatal(err)
	}
	for path := range files {
		if strings.HasSuffix(path, ".parquet") {
			t.Fatalf("rejected immutable output poisoned future capacity: %s", path)
		}
	}
	if a.Status().StagedTraces != 1 {
		t.Fatal("rejected conversion lost complete admitted evidence")
	}
	a.config.MaxArchiveBytes = budget
	if err := a.Maintain(t.Context()); err != nil {
		t.Fatalf("capacity did not recover without a process restart: %v", err)
	}
	if a.Status().ActiveTraces != 1 || a.Status().StagedTraces != 0 {
		t.Fatal("successful retry did not complete publication")
	}
	if _, err := a.root.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("acknowledged staging body still retained")
	}
}
