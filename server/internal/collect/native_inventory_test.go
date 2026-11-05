package collect_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativeInventoryProvider struct {
	evidence collectnative.InventoryEvidence
	failure  bool
}

func (p *nativeInventoryProvider) BuildInventory(_ context.Context, scope collectnative.EnrollmentScope, _ collectnative.InventorySnapshot) (collectnative.InventoryEvidence, error) {
	if p.failure {
		return collectnative.InventoryEvidence{}, errors.New("provider unavailable")
	}
	evidence := p.evidence
	evidence.Scope = scope
	return evidence, nil
}

func TestNativeInventoryPostgresServiceDurableRetry(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	provenance := collectnative.InventoryProvenance{SourceRepository: "octo/api", SourceSHA: strings.Repeat("a", 40), PolicySHA: strings.Repeat("b", 40)}
	raw := []byte(`{"version":1,"controlRepository":"octo/api","sourceRepository":"octo/api","sourceSHA":"` + provenance.SourceSHA +
		`","policySHA":"` + provenance.PolicySHA + `","policyResolution":"available","sources":{
		"campaigns":{"source":"campaigns","rows":[{"campaign":"security","campaign-name":"Security"}]},
		"repositories":{"source":"repositories","rows":[{"organization":"octo","repository":"api"}]},
		"workflows":{"source":"workflows","rows":[{"organization":"octo","repository":"api","workflow":".github/workflows/test.md","workflow-role":"worker","workflow-source-kind":"source-managed","campaign":"security"}]},
		"configuration-policy":{"source":"configuration-policy","rows":[{"path":".github/workflows/cao.json","raw":"reviewed","diagnostics":[{"severity":"valid"}]}]}
	}}`)
	sum := sha256.Sum256(raw)
	snapshot, err := collectnative.NewInventorySnapshot(raw, hex.EncodeToString(sum[:]), provenance)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Sources map[string]model.Source `json:"sources"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	evidence := collectnative.InventoryEvidence{Provenance: provenance, Digest: snapshot.Digest, SourceSnapshot: raw,
		ObservedAt: time.Now().UTC(), Sources: document.Sources}
	a := store.Acquisition()
	if err := a.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	provider := &nativeInventoryProvider{evidence: evidence, failure: true}
	service, err := collect.NewNativeInventoryService(t.Context(), snapshot, provider, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	if err := service.RefreshInventory(t.Context(), scope); err == nil {
		t.Fatal("unavailable source provider succeeded")
	}
	if err := service.EnsureReady(t.Context(), scope); err == nil {
		t.Fatal("provider failure made empty inventory ready")
	}
	provider.failure = false
	if done, err := service.RetryPending(t.Context(), 10); err != nil || done != 1 {
		t.Fatalf("durable refresh intent did not converge: %d %v", done, err)
	}
	if err := service.EnsureReady(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if scopes, err := a.PendingInventoryScopes(t.Context(), 10); err != nil || len(scopes) != 0 {
		t.Fatalf("committed refresh left unnecessary pending work: %+v %v", scopes, err)
	}
}
