package postgresx

import (
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestNativeActivationRequiresPinnedInventoryInWriterTransaction(t *testing.T) {
	store, _ := nativeTestStore(t)
	a := store.Acquisition()
	snapshot, inventory := nativeInventoryFixture(t, "Security")
	strict, err := a.RequireInventory(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	identity := collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := a.ClaimAtEpoch(t.Context(), identity, scope.Epoch, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evidence := collectnative.Evidence{Version: 1, Identity: identity, Run: map[string]any{
		"organization": "octo", "repository": identity.Repository, "run_id": identity.RunID, "run_attempt": 1,
		"workflow_name": "Test", "workflow_path": ".github/workflows/test.lock.yml",
	}, Gaps: []string{"session-unavailable"}}
	if err := a.Publish(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	publications, err := a.Publications(t.Context(), 10)
	if err != nil || len(publications) != 1 {
		t.Fatalf("durable pending intent: %+v %v", publications, err)
	}
	assertPending := func() {
		t.Helper()
		if changed, err := strict.Activate(t.Context(), publications[0]); err == nil || changed {
			t.Fatalf("canonical activation bypassed inventory fence: changed=%v err=%v", changed, err)
		}
		var count int
		if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runs WHERE namespace=$1`, store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed activation leaked a run: %d %v", count, err)
		}
		pending, err := a.Publications(t.Context(), 10)
		if err != nil || len(pending) != 1 {
			t.Fatalf("failed activation consumed its evidence: %+v %v", pending, err)
		}
	}
	assertPending()
	refresh, err := a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	inventory.Scope = scope
	if err := a.PublishInventory(t.Context(), refresh, inventory); err != nil {
		t.Fatal(err)
	}
	// Retained source evidence alone is not proof of intact canonical control.
	if _, err := store.db.ExecContext(t.Context(), `DELETE FROM workflows WHERE namespace=$1`, store.namespace); err != nil {
		t.Fatal(err)
	}
	assertPending()
	refresh, err = a.BeginInventoryRefresh(t.Context(), scope, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PublishInventory(t.Context(), refresh, inventory); err != nil {
		t.Fatal(err)
	}
	other := strict
	other.inventoryDigest = strings.Repeat("f", 64)
	if _, err := other.Activate(t.Context(), publications[0]); err == nil {
		t.Fatal("replica with another reviewed inventory activated this intent")
	}
	if changed, err := strict.Activate(t.Context(), publications[0]); err != nil || !changed {
		t.Fatalf("ready inventory could not activate durable run: changed=%v err=%v", changed, err)
	}
	if changed, err := strict.Activate(t.Context(), publications[0]); err != nil || changed {
		t.Fatalf("strict publication was not idempotent: changed=%v err=%v", changed, err)
	}
}
