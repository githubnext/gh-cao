package postgresx

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func testValueBatch(t *testing.T, capture string) []model.Row {
	t.Helper()
	packets := make([]model.Row, 3)
	for index := range packets {
		content, err := json.Marshal(evidencePacket(t))
		if err != nil {
			t.Fatal(err)
		}
		var row model.Row
		if err := json.Unmarshal(content, &row); err != nil {
			t.Fatal(err)
		}
		metric := "linked-" + string(rune('a'+index))
		row["id"] = "operational-value-evidence:" + url.QueryEscape("octo/example:verify-bugs:"+metric+":v1")
		row["observedAt"] = capture
		for _, raw := range row["facts"].([]any) {
			fact := raw.(map[string]any)
			if fact["fact-kind"] == "contract" {
				fact["metric-id"] = metric
			}
		}
		packets[index] = row
	}
	return packets
}

func TestNativeValueBatchPublicationIsAtomicAndEpochFenced(t *testing.T) {
	store, config := nativeTestStore(t)
	replica, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	packets := testValueBatch(t, "2026-10-20T12:00:00Z")
	seedPublicationParents(t, store, packets[0])
	scope := nativeValueScope(t, store)
	starting, files, err := replica.Acquisition().ReadValueBatch(t.Context(), scope, "verify-bugs")
	if err != nil || len(starting) != 0 || len(files) != 0 {
		t.Fatalf("new workflow had prior evidence: %d %d %v", len(starting), len(files), err)
	}
	proof := []ValueProofFile{{Path: "lifecycle.json", Content: []byte("complete")}}
	accepted, err := store.Acquisition().PublishValueBatch(t.Context(), scope, nil, packets, proof, time.Now())
	if err != nil || !accepted.Updated {
		t.Fatalf("complete metric batch rejected: %+v %v", accepted, err)
	}
	retry, err := replica.Acquisition().PublishValueBatch(t.Context(), scope, nil, packets, proof, time.Now())
	if err != nil || retry.Updated || retry.Revision != accepted.Revision {
		t.Fatalf("exact committed retry failed: %+v %v", retry, err)
	}
	if _, err := replica.Acquisition().PublishValueBatch(t.Context(), scope, nil, packets,
		[]ValueProofFile{{Path: "lifecycle.json", Content: []byte("different")}}, time.Now()); !errors.Is(err, ErrValueConflict) {
		t.Fatalf("committed evidence accepted different proof: %v", err)
	}
	starting, files, err = replica.Acquisition().ReadValueBatch(t.Context(), scope, "verify-bugs")
	if err != nil || len(starting) != 3 || len(files) != 1 || string(files[0].Content) != "complete" {
		t.Fatalf("replica did not read an atomic packet/proof snapshot: %d %+v %v", len(starting), files, err)
	}
	next := testValueBatch(t, "2026-10-20T13:00:00Z")
	next[2]["contractDigest"] = "not-a-digest"
	if _, err := store.Acquisition().PublishValueBatch(t.Context(), scope, starting, next, proof, time.Now()); err == nil {
		t.Fatal("invalid third packet partially replaced the first two")
	}
	unchanged, files, err := store.Acquisition().ReadValueBatch(t.Context(), scope, "verify-bugs")
	if err != nil || len(unchanged) != 3 || len(files) != 1 {
		t.Fatalf("failed batch changed publication: %d %v", len(unchanged), err)
	}
	for _, row := range unchanged {
		if row["observedAt"] != "2026-10-20T12:00:00Z" {
			t.Fatalf("failed batch advanced a packet: %+v", row)
		}
	}
	next[2]["contractDigest"] = packets[2]["contractDigest"]
	updated, err := replica.Acquisition().PublishValueBatch(t.Context(), scope, starting, next, proof, time.Now())
	if err != nil || !updated.Updated {
		t.Fatalf("complete batch did not advance: %+v %v", updated, err)
	}
	if _, err := store.Acquisition().PublishValueBatch(t.Context(), scope, starting,
		testValueBatch(t, "2026-10-20T14:00:00Z"), proof, time.Now()); !errors.Is(err, ErrValueConflict) {
		t.Fatalf("stale starting packets replaced newer evidence: %v", err)
	}
	authority := replica.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := authority.Withdraw(ctx, scope.InstallationID, []string{scope.Repository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Acquisition().ReadValueBatch(t.Context(), scope, "verify-bugs"); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("withdrawn scope read accepted evidence: %v", err)
	}
	if _, err := store.Acquisition().PublishValueBatch(t.Context(), scope, starting, next, proof, time.Now()); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("withdrawn scope published accepted evidence: %v", err)
	}
}
