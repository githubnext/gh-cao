package postgresx

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestValueProofPersistsAtomicallyAcrossConnections(t *testing.T) {
	store, config := nativeTestStore(t)
	replica, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	files := []ValueProofFile{
		{Path: "nested/inventory.json", Content: []byte(`{"files":3}`)},
		{Path: "receipts/accepted.txt", Content: []byte("accepted")},
	}
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	if _, err := store.PublishOperationalValueWithProof(
		t.Context(), packet, "octo/example", "verify-bugs", files, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := replica.ReadValueProof(t.Context(), "octo/example", "verify-bugs")
	if err != nil || len(got) != 2 || got[0].Path != "nested/inventory.json" ||
		!bytes.Equal(got[1].Content, []byte("accepted")) {
		t.Fatalf("proof=%+v err=%v", got, err)
	}
	if _, err := replica.PublishOperationalValueWithProof(t.Context(), laterPublication(t),
		"octo/example", "verify-bugs",
		[]ValueProofFile{{Path: "next.txt", Content: []byte("next")}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = store.ReadValueProof(t.Context(), "octo/example", "verify-bugs")
	if err != nil || len(got) != 1 || got[0].Path != "next.txt" {
		t.Fatalf("replacement proof=%+v err=%v", got, err)
	}
	if err := store.DeleteValueProofs(t.Context(), "octo/example"); err != nil {
		t.Fatal(err)
	}
	if got, err := replica.ReadValueProof(t.Context(), "octo/example", "verify-bugs"); err != nil || len(got) != 0 {
		t.Fatalf("deleted proof=%+v err=%v", got, err)
	}
}

func TestValueProofPublishesAtomicallyWithEvidence(t *testing.T) {
	store, _ := nativeTestStore(t)
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	initial, err := store.PublishOperationalValueWithProof(t.Context(), packet, "octo/example", "verify-bugs",
		[]ValueProofFile{{Path: "proof.txt", Content: []byte("initial")}}, time.Now())
	if err != nil || !initial.Updated {
		t.Fatalf("initial publication=%+v err=%v", initial, err)
	}
	if _, err := store.db.ExecContext(t.Context(), `CREATE FUNCTION reject_value_proof() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject proof'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(t.Context(), `CREATE TRIGGER reject_value_proof
		BEFORE INSERT ON cao_value_proof_files FOR EACH ROW EXECUTE FUNCTION reject_value_proof()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS reject_value_proof ON cao_value_proof_files`)
		_, _ = store.db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS reject_value_proof()`)
	}()
	next := laterPublication(t)
	if _, err := store.PublishOperationalValueWithProof(t.Context(), next, "octo/example", "verify-bugs",
		[]ValueProofFile{{Path: "proof.txt", Content: []byte("next")}}, time.Now()); err == nil {
		t.Fatal("proof storage failure did not roll back evidence publication")
	}
	retained, err := store.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs")
	if err != nil || retained["observedAt"] != packet["observedAt"] {
		t.Fatalf("failed proof replacement changed evidence: %v %v", retained, err)
	}
	proof, err := store.ReadValueProof(t.Context(), "octo/example", "verify-bugs")
	if err != nil || len(proof) != 1 || string(proof[0].Content) != "initial" {
		t.Fatalf("failed evidence publication changed proof: %+v %v", proof, err)
	}
}

func TestValueCollectionCostsAggregateByDayAndExposeBoundedSource(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for _, record := range []ValueCollectionCost{
		{
			At: at, Installation: 7, Repository: "octo/api", Workflow: "daily-file-diet",
			Collector:   ".github/value-collectors/daily-file-diet.mjs",
			APIRequests: 6, CoreRequests: 3, SearchRequests: 2, GraphQLRequests: 1,
			CacheHits: 4, PreflightRequests: 1, GraphQLPointsObserved: 3,
			WallMS: 2000, OutputBytes: 100, TransportBytes: 200, Succeeded: true,
		},
		{
			At: at.Add(time.Hour), Installation: 7, Repository: "octo/api", Workflow: "daily-file-diet",
			Collector:   ".github/value-collectors/daily-file-diet.mjs",
			APIRequests: 2, CoreRequests: 2, WallMS: 5000, OutputBytes: 20,
			TransportBytes: 30, FailureReason: "timeout",
		},
	} {
		if err := store.AppendValueCollectionCost(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	source, err := store.ValueCollectionCostSource(t.Context(), at.Add(-time.Hour), 10)
	if err != nil || len(source.Rows) != 1 {
		t.Fatalf("source=%+v err=%v", source, err)
	}
	row := source.Rows[0]
	if row["collectors"] != int64(1) || row["installations"] != int64(1) || row["repositories"] != int64(1) ||
		row["attempts"] != int64(2) || row["successes"] != int64(1) || row["failures"] != int64(1) ||
		row["api-requests"] != int64(8) || row["core-requests"] != int64(5) ||
		row["search-requests"] != int64(2) || row["graphql-requests"] != int64(1) ||
		row["max-wall-ms"] != int64(5000) || row["last-failure-reason"] != "timeout" {
		t.Fatalf("cost row=%+v", row)
	}
}
