package postgresx

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestNativeValuePreparationMergesImportsHealthAndPreservesProof(t *testing.T) {
	store, _ := nativeTestStore(t)
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	scope := nativeValueScope(t, store)
	if _, err := store.Acquisition().PublishValue(t.Context(), scope, nil, packet,
		[]ValueProofFile{{Path: "inventory.json", Content: []byte("durable")}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	health := collectionFixture(t)
	state, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{health}, []model.Row{packet})
	if err != nil || revision != state.Revision+1 {
		t.Fatalf("preparation publication: %d %v", revision, err)
	}
	assertNativeValueProof(t, store, "durable")
	inputs, err := store.ReadNativeValueInputs(t.Context(), []string{scope.Repository})
	if err != nil || inputs.Revision != revision || len(inputs.Collections) != 1 ||
		len(inputs.Inventory["workflows"].Rows) != 1 {
		t.Fatalf("bounded canonical preparation: %+v %v", inputs, err)
	}
	// A repeated preparation is a retry of the same accepted import, not a
	// remote acquisition with permission to replace its private proof.
	revision, err = store.PublishNativeValuePreparation(t.Context(), revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{health}, []model.Row{packet})
	if err != nil {
		t.Fatalf("identical bulk import/health retry was rejected: %v", err)
	}
	assertNativeValueProof(t, store, "durable")
	next := maps.Clone(health)
	next["observedAt"] = "2026-10-24T14:00:00.000Z"
	next["registration"], next["state"], next["reasonCode"] = "disabled", "disabled", "disabled"
	next["reason"] = valueCollectionReasons["disabled"]
	delete(next, "collector")
	delete(next, "lastAttemptAt")
	delete(next, "lastAttemptOutcome")
	revision, err = store.PublishNativeValuePreparation(t.Context(), revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{next}, []model.Row{recurringPublication(t)})
	if err != nil {
		t.Fatal(err)
	}
	inputs, err = store.ReadNativeValueInputs(t.Context(), []string{scope.Repository})
	if err != nil || inputs.Collections[0]["registration"] != "disabled" ||
		inputs.Collections[0]["lastAttemptOutcome"] != health["lastAttemptOutcome"] {
		t.Fatalf("independent decision/attempt reconciliation: %+v %v", inputs, err)
	}
	assertNativeValueProof(t, store, "durable")
	// A stale import and its accompanying health decision roll back together.
	if _, err := store.PublishNativeValuePreparation(t.Context(), revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{health}, []model.Row{packet}); !errors.Is(err, ErrValueConflict) {
		t.Fatalf("stale import accepted: %v", err)
	}
	state, err = store.State(t.Context())
	if err != nil || state.Revision != revision {
		t.Fatalf("rejected import advanced revision: %+v %v", state, err)
	}
	assertNativeValueProof(t, store, "durable")
	expected := recurringPublication(t)
	assertPacket := func() {
		t.Helper()
		accepted, err := store.ReadOperationalValue(t.Context(), scope.Repository, "verify-bugs")
		if err != nil {
			t.Fatal(err)
		}
		_, expectedDigest, err := normalizeValuePublication(expected)
		if err != nil {
			t.Fatal(err)
		}
		_, acceptedDigest, err := normalizeValuePublication(accepted)
		if err != nil || acceptedDigest != expectedDigest {
			t.Fatalf("bulk retry/rollback replaced accepted import history: %v", err)
		}
	}
	assertPacket()
	if _, err := store.PublishNativeValuePreparation(t.Context(), revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{next}, nil); err != nil {
		t.Fatalf("health-only retry required or erased its earlier accepted import: %v", err)
	}
	assertPacket()
	assertNativeValueProof(t, store, "durable")
}

func TestNativeValuePreparationRejectsStaleRevisionAndEpoch(t *testing.T) {
	store, _ := nativeTestStore(t)
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	scope := nativeValueScope(t, store)
	state, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	wrong := scope
	wrong.Epoch++
	if _, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{wrong}, []model.Row{collectionFixture(t)}, nil); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("wrong source epoch published health: %v", err)
	}
	revision, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, nil, []model.Row{packet})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{collectionFixture(t)}, nil); !errors.Is(err, ErrOperationalValueRevisionStale) {
		t.Fatalf("stale canonical input published health: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PublishNativeValuePreparation(canceled, revision,
		[]collectnative.EnrollmentScope{scope}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication: %v", err)
	}
	authority := store.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, scope.InstallationID, []string{scope.Repository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceOperationalValues(t.Context(), revision, nil); !errors.Is(err, ErrOperationalValueRevisionStale) {
		t.Fatalf("withdrawal did not fence an evaluated local result: %v", err)
	}
	state, err = store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{collectionFixture(t)}, []model.Row{packet}); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("withdrawn source resurrected evidence: %v", err)
	}
	inputs, err := store.ReadNativeValueInputs(t.Context(), []string{scope.Repository})
	if err != nil || len(inputs.Collections) != 0 || len(inputs.Inventory["workflows"].Rows) != 0 {
		t.Fatalf("withdrawn source survived preparation: %+v %v", inputs, err)
	}
}

func TestNativeValuePreparationCreatesOnlyReviewedStructuralWorkflowParents(t *testing.T) {
	store, _ := nativeTestStore(t)
	scope := nativeValueScope(t, store)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	if err := writer.Append(t.Context(), "$repositories", model.Row{
		"id": "repository:octo%2Fexample", "owner": "octo", "name": "example",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := writer.Publish(t.Context(), "repository-without-workflows")
	if err != nil {
		t.Fatal(err)
	}
	health := collectionFixture(t)
	unconfigured := maps.Clone(health)
	unconfigured["registration"], unconfigured["state"], unconfigured["reasonCode"] = "unconfigured", "unconfigured", "unconfigured"
	unconfigured["reason"] = valueCollectionReasons["unconfigured"]
	delete(unconfigured, "collector")
	if _, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{unconfigured}, nil); !errors.Is(err, ErrValueParentMissing) {
		t.Fatalf("ingestion scope invented an unconfigured workflow: %v", err)
	}
	if _, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{health}, []model.Row{evidencePacket(t)}); err != nil {
		t.Fatalf("reviewed registered workflow could not precede its first run: %v", err)
	}
	inputs, err := store.ReadNativeValueInputs(t.Context(), []string{scope.Repository})
	if err != nil || len(inputs.Inventory["workflows"].Rows) != 1 || len(inputs.Collections) != 1 {
		t.Fatalf("reviewed structural parent disappeared: %+v %v", inputs, err)
	}
}

func TestNativeValuePreparationWorksWithProductionRuntimeGrants(t *testing.T) {
	owner, config := nativeTestStore(t)
	store := valueRuntimeRoleStore(t, owner, config)
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	scope := nativeValueScope(t, store)
	inputs, err := store.ReadNativeValueInputs(t.Context(), []string{scope.Repository})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishNativeValuePreparation(t.Context(), inputs.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{collectionFixture(t)}, []model.Row{packet}); err != nil {
		t.Fatalf("non-owner runtime could not merge reviewed preparation: %v", err)
	}
}
