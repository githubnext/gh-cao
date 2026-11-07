package postgresx_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func nativeOutcomeEvidence(t *testing.T, identity collectnative.Identity) collectnative.Evidence {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../../dashboard/site/test/fixtures", "unified-telemetry-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"runId":"303",`), []byte(`"runId":"303","repository":"octo/api",`), 1)
	session, _, err := collectnative.ProjectSession(t.Context(), raw, "usage/aw_session.jsonl", identity, collectnative.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := acquisitionEvidence(identity)
	evidence.Run["cao_unified_session"] = session
	evidence.Run["created_at"] = time.Now().UTC().Format(time.RFC3339)
	return evidence
}

func nativeOutcomeAudits(t *testing.T, store *postgresx.Store) []model.Row {
	t.Helper()
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "audits", From: "$audits"},
	}, []string{"audits"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []model.Row
	for _, row := range results["audits"].Rows {
		if row["source"] == mining.Source && row["type"] == "outcome.observed" {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestNativeOutcomesSurviveHandoffReprojectionAndConsentWithdrawal(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	identity := acquisitionIdentity()
	evidence := nativeOutcomeEvidence(t, identity)
	a, b := first.Acquisition(), second.Acquisition()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	outcome := mining.Outcome{Repository: identity.Repository, Number: 42, URL: "https://github.com/octo/api/issues/42",
		State: "closed", StateReason: "completed", UpdatedAt: now.Add(-time.Minute), ObservedAt: now, Source: "github-webhook"}
	t.Setenv("PATH", "/nonexistent")
	if err := a.RecordOutcome(t.Context(), scope, outcome); err != nil {
		t.Fatal(err)
	}
	if rows := nativeOutcomeAudits(t, second); len(rows) != 0 {
		t.Fatal("unattributed object was invented as an agent output")
	}
	publication := publishNative(t, a, evidence)
	if _, err := b.Activate(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	assertStatus := func(status string) {
		t.Helper()
		rows := nativeOutcomeAudits(t, second)
		if len(rows) != 1 || rows[0]["status"] != status {
			t.Fatalf("expected one %s safe-output disposition: %+v", status, rows)
		}
	}
	assertStatus("accepted")
	before, err := first.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, acquisition := range []postgresx.Acquisition{a, b} {
		workers.Go(func() {
			if err := acquisition.RecordOutcome(t.Context(), scope, outcome); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	after, err := second.State(t.Context())
	if err != nil || fmt.Sprint(before.Revision) != fmt.Sprint(after.Revision) {
		t.Fatalf("duplicate replicas changed canonical revision: before=%v after=%v err=%v", before, after, err)
	}
	reopened := outcome
	reopened.State, reopened.StateReason, reopened.UpdatedAt = "open", "reopened", now.Add(time.Minute)
	if err := b.RecordOutcome(t.Context(), scope, reopened); err != nil {
		t.Fatal(err)
	}
	assertStatus("pending")
	conflict := reopened
	conflict.State, conflict.StateReason = "closed", "completed"
	if err := a.RecordOutcome(t.Context(), scope, conflict); err == nil {
		t.Fatal("same-clock conflicting disposition was selected")
	}
	if err := a.RecordOutcome(t.Context(), scope, outcome); err != nil {
		t.Fatal(err)
	}
	assertStatus("pending")
	evidence.Run["updated_at"] = now.Add(time.Minute).Format(time.RFC3339)
	publication = publishNative(t, b, evidence)
	if _, err := a.Activate(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	assertStatus("pending")
	authority := first.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, identity.InstallationID, []string{identity.Repository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return authority.Enroll(ctx, identity.InstallationID, []string{identity.Repository})
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordOutcome(t.Context(), scope, reopened); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old outcome task crossed consent epoch: %v", err)
	}
	publication = publishNative(t, a, evidence)
	if _, err := b.Activate(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	if rows := nativeOutcomeAudits(t, second); len(rows) != 0 {
		t.Fatal("withdrawn disposition reappeared after same-installation reenrollment")
	}
}
