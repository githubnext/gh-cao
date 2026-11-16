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
	return nativeOutcomeEvidenceForTarget(t, identity, identity.Repository)
}

func nativeOutcomeEvidenceForTarget(
	t *testing.T,
	identity collectnative.Identity,
	targetRepository string,
) collectnative.Evidence {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../../dashboard/site/test/fixtures", "unified-telemetry-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"runId":"303",`), []byte(`"runId":"303","repository":"octo/api",`), 1)
	raw = bytes.ReplaceAll(raw, []byte(`"repo":"octo/api"`), []byte(`"repo":"`+targetRepository+`"`))
	raw = bytes.ReplaceAll(raw, []byte("https://github.com/octo/api/issues/42"),
		[]byte("https://github.com/"+targetRepository+"/issues/42"))
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

func nativeOutputLifecycles(t *testing.T, store *postgresx.Store) []model.Row {
	t.Helper()
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "lifecycles", From: "$outputLifecycles"},
	}, []string{"lifecycles"})
	if err != nil {
		t.Fatal(err)
	}
	return results["lifecycles"].Rows
}

func TestCrossRepositoryNativeOutcomesUseTargetEnrollmentAuthority(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	identity := acquisitionIdentity()
	targetRepository := "octo/target"
	targetURL := "https://github.com/octo/target/issues/42"
	evidence := nativeOutcomeEvidenceForTarget(t, identity, targetRepository)
	a, b := first.Acquisition(), second.Acquisition()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnrollment(t.Context(), targetRepository, 8); err != nil {
		t.Fatal(err)
	}
	producingScope, err := a.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	targetScope, err := a.CurrentScope(t.Context(), targetRepository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	dispositionAt := now.Add(-time.Minute)
	outcome := mining.Outcome{
		Repository: targetRepository, Number: 42, URL: targetURL,
		State: "closed", StateReason: "completed", CloseActor: "User",
		DispositionAt: &dispositionAt, UpdatedAt: dispositionAt,
		ObservedAt: now, Source: "github-api",
	}
	if err := a.RecordOutcome(t.Context(), targetScope, outcome); err != nil {
		t.Fatal(err)
	}
	publication := publishNative(t, a, evidence)
	if _, err := b.Activate(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	lifecycles := nativeOutputLifecycles(t, second)
	if len(lifecycles) != 1 || lifecycles[0]["adoptionState"] != "accepted" ||
		lifecycles[0]["targetRepository"] != targetRepository ||
		lifecycles[0]["deliveryState"] != "delivered" {
		t.Fatalf("cross-repository outcome was not attributed to the producing lifecycle: %+v", lifecycles)
	}
	targets, more, err := b.LifecycleRefreshTargets(t.Context(), targetScope, time.Time{}, 25)
	if err != nil || more || len(targets) != 1 || targets[0] != targetURL {
		t.Fatalf("target-authority refresh page = %v more=%t err=%v", targets, more, err)
	}
	producingTargets, more, err := b.LifecycleRefreshTargets(t.Context(), producingScope, time.Time{}, 25)
	if err != nil || more || len(producingTargets) != 0 {
		t.Fatalf("producing repository acquired target refresh authority: %v more=%t err=%v",
			producingTargets, more, err)
	}
	if audits := nativeOutcomeAudits(t, second); len(audits) != 1 ||
		audits[0]["targetRepository"] != targetRepository {
		t.Fatalf("cross-repository outcome audit = %+v", audits)
	}

	if err := a.SetEnrollment(t.Context(), targetRepository, 9); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordOutcome(t.Context(), targetScope, outcome); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("previous target epoch retained outcome authority: %v", err)
	}
	assertDeliveryFallback := func(stage string) {
		t.Helper()
		rows := nativeOutputLifecycles(t, second)
		if len(rows) != 1 || rows[0]["adoptionState"] != "pending" ||
			rows[0]["adoptionSource"] != "adoption-registry" ||
			rows[0]["survivalState"] != "unknown" ||
			rows[0]["deliveryState"] != "delivered" ||
			rows[0]["targetRepository"] != targetRepository ||
			rows[0]["reversalSignal"] != nil || rows[0]["survivalSignal"] != nil {
			t.Fatalf("%s did not retain delivery-only lifecycle: %+v", stage, rows)
		}
		if audits := nativeOutcomeAudits(t, second); len(audits) != 0 {
			t.Fatalf("%s retained provider-derived audits: %+v", stage, audits)
		}
	}
	assertDeliveryFallback("target authority transfer")

	currentTarget, err := b.CurrentScope(t.Context(), targetRepository)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RecordOutcome(t.Context(), currentTarget, outcome); err != nil {
		t.Fatal(err)
	}
	if rows := nativeOutputLifecycles(t, second); len(rows) != 1 || rows[0]["adoptionState"] != "accepted" {
		t.Fatalf("current target epoch did not reacquire exact outcome: %+v", rows)
	}
	authority := first.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, currentTarget.InstallationID, []string{targetRepository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertDeliveryFallback("target withdrawal")
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
	dispositionAt := now.Add(-time.Minute)
	outcome := mining.Outcome{Repository: identity.Repository, Number: 42, URL: "https://github.com/octo/api/issues/42",
		State: "closed", StateReason: "completed", CloseActor: "User", DispositionAt: &dispositionAt,
		UpdatedAt: dispositionAt, ObservedAt: now, Source: "github-api"}
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
		lifecycles := nativeOutputLifecycles(t, second)
		if len(lifecycles) != 1 || lifecycles[0]["adoptionState"] != status {
			t.Fatalf("expected one %s canonical output lifecycle: %+v", status, lifecycles)
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
	reopened.ObservedAt = now.Add(2 * time.Minute)
	reopened.CloseActor, reopened.DispositionAt = "", nil
	if err := b.RecordOutcome(t.Context(), scope, reopened); err != nil {
		t.Fatal(err)
	}
	assertStatus("pending")
	if lifecycle := nativeOutputLifecycles(t, second)[0]; lifecycle["reversalSignal"] != "ISS_REOPENED" ||
		lifecycle["survivalState"] != "reversed" {
		t.Fatalf("reversal evidence was not retained: %+v", lifecycle)
	}
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
	if lifecycles := nativeOutputLifecycles(t, second); len(lifecycles) != 1 ||
		lifecycles[0]["adoptionState"] != "pending" || lifecycles[0]["adoptionSource"] != "adoption-registry" {
		t.Fatalf("reenrollment did not rebuild delivery-owned lifecycle: %+v", lifecycles)
	}
}
