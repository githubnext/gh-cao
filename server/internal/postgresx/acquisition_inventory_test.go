package postgresx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func nativeInventoryFixture(t *testing.T, name string) (collectnative.InventorySnapshot, collectnative.InventoryEvidence) {
	t.Helper()
	provenance := collectnative.InventoryProvenance{SourceRepository: "octo/api", SourceSHA: strings.Repeat("a", 40), PolicySHA: strings.Repeat("b", 40)}
	sources := map[string]model.Source{}
	content := fmt.Sprintf(`{
		"campaigns":{"source":"campaigns","rows":[{"campaign":"security","campaign-name":%q,"campaign-mode":"review","campaign-enabled":false,"campaign-source":"octo/catalog/security","campaign-version":"aaaaaaaaaaaa","campaign-current-version":"unknown","campaign-update-state":"unknown"}]},
		"repositories":{"source":"repositories","rows":[{"organization":"octo","repository":"api","visibility":"private"}]},
		"workflows":{"source":"workflows","rows":[{"organization":"octo","repository":"api","workflow":".github/workflows/test.md","workflow-name":"Test","workflow-role":"worker","workflow-source-kind":"source-managed","campaign":"security","inventory-ready":false,"admission-status":"blocked","admission-reason":"worker-disabled","gh-aw-version":"unknown","rollout-mode":"review"}]},
		"configuration-policy":{"source":"configuration-policy","rows":[{"path":".github/workflows/cao.json","raw":"reviewed raw policy","document":{"control-plane":{"mode":"review"}},"diagnostics":[{"severity":"valid","title":"Policy is valid"}]}]}
	}`, name)
	if err := decodeJSON([]byte(content), &sources); err != nil {
		t.Fatal(err)
	}
	document := struct {
		collectnative.InventoryProvenance
		ControlRepository string                  `json:"controlRepository"`
		Version           int                     `json:"version"`
		PolicyResolution  string                  `json:"policyResolution"`
		Sources           map[string]model.Source `json:"sources"`
	}{provenance, "octo/api", 1, "available", sources}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	snapshot, err := collectnative.NewInventorySnapshot(raw, hex.EncodeToString(sum[:]), provenance)
	if err != nil {
		t.Fatal(err)
	}
	evidence := collectnative.InventoryEvidence{Provenance: provenance, Digest: snapshot.Digest, SourceSnapshot: raw,
		Scope: collectnative.EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, ObservedAt: time.Now().UTC(), Sources: sources}
	return snapshot, evidence
}

func TestNativeInventoryPostgresReplicaFencesAndReadiness(t *testing.T) {
	first, config := nativeTestStore(t)
	second, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	a, b := first.Acquisition(), second.Acquisition()
	snapshot, evidence := nativeInventoryFixture(t, "Security")
	if err := a.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	evidence.Scope = scope
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	if err := b.EnsureInventoryReady(t.Context(), scope, snapshot.Digest); err == nil {
		t.Fatal("first activation was ready without source inventory")
	}
	old, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	current, err := b.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), old, evidence); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("superseded replica published: %v", err)
	}
	if err := b.PublishInventory(t.Context(), current, evidence); err != nil {
		t.Fatal(err)
	}
	if err := a.EnsureInventoryReady(t.Context(), scope, snapshot.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := first.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkflowSubjectRevision <= 0 || !strings.HasPrefix(state.WorkflowSubjectDigest, "sha256:") {
		t.Fatalf("native inventory omitted workflow-subject identity: %+v", state)
	}
	var workers sync.WaitGroup
	for _, store := range []Acquisition{a, b} {
		workers.Go(func() {
			if err := store.PublishInventory(t.Context(), current, evidence); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	after, err := second.State(t.Context())
	if err != nil || after.Revision != state.Revision ||
		after.WorkflowSubjectRevision != state.WorkflowSubjectRevision || after.WorkflowSubjectDigest != state.WorkflowSubjectDigest {
		t.Fatalf("idempotent publication advanced revision: %+v %+v %v", state, after, err)
	}
	refresh, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), refresh, evidence); err != nil {
		t.Fatal(err)
	}
	refreshed, err := first.State(t.Context())
	if err != nil || refreshed.Revision != state.Revision+1 ||
		refreshed.WorkflowSubjectRevision != state.WorkflowSubjectRevision || refreshed.WorkflowSubjectDigest != state.WorkflowSubjectDigest {
		t.Fatalf("unchanged inventory topology invalidated workflow-subject pages: %+v %+v %v", state, refreshed, err)
	}
	results, _, err := second.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "campaigns", From: "$campaigns"}, {Name: "repositories", From: "$repositories"}, {Name: "workflows", From: "$workflows"},
	}, []string{"campaigns", "repositories", "workflows"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results["campaigns"].Rows) != 1 || len(results["repositories"].Rows) != 1 || len(results["workflows"].Rows) != 1 {
		t.Fatalf("inventory parents not published through production query boundary: %+v", results)
	}
	workflow := results["workflows"].Rows[0]
	if workflow["role"] != "worker" || workflow["inventoryReady"] != false || workflow["admissionStatus"] != "blocked" ||
		workflow["campaignId"] != results["campaigns"].Rows[0]["id"] {
		t.Fatalf("canonical campaign/admission fields lost: %+v", workflow)
	}
	retained, err := a.InventoryEvidence(t.Context(), scope)
	if err != nil || len(retained.Sources["configuration-policy"].Rows) != 1 {
		t.Fatalf("configuration-policy diagnostics not retained: %+v %v", retained, err)
	}
	// A failed refresh retains serving evidence AND its retry intent.
	if _, err := b.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest); err != nil {
		t.Fatal(err)
	}
	pending, err := a.PendingInventoryScopes(t.Context(), 10)
	if err != nil || len(pending) != 1 || pending[0] != scope {
		t.Fatalf("refresh retry intent was not durable: %+v %v", pending, err)
	}
	if err := a.EnsureInventoryReady(t.Context(), scope, snapshot.Digest); err != nil {
		t.Fatal("valid previous inventory was lost during transient refresh failure:", err)
	}
	stale, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, installation := range []int64{9, 0, 7} {
		if err := b.SetEnrollment(t.Context(), scope.Repository, installation); err != nil {
			t.Fatal(err)
		}
		if err := a.PublishInventory(t.Context(), stale, evidence); err == nil {
			t.Fatalf("transfer/withdrawal installation %d allowed stale publication", installation)
		}
		var retained int
		if err := first.db.QueryRowContext(t.Context(), `SELECT count(*) FROM cao_acquisition_inventory WHERE namespace=$1`,
			first.namespace).Scan(&retained); err != nil || retained != 0 {
			t.Fatalf("transfer/withdrawal retained scoped inventory evidence: %d %v", retained, err)
		}
	}

	newScope, err := b.CurrentScope(t.Context(), scope.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureInventoryReady(t.Context(), newScope, snapshot.Digest); err == nil {
		t.Fatal("transferred-back owner inherited previous epoch's inventory readiness")
	}
}

