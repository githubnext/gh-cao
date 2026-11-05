package collect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func TestNativeValueBridgeRejectsWithdrawnSourceBeforeCacheOrQuota(t *testing.T) {
	checked := 0
	api := remoteValueAPI{authorize: func(context.Context) error {
		checked++
		return collectnative.ErrClaimLost
	}}
	for _, path := range []string{"/api/repos/octo/api/issues", "/graphql", "/artifact"} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("withdrawn source reached API bridge: %s %d", path, recorder.Code)
		}
	}
	if checked != 3 || api.used != 0 || api.sent != 0 || api.cacheHits != 0 {
		t.Fatalf("source rejection reached quota, transport or response cache: checked=%d used=%d sent=%d hits=%d",
			checked, api.used, api.sent, api.cacheHits)
	}
}

func TestNativeValueLaneRequiresExplicitSharedDependencies(t *testing.T) {
	if err := (NativeValueService{}).Validate(); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("unconfigured Value lane was silently selected: %v", err)
	}
	if err := (Projector{}).CollectScopedRemoteValues(t.Context(), collectnative.EnrollmentScope{
		Repository: "octo/api", InstallationID: 7, Epoch: 1,
	}); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("native Value bypassed authoritative enrollment: %v", err)
	}
}

func TestNativeValuePlanTracksComposedPolicyAuthority(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "cao.json")) // #nosec G304 -- fixed checked-in policy fixture.
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	base := filepath.Join(directory, "cao.json")
	profile := filepath.Join(directory, "profile.json")
	if err := os.WriteFile(base, content, 0o600); err != nil { // #nosec G703 -- fixed filename beneath t.TempDir.
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte(`{
		"extends":"cao.json",
		"control-plane":{"web":{"host":{"target":{"module":"azure-functions"}}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	projector := Projector{
		CatalogRoot: root, PolicyPath: profile, RemoteValuePlan: &RemoteValuePlanCache{},
		Enrollment: Enrollment{Authority: new(postgresx.Store).NativeEnrollment()},
	}
	entries, digest, err := projector.remoteValuePlan(t.Context())
	if err != nil || len(entries) == 0 {
		t.Fatalf("composed reviewed registrations unavailable: entries=%d err=%v", len(entries), err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	document["control-plane"].(map[string]any)["campaigns"].(map[string]any)["activity"].(map[string]any)["enabled"] = false
	content, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, content, 0o600); err != nil {
		t.Fatal(err)
	}
	current, changed, err := projector.remoteValuePlan(t.Context())
	if err != nil || len(current) != 0 || changed == digest {
		t.Fatalf("base-policy revocation hidden by unchanged profile: entries=%d changed=%v err=%v",
			len(current), changed != digest, err)
	}
	if projector.RemoteValuePlan.loaded {
		t.Fatal("native authority was cached by entry-profile bytes")
	}
}
