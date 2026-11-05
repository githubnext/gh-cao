package postgresx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func nativeValueScope(t *testing.T, store *Store) collectnative.EnrollmentScope {
	t.Helper()
	authority := store.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return authority.Enroll(ctx, 7, []string{"octo/example"})
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := authority.CurrentScope(t.Context(), "octo/example")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func assertNativeValueProof(t *testing.T, store *Store, want string) {
	t.Helper()
	proof, err := store.ReadValueProof(t.Context(), "octo/example", "verify-bugs")
	if err != nil || len(proof) != 1 || string(proof[0].Content) != want {
		t.Fatalf("private proof=%+v err=%v; want %q", proof, err, want)
	}
}

func TestNativeValuePublicationFencesReplicasAndRetries(t *testing.T) {
	store, config := nativeTestStore(t)
	replica, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	scope := nativeValueScope(t, store)
	start := make(chan struct{})
	type result struct {
		proof string
		value ValuePublication
		err   error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for index, client := range []*Store{store, replica} {
		workers.Go(func() {
			<-start
			proof := []string{"first", "second"}[index]
			value, err := client.Acquisition().PublishValue(t.Context(), scope, nil, packet,
				[]ValueProofFile{{Path: "inventory.json", Content: []byte(proof)}}, time.Now())
			results <- result{proof: proof, value: value, err: err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var accepted result
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil && result.value.Updated:
			accepted = result
			successes++
		case errors.Is(result.err, ErrValueConflict):
			conflicts++
		default:
			t.Fatalf("unexpected publication result: %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("competing starting packets were not fenced: successes=%d conflicts=%d", successes, conflicts)
	}
	proof := []ValueProofFile{{Path: "inventory.json", Content: []byte(accepted.proof)}}
	retry, err := replica.Acquisition().PublishValue(t.Context(), scope, nil, packet, proof, time.Now())
	if err != nil || retry.Updated || retry.Revision != accepted.value.Revision {
		t.Fatalf("retry after committed publication: %+v %v", retry, err)
	}
	assertNativeValueProof(t, store, accepted.proof)
	starting, restored, err := replica.Acquisition().ReadValue(t.Context(), scope, "verify-bugs")
	if err != nil || len(restored) != 1 || string(restored[0].Content) != accepted.proof {
		t.Fatalf("replica could not restore its source-pinned snapshot: %v %+v", err, restored)
	}
	if _, got, err := normalizeValuePublication(starting); err != nil || got == "" {
		t.Fatalf("restored packet is not canonical: %v %v", starting, err)
	}
	next := laterPublication(t)
	if _, err := replica.Acquisition().PublishValue(t.Context(), scope, nil, next,
		[]ValueProofFile{{Path: "inventory.json", Content: []byte("stale")}}, time.Now()); !errors.Is(err, ErrValueConflict) {
		t.Fatalf("stale starting packet replaced accepted proof: %v", err)
	}
	previous, err := store.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := replica.Acquisition().PublishValue(t.Context(), scope, previous, next,
		[]ValueProofFile{{Path: "inventory.json", Content: []byte("next")}}, time.Now()); err != nil || !value.Updated {
		t.Fatalf("current starting packet was rejected: %+v %v", value, err)
	}
	assertNativeValueProof(t, store, "next")
}

func TestNativeValueCoexistsWithRunProjectionAndWithdrawsAtomically(t *testing.T) {
	store, config := nativeTestStore(t)
	replica, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	scope := nativeValueScope(t, store)
	proof := []ValueProofFile{{Path: "inventory.json", Content: []byte("accepted")}}
	if _, err := store.Acquisition().PublishValue(t.Context(), scope, nil, packet, proof, time.Now()); err != nil {
		t.Fatal(err)
	}
	a := replica.Acquisition()
	identity := collectnative.Identity{Repository: scope.Repository, InstallationID: 7, RunID: 484, Attempt: 1}
	claim, err := a.ClaimAtEpoch(t.Context(), identity, scope.Epoch, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evidence := collectnative.Evidence{Version: 1, Identity: identity, Run: map[string]any{
		"organization": "octo", "repository": scope.Repository, "run_id": identity.RunID, "run_attempt": 1,
		"workflow_name": "Verify Bugs", "workflow_path": ".github/workflows/verify-bugs.lock.yml",
	}, Gaps: []string{"session-unavailable"}}
	if err := a.Publish(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	if count, err := a.Replay(t.Context(), 10); err != nil || count != 1 {
		t.Fatalf("run publication failed beside accepted Value: %d %v", count, err)
	}
	retained, err := store.ReadOperationalValue(t.Context(), scope.Repository, "verify-bugs")
	if err != nil || retained["observedAt"] != packet["observedAt"] {
		t.Fatalf("run projection changed accepted Value: %v %v", retained, err)
	}
	assertNativeValueProof(t, store, "accepted")
	authority := replica.NativeEnrollment()
	if _, err := store.db.ExecContext(t.Context(), `CREATE FUNCTION reject_native_value_erasure() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject private proof erasure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(t.Context(), `CREATE TRIGGER reject_native_value_erasure
		BEFORE DELETE ON cao_value_proof_files FOR EACH ROW EXECUTE FUNCTION reject_native_value_erasure()`); err != nil {
		t.Fatal(err)
	}
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, 7, []string{scope.Repository})
		return err
	}); err == nil {
		t.Fatal("proof erasure failure did not roll back source withdrawal")
	}
	assertNativeValueProof(t, store, "accepted")
	if current, err := authority.CurrentScope(t.Context(), scope.Repository); err != nil || current != scope {
		t.Fatalf("failed erasure changed authoritative membership: %+v %v", current, err)
	}
	if _, err := store.db.ExecContext(t.Context(), `DROP TRIGGER reject_native_value_erasure ON cao_value_proof_files;
		DROP FUNCTION reject_native_value_erasure()`); err != nil {
		t.Fatal(err)
	}
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, 7, []string{scope.Repository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"operational_value_evidence", "operational_value_evidence_facts",
		"cao_value_publications", "cao_value_proof_files"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table)+
			" WHERE namespace=$1", store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("withdrawal retained %s: %d %v", table, count, err)
		}
	}
	if _, err := store.Acquisition().PublishValue(t.Context(), scope, retained, laterPublication(t), proof,
		time.Now()); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("withdrawn worker could republish: %v", err)
	}
	nextScope := nativeValueScope(t, replica)
	if nextScope.Epoch <= scope.Epoch {
		t.Fatal("re-enrollment reused the withdrawn epoch")
	}
	seedPublicationParents(t, store, packet)
	if _, err := store.Acquisition().PublishValue(t.Context(), scope, nil, packet, proof,
		time.Now()); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old worker retargeted re-enrolled parents: %v", err)
	}
	if _, _, err := store.Acquisition().ReadValue(t.Context(), scope, "verify-bugs"); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old worker could restore a new owner's proof: %v", err)
	}
	if _, err := replica.Acquisition().PublishValue(t.Context(), nextScope, nil, packet, proof,
		time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeValuePublicationValidatesStartingIdentity(t *testing.T) {
	scope := collectnative.EnrollmentScope{Repository: "octo/example", InstallationID: 7, Epoch: 1}
	packet := evidencePacket(t)
	other := evidencePacket(t)
	other["repository"] = "octo/other"
	other["repositoryId"] = "repository:octo%2Fother"
	other["workflowId"] = "workflow:octo%2Fother%3A.github%2Fworkflows%2Fverify-bugs.md"
	for _, input := range []struct {
		scope    collectnative.EnrollmentScope
		previous model.Row
		packet   model.Row
	}{
		{collectnative.EnrollmentScope{}, nil, packet},
		{scope, nil, other},
		{scope, other, packet},
	} {
		if _, err := (Acquisition{}).PublishValue(t.Context(), input.scope, input.previous, input.packet,
			nil, time.Now()); err == nil {
			t.Fatal("invalid source or starting identity reached storage")
		}
	}
}