func TestNativeInventoryPreservesExistingCanonicalParentIDs(t *testing.T) {
	store, _ := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, nil)
	a := store.Acquisition()
	snapshot, evidence := nativeInventoryFixture(t, "Security")
	if err := a.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	evidence.Scope = scope
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	claim, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	var repositories, workflows int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM repositories WHERE namespace=$1`,
		store.namespace).Scan(&repositories); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM workflows WHERE namespace=$1`,
		store.namespace).Scan(&workflows); err != nil {
		t.Fatal(err)
	}
	var repositoryID, workflowID string
	if err := store.db.QueryRowContext(t.Context(), `SELECT repository_id,workflow_id FROM runs WHERE namespace=$1`,
		store.namespace).Scan(&repositoryID, &workflowID); err != nil {
		t.Fatal(err)
	}
	if repositories != 1 || workflows != 1 || repositoryID != "repository" || workflowID != "workflow" {
		t.Fatalf("inventory duplicated or rebound existing run parents: %d %d %s %s", repositories, workflows, repositoryID, workflowID)
	}
	var role, campaignID string
	if err := store.db.QueryRowContext(t.Context(), `SELECT role,campaign_id FROM workflows WHERE namespace=$1 AND id='workflow'`,
		store.namespace).Scan(&role, &campaignID); err != nil || role != "worker" || campaignID == "" {
		t.Fatalf("inventory failed to enrich existing identity: %s %s %v", role, campaignID, err)
	}
}

func TestNativeInventoryUnsupportedIntelligenceFailsClosed(t *testing.T) {
	if _, err := normalizeNativeInventory("campaigns", model.Row{"campaign": "security",
		"campaign-intelligence-declaration": model.Row{"version": 1}}); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("unrepresentable intelligence envelope was silently dropped: %v", err)
	}
}
func TestNativeInventoryPostgresConfigurationAndAtomicFailure(t *testing.T) {
	store, _ := nativeTestStore(t)
	a := store.Acquisition()
	snapshot, evidence := nativeInventoryFixture(t, "Security")
	if err := a.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
		t.Fatal(err)
	}
	scope, _ := a.CurrentScope(t.Context(), "octo/api")
	evidence.Scope = scope
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	claim, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	// A typed normalization failure must roll back earlier campaign/repository
	// writes, quality, retained evidence and revision together.
	evidence.Sources["workflows"].Rows[0]["created-at"] = "not-a-timestamp"
	before, _ := store.State(t.Context())
	if err := a.PublishInventory(t.Context(), claim, evidence); err == nil {
		t.Fatal("malformed inventory projection succeeded")
	}
	after, _ := store.State(t.Context())
	if after.Revision != before.Revision || after.Counts["$campaigns"] != 0 || after.Counts["$repositories"] != 0 {
		t.Fatalf("failed projection committed partial parents: %+v %+v", before, after)
	}
	replacement, _ := nativeInventoryFixture(t, "Reviewed replacement")
	if err := a.PinInventoryConfiguration(t.Context(), replacement); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("incompatible replica configuration accepted: %v", err)
	}
	if err := a.ReplaceInventoryConfiguration(t.Context(), "not-the-current-digest", replacement); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("stale deployment CAS succeeded: %v", err)
	}
	if err := a.ReplaceInventoryConfiguration(t.Context(), snapshot.Digest, replacement); err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), claim, evidence); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old deployment published after configuration replacement: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.BeginInventoryRefresh(ctx, scope, replacement.Digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("database cancellation lost: %v", err)
	}
}

