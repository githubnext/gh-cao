package collect

import (
	"context"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type syncInventoryProvider struct {
	*runtimeInventoryProvider
	visibility string
}

func (p *syncInventoryProvider) BuildInventory(ctx context.Context, scope collectnative.EnrollmentScope, snapshot collectnative.InventorySnapshot) (collectnative.InventoryEvidence, error) {
	evidence, err := p.runtimeInventoryProvider.BuildInventory(ctx, scope, snapshot)
	if err == nil {
		evidence.Sources["repositories"].Rows[0]["visibility"] = p.visibility
	}
	return evidence, err
}

func TestNativeInventorySyncPublishesChangedKnownRepositoryMetadata(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/z-control"}); err != nil {
		t.Fatal(err)
	}
	snapshot, base := runtimeInventorySnapshot(t)
	provider := &syncInventoryProvider{runtimeInventoryProvider: base, visibility: "private"}
	inventory, err := NewNativeInventoryService(t.Context(), snapshot, provider, data.Acquisition())
	if err != nil {
		t.Fatal(err)
	}
	worker := nativeEventWorker(queue, enrollment, &nativeRefreshFixture{})
	worker.Inventory = inventory
	for _, visibility := range []string{"private", "public"} {
		provider.visibility = visibility
		if err := queue.SyncInventory(t.Context(), enrollment); err != nil {
			t.Fatal(err)
		}
		if more, err := worker.Step(t.Context()); err != nil || !more {
			t.Fatalf("inventory sync publication: %v %v", more, err)
		}
		sources, _, err := data.ExecuteSQLPlan(t.Context(), []query.Definition{{
			Name: "repositories", From: "$repositories", Select: []query.SelectedField{{Field: "visibility"}},
		}}, []string{"repositories"})
		if err != nil || len(sources["repositories"].Rows) != 1 || sources["repositories"].Rows[0]["visibility"] != visibility {
			t.Fatalf("known repository metadata did not refresh to %s: %+v %v", visibility, sources, err)
		}
	}
	if len(provider.calls) != 2 {
		t.Fatalf("known ready inventory suppressed fresh acquisition: %v", provider.calls)
	}
}
