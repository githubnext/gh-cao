package collectnative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func inventoryTestProvenance() InventoryProvenance {
	return InventoryProvenance{SourceRepository: "octo/api", SourceSHA: strings.Repeat("a", 40), PolicySHA: strings.Repeat("b", 40)}
}

func inventoryTestSnapshot(t *testing.T, status string) InventorySnapshot {
	t.Helper()
	document := inventorySnapshotDocument{InventoryProvenance: inventoryTestProvenance(), ControlRepository: "octo/api", Version: 1, PolicyResolution: status}
	if err := decode([]byte(`{
		"campaigns":{"source":"campaigns","rows":[{"campaign":"security","campaign-name":"Security","campaign-source":"octo/catalog/security","campaign-mode":"review","campaign-enabled":false,"campaign-version":"aaaaaaaaaaaa","campaign-current-version":"unknown","campaign-update-state":"unknown"}]},
		"repositories":{"source":"repositories","rows":[{"organization":"octo","repository":"api"},{"organization":"untrusted","repository":"fleet"}]},
		"workflows":{"source":"workflows","rows":[{"organization":"octo","repository":"api","workflow":".github/workflows/test.md","workflow-name":"Test","workflow-role":"worker","workflow-source-kind":"source-managed","campaign":"security","inventory-ready":false,"admission-status":"blocked","admission-reason":"worker-disabled","gh-aw-version":"unknown","rollout-mode":"review"}]},
		"configuration-policy":{"source":"configuration-policy","rows":[{"path":".github/workflows/cao.json","raw":"reviewed raw policy","document":{"control-plane":{"mode":"review"}},"diagnostics":[{"severity":"valid","title":"Policy is valid"}]}]}
	}`), &document.Sources); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	snapshot, err := NewInventorySnapshot(content, hex.EncodeToString(sum[:]), document.InventoryProvenance)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func inventoryTestGitHub(t *testing.T, partial bool) (GitHub, *atomic.Int64) {
	t.Helper()
	requests := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/octo/api":
			_, _ = fmt.Fprint(w, `{"full_name":"octo/api","visibility":"private","default_branch":"main"}`)
		case "/repos/octo/api/commits/main":
			_, _ = fmt.Fprintf(w, `{"sha":"%s"}`, strings.Repeat("c", 40))
		case "/repos/github/gh-aw/releases":
			if partial {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, `[{"tag_name":"v1.2.0","draft":false,"prerelease":false}]`)
		case "/repos/octo/api/actions/workflows":
			if r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != "1" {
				t.Error("unbounded or unexpected registry page")
			}
			_, _ = fmt.Fprint(w, `{"total_count":2,"workflows":[{"id":123,"name":"Remote test","path":".github/workflows/test.lock.yml","state":"disabled_manually","html_url":"https://github.com/octo/api/actions/workflows/123"},{"id":124,"name":"YAML workflow","path":".github/workflows/plain.yml","state":"active"}]}`)
		case "/repos/octo/api/contents/.github/workflows/test.lock.yml":
			if r.URL.Query().Get("ref") != strings.Repeat("c", 40) {
				t.Error("compiler metadata was not read at explicitly pinned repository revision")
			}
			if partial {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			encoded := base64.StdEncoding.EncodeToString([]byte("# gh-aw-metadata: {\"compiler_version\":\"v1.1.0\"}\n"))
			_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, encoded)
		default:
			t.Errorf("unexpected inventory scope/request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}, requests
}

func TestNativeInventorySourceBoundNoSubprocess(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	provider, requests := inventoryTestGitHub(t, false)
	t.Setenv("PATH", "/nonexistent")
	scope := EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	evidence, err := provider.BuildInventory(t.Context(), scope, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 || len(evidence.Sources["repositories"].Rows) != 1 || len(evidence.Sources["workflows"].Rows) != 2 {
		t.Fatalf("scope or bounded acquisition changed: %d %+v", requests.Load(), evidence.Sources)
	}
	workflow := evidence.Sources["workflows"].Rows[1]
	if workflow["workflow-role"] != "worker" || workflow["campaign"] != "security" ||
		workflow["inventory-ready"] != false || workflow["admission-status"] != "blocked" ||
		workflow["workflow-active"] != "false" || workflow["gh-aw-version"] != "v1.1.0" ||
		workflow["gh-aw-update-state"] != "update-available" {
		t.Fatalf("reviewed campaign metadata or remote compiler evidence lost: %+v", workflow)
	}
	document, _ := snapshot.document()
	if !reflect.DeepEqual(evidence.Sources["configuration-policy"].Rows, document.Sources["configuration-policy"].Rows) {
		t.Fatal("configuration-policy diagnostics were dropped")
	}
	if evidence.Sources["campaigns"].Metadata["freshness"] != "unknown" {
		t.Fatal("source-timed maintenance evidence was misrepresented as refreshed")
	}
	workflow["workflow-role"] = "orchestrator"
	if err := ValidateInventoryEvidence(evidence); err == nil {
		t.Fatal("modified reviewed role was admitted")
	}
}

func TestNativeInventoryPartialEvidenceAndFailClosedPolicy(t *testing.T) {
	provider, requests := inventoryTestGitHub(t, true)
	scope := EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	if _, err := provider.BuildInventory(t.Context(), scope, inventoryTestSnapshot(t, "unavailable")); err == nil || requests.Load() != 0 {
		t.Fatal("unavailable reviewed policy was promoted or used GitHub")
	}
	evidence, err := provider.BuildInventory(t.Context(), scope, inventoryTestSnapshot(t, "available"))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Sources["workflows"].Metadata["completeness"] != "partial" {
		t.Fatal("missing registry/version evidence was silently completed")
	}
	for _, row := range evidence.Sources["workflows"].Rows {
		if row["gh-aw-version"] != "unknown" || row["gh-aw-current-version"] != "unknown" || row["gh-aw-update-state"] != "unknown" {
			t.Fatalf("unresolved maintenance evidence invented: %+v", row)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.BuildInventory(ctx, scope, inventoryTestSnapshot(t, "available")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestNativeInventoryImmutableRemoteSnapshot(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octo/api/contents/reviewed/inventory.json" || r.URL.Query().Get("ref") != strings.Repeat("d", 40) {
			t.Errorf("unpinned source snapshot acquisition: %s", r.URL)
		}
		_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString(snapshot.content))
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	ref := InventorySnapshotRef{Repository: "octo/api", InstallationID: 7, SHA: strings.Repeat("d", 40),
		Path: "reviewed/inventory.json", Digest: snapshot.Digest, Provenance: snapshot.Provenance}
	t.Setenv("PATH", "/nonexistent")
	got, err := provider.LoadInventorySnapshot(t.Context(), ref)
	if err != nil || got.Digest != snapshot.Digest {
		t.Fatalf("remote immutable source snapshot unavailable: %v", err)
	}
	ref.Digest = strings.Repeat("0", 64)
	if _, err := provider.LoadInventorySnapshot(t.Context(), ref); err == nil {
		t.Fatal("snapshot digest mismatch accepted")
	}
	for _, unsafe := range []string{"../policy.json", "/policy.json", "a//b.json", "a?ref=main"} {
		ref.Path = unsafe
		if _, err := provider.LoadInventorySnapshot(t.Context(), ref); err == nil {
			t.Fatalf("unsafe object path %q accepted", unsafe)
		}
	}
}

func TestNativeInventoryActivityOracle(t *testing.T) {
	// The production implementation never invokes Node. This independent
	// producer oracle freezes the existing Activity source contract.
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { buildInventoryDashboardSources } from "./dashboard/report/inventory-sources.mjs";
const generatedAt = "2026-10-01T00:00:00Z";
const input = {
 repository:"octo/api",generatedAt,
 inventory:{generatedAt,bundles:[{id:"security",workflow:".github/workflows/test.md",name:"Security",compiled:true,workers:[],source:"octo/catalog/security",version:"a".repeat(40)}],
 workflows:[{id:"test",sourcePath:".github/workflows/test.md",name:"Test",compiled:true,source:"octo/catalog/security@"+ "a".repeat(40),ghAwVersion:"v1.1.0"}]},
 controlSettings:{policy_resolution:{status:"available"},policy_document:{"control-plane":{mode:"review"}},policy_source:"reviewed",
 campaigns:{security:{enabled:false,mode:"review"}},allowed_repositories:["octo/api"]},
 discoveredRepositories:[{full_name:"octo/api",visibility:"private"}],
 latestGhAwVersion:"v1.2.0"
};
const sources = buildInventoryDashboardSources(input);
const enriched = buildInventoryDashboardSources({...input,workflowRegistries:[{repository:"octo/api",state:"complete",workflows:[
{repository:"octo/api",id:123,name:"Remote test",path:".github/workflows/test.lock.yml",state:"disabled_manually",htmlUrl:"https://github.com/octo/api/actions/workflows/123",ghAwVersion:"v1.1.0"},
{repository:"octo/api",id:124,name:"YAML workflow",path:".github/workflows/plain.yml",state:"active",htmlUrl:""}
]}]});
console.log(JSON.stringify({snapshot:{version:1,controlRepository:"octo/api",sourceRepository:"octo/api",sourceSHA:"a".repeat(40),policySHA:"b".repeat(40),policyResolution:"available",sources},expected:enriched}));
`)
	command.Dir = "../../.."
	content, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Activity oracle failed (Node is test-only): %v %s", err, content)
	}
	var oracle struct {
		Snapshot json.RawMessage         `json:"snapshot"`
		Expected map[string]model.Source `json:"expected"`
	}
	if err := decode(content, &oracle); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(oracle.Snapshot)
	snapshot, err := NewInventorySnapshot(oracle.Snapshot, hex.EncodeToString(sum[:]), inventoryTestProvenance())
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := inventoryTestGitHub(t, false)
	t.Setenv("PATH", "/nonexistent")
	evidence, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"campaigns", "configuration-policy", "workflows"} {
		actual := evidence.Sources[name].Rows
		expected := oracle.Expected[name].Rows
		for i := range actual {
			if name == "workflows" && actual[i]["workflow-source-kind"] == "registry" {
				actual[i]["observed-at"] = "2026-10-01T00:00:00Z"
			}
		}
		got, _ := json.Marshal(actual)
		want, _ := json.Marshal(expected)
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from Activity producer:\ngot %s\nwant %s", name, got, want)
		}
	}
}

func TestNativeInventoryCompilerVersionOracleCases(t *testing.T) {
	for _, test := range []struct{ installed, latest, state string }{
		{"v1.2.3", "v1.2.4", "update-available"}, {"v1.10.0", "v1.9.9", "up-to-date"},
		{"v1.2.3-alpha.2", "v1.2.3-alpha.10", "update-available"},
		{"v1.2.3-alpha", "v1.2.3", "update-available"}, {"v1.2.3", "v1.2.3-alpha", "up-to-date"},
		{"v1.2.3-01", "v1.2.3", "unknown"}, {"unknown", "v1.2.3", "unknown"},
	} {
		if got := inventoryVersionState(test.installed, test.latest); got != test.state {
			t.Errorf("%s -> %s: %s != %s", test.installed, test.latest, got, test.state)
		}
	}
}

func TestNativeInventoryRegistryBudgetIsExplicitlyPartial(t *testing.T) {
	var pages atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/octo/api":
			_, _ = fmt.Fprint(w, `{"full_name":"octo/api","visibility":"private","default_branch":"main"}`)
		case "/repos/octo/api/commits/main":
			_, _ = fmt.Fprintf(w, `{"sha":"%s"}`, strings.Repeat("c", 40))
		case "/repos/github/gh-aw/releases":
			_, _ = fmt.Fprint(w, `[{"tag_name":"v1.2.0"}]`)
		case "/repos/octo/api/actions/workflows":
			page := pages.Add(1)
			workflows := make([]model.Row, 100)
			for i := range workflows {
				id := (page-1)*100 + int64(i)
				workflows[i] = model.Row{"id": id, "name": fmt.Sprintf("Workflow %d", id),
					"path": fmt.Sprintf(".github/workflows/ci-%04d.yml", id), "state": "active"}
			}
			if err := json.NewEncoder(w).Encode(model.Row{"total_count": 1001, "workflows": workflows}); err != nil {
				t.Error(err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	evidence, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, inventoryTestSnapshot(t, "available"))
	if err != nil {
		t.Fatal(err)
	}
	metadata := evidence.Sources["workflows"].Metadata
	if pages.Load() != 10 || metadata["completeness"] != "partial" || metadata["registry-complete"] != false ||
		metadata["expected"] != 1001 || metadata["observed"] != 1000 {
		t.Fatalf("registry budget silently truncated or widened: %d %+v", pages.Load(), metadata)
	}
}

func TestNativeInventoryCancelsInflightGitHub(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	snapshot := inventoryTestSnapshot(t, "available")
	go func() {
		_, err := provider.BuildInventory(ctx, EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory request never entered provider")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("inflight cancellation was not preserved: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled inventory provider did not return")
	}
}

func TestNativeInventoryRejectsSourceWithoutControlCampaign(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	var document inventorySnapshotDocument
	if err := decode(snapshot.content, &document); err != nil {
		t.Fatal(err)
	}
	document.Sources["workflows"] = model.Source{Source: "workflows", Rows: []model.Row{}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	snapshot, err = NewInventorySnapshot(raw, hex.EncodeToString(sum[:]), snapshot.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	provider, requests := inventoryTestGitHub(t, false)
	if _, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot); err == nil || requests.Load() != 0 {
		t.Fatal("standalone registry substituted for source-bound control/campaign inventory")
	}
}

func TestNativeInventoryReturnedEvidenceCannotMutateSnapshot(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	provider, _ := inventoryTestGitHub(t, false)
	evidence, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	evidence.SourceSnapshot[0] = '!'
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("returned evidence mutated immutable deployment snapshot: %v", err)
	}
}

func TestNativeInventoryRejectsUnreviewedDeclarations(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	provider, _ := inventoryTestGitHub(t, false)
	evidence, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		index int
		field string
		value any
	}{
		{"registry-readiness", 0, "inventory-ready", true},
		{"registry-admission", 0, "admission-status", "admitted"},
		{"registry-rollout", 0, "rollout-mode", "live"},
		{"source-extra-field", 1, "unreviewed-declaration", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			original, exists := evidence.Sources["workflows"].Rows[test.index][test.field]
			evidence.Sources["workflows"].Rows[test.index][test.field] = test.value
			if err := ValidateInventoryEvidence(evidence); err == nil {
				t.Fatal("unreviewed workflow declaration was accepted")
			}
			if exists {
				evidence.Sources["workflows"].Rows[test.index][test.field] = original
			} else {
				delete(evidence.Sources["workflows"].Rows[test.index], test.field)
			}
		})
	}
	if err := ValidateInventoryEvidence(evidence); err != nil {
		t.Fatalf("valid inventory was rejected: %v", err)
	}
}