func TestNativeInventoryTargetRegistryCannotActivateWithoutControlInventory(t *testing.T) {
	store, _ := nativeTestStore(t)
	a := store.Acquisition()
	snapshot, controlEvidence := nativeInventoryFixture(t, "Security")
	for _, repository := range []string{"octo/api", "octo/target"} {
		if err := a.SetEnrollment(t.Context(), repository, 7); err != nil {
			t.Fatal(err)
		}
	}
	control, err := a.CurrentScope(t.Context(), "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.CurrentScope(t.Context(), "octo/target")
	if err != nil {
		t.Fatal(err)
	}
	controlEvidence.Scope = control
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(controlEvidence)
	if err != nil {
		t.Fatal(err)
	}
	var targetEvidence collectnative.InventoryEvidence
	if err := decodeJSON(raw, &targetEvidence); err != nil {
		t.Fatal(err)
	}
	targetEvidence.Scope = target
	targetEvidence.Sources["repositories"] = model.Source{Source: "repositories",
		Rows: []model.Row{{"organization": "octo", "repository": "target"}}}
	targetEvidence.Sources["workflows"] = model.Source{Source: "workflows", Rows: []model.Row{}}
	claim, err := a.BeginInventoryRefresh(t.Context(), target, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), claim, targetEvidence); err != nil {
		t.Fatal(err)
	}
	assertAtomicReadiness := func(ready bool) {
		t.Helper()
		tx, err := store.beginInvocation(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := a.fenceScope(t.Context(), tx, target); err != nil {
			t.Fatal(err)
		}
		err = a.inventoryReady(t.Context(), tx, target, snapshot.Digest)
		if (err == nil) != ready {
			t.Fatalf("atomic inventory gate readiness=%t: %v", ready, err)
		}
	}
	assertAtomicReadiness(false)
	claim, err = a.BeginInventoryRefresh(t.Context(), control, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), claim, controlEvidence); err != nil {
		t.Fatal(err)
	}
	assertAtomicReadiness(true)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE campaigns SET slug='unreviewed'
		WHERE namespace=$1`, store.namespace); err != nil {
		t.Fatal(err)
	}
	assertAtomicReadiness(false)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE campaigns SET slug='security'
		WHERE namespace=$1`, store.namespace); err != nil {
		t.Fatal(err)
	}
	assertAtomicReadiness(true)
	if err := a.SetEnrollment(t.Context(), control.Repository, 9); err != nil {
		t.Fatal(err)
	}
	assertAtomicReadiness(false)
}

func TestNativeInventoryAtomicReadinessFencesControlTransfer(t *testing.T) {
	first, config := nativeTestStore(t)
	config.RuntimeParams["application_name"] = "inventory-transfer-" + first.namespace
	second, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	a, b := first.Acquisition(), second.Acquisition()
	snapshot, controlEvidence := nativeInventoryFixture(t, "Security")
	for _, repository := range []string{"octo/api", "octo/target"} {
		if err := a.SetEnrollment(t.Context(), repository, 7); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	var target collectnative.EnrollmentScope
	for _, repository := range []string{"octo/api", "octo/target"} {
		scope, err := a.CurrentScope(t.Context(), repository)
		if err != nil {
			t.Fatal(err)
		}
		evidence := controlEvidence
		evidence.Scope = scope
		if repository == "octo/target" {
			target = scope
			evidence.Sources = map[string]model.Source{
				"campaigns":            controlEvidence.Sources["campaigns"],
				"configuration-policy": controlEvidence.Sources["configuration-policy"],
				"repositories": {Source: "repositories",
					Rows: []model.Row{{"organization": "octo", "repository": "target"}}},
				"workflows": {Source: "workflows", Rows: []model.Row{}},
			}
		}
		claim, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.PublishInventory(t.Context(), claim, evidence); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := first.beginInvocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(t.Context(), tx, target); err != nil {
		t.Fatal(err)
	}
	if err := a.inventoryReady(t.Context(), tx, target, snapshot.Digest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	transferred := make(chan error, 1)
	go func() { transferred <- b.SetEnrollment(ctx, "octo/api", 9) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		var waiting bool
		if err := first.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%cao_acquisition_scopes%')`,
			config.RuntimeParams["application_name"]).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-transferred:
			t.Fatalf("control transferred before the activation transaction completed: %v", err)
		case <-deadline.C:
			t.Fatal("control transfer did not reach the source epoch fence")
		case <-poll.C:
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-transferred:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("control transfer failed to resume after activation commit")
	}
	if err := a.EnsureInventoryReady(t.Context(), target, snapshot.Digest); err == nil {
		t.Fatal("next activation reused inventory from the withdrawn control epoch")
	}
}
