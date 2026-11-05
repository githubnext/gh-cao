package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func hostedNativeInventory(t *testing.T) collectnative.InventorySnapshot {
	t.Helper()
	provenance := collectnative.InventoryProvenance{
		SourceRepository: "octo/api", SourceSHA: strings.Repeat("a", 40), PolicySHA: strings.Repeat("b", 40),
	}
	content := []byte(fmt.Sprintf(`{
		"version":1,"controlRepository":"octo/api","policyResolution":"available",
		"sourceRepository":"octo/api","sourceSHA":%q,"policySHA":%q,
		"sources":{
			"campaigns":{"source":"campaigns","rows":[{"campaign":"security","campaign-name":"Security","campaign-mode":"review","campaign-enabled":false,"campaign-source":"octo/catalog/security","campaign-version":"aaaaaaaaaaaa","campaign-current-version":"unknown","campaign-update-state":"unknown"}]},
			"repositories":{"source":"repositories","rows":[{"organization":"octo","repository":"api","visibility":"private"}]},
			"workflows":{"source":"workflows","rows":[{"organization":"octo","repository":"api","workflow":".github/workflows/test.md","workflow-name":"Test","workflow-role":"worker","workflow-source-kind":"source-managed","campaign":"security","inventory-ready":false,"admission-status":"blocked","admission-reason":"worker-disabled","gh-aw-version":"unknown","rollout-mode":"review"}]},
			"configuration-policy":{"source":"configuration-policy","rows":[{"path":".github/workflows/cao.json","raw":"synthetic reviewed policy","document":{"control-plane":{"mode":"review"}},"diagnostics":[{"severity":"valid","title":"Policy is valid"}]}]}
		}}`, provenance.SourceSHA, provenance.PolicySHA))
	sum := sha256.Sum256(content)
	snapshot, err := collectnative.NewInventorySnapshot(content, hex.EncodeToString(sum[:]), provenance)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestGatedNativeHostedAssemblyRunsWithoutLegacyFilesOrExecutables(t *testing.T) {
	data, pgconfig := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	endpoint, err := url.Parse(pgconfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parameters := endpoint.Query()
	parameters.Set("search_path", pgconfig.RuntimeParams["search_path"])
	endpoint.RawQuery = parameters.Encode()
	operational, err := operationalpostgres.New(t.Context(), endpoint.String(), "native-hosted-assembly", operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = operational.Close() }()
	services := operational.OperationalServices()
	quota, err := githubquota.New(services.GitHubQuota, githubquota.Options{SafetyReserve: 10})
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4900")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		path := strings.TrimPrefix(r.URL.Path, "/api/v3")
		switch path {
		case "/app/installations":
			_, _ = w.Write([]byte(`[{"id":7,"account":{"login":"octo"}}]`))
		case "/app/installations/7/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "synthetic-installation-token", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
		case "/installation/repositories":
			_, _ = w.Write([]byte(`{"total_count":1,"repositories":[{"id":11,"full_name":"octo/api","private":true,"visibility":"private"}]}`))
		case "/rate_limit":
			_ = json.NewEncoder(w).Encode(map[string]any{"resources": map[string]any{"core": map[string]any{
				"limit": 5000, "remaining": 4900, "reset": time.Now().Add(time.Hour).Unix(),
			}}})
		case "/repos/octo/api":
			_, _ = w.Write([]byte(`{"full_name":"octo/api","private":true,"visibility":"private"}`))
		case "/repos/octo/api/actions/workflows":
			_, _ = w.Write([]byte(`{"total_count":1,"workflows":[{"id":1,"name":"Test","path":".github/workflows/test.lock.yml","state":"active"}]}`))
		case "/repos/octo/api/actions/workflows/1/runs":
			_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
		case "/repos/octo/api/actions/runs/303/attempts/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 303, "run_attempt": 1, "path": ".github/workflows/test.lock.yml",
				"repository": map[string]any{"full_name": "octo/api"}, "status": "completed", "conclusion": "success",
				"created_at": time.Now().UTC().Format(time.RFC3339)})
		case "/repos/octo/api/actions/runs/303/artifacts":
			_, _ = w.Write([]byte(`{"total_count":0,"artifacts":[]}`))
		case "/repos/octo/api/actions/runs/303/attempts/1/jobs":
			_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
		case "/repos/github/gh-aw/releases/latest":
			http.NotFound(w, r)
		case "/repos/github/gh-aw/releases":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected native hosted endpoint: %s", path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := hostedNativeInventory(t)
	config := CollectorConfig{NativeAcquisition: true, NativeInventory: &snapshot, AppID: 42, Workers: 1, WindowDays: 7,
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		BaseURL:       api.URL + "/api/v3/", ControlRepository: "octo/api",
		LakeDirectory: "/must-not-read", CatalogRoot: "/must-not-read", PolicyPath: "/must-not-read", StaticInventoryPath: "/must-not-read"}
	queue := collect.Queue{Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics, MaxLength: 100}
	t.Setenv("PATH", "/nonexistent")
	// This exercises the real composition below its explicit public activation
	// gate, not a claim of full N01 audit or Value profile equivalence.
	collector, err := newNativeCollector(t.Context(), services, operational.Capabilities(), data, config, quota, queue)
	if err != nil {
		t.Fatal(err)
	}
	if collector.lake.Directory != "" || collector.runner.CatalogRoot != "" || collector.projector.State != nil {
		t.Fatal("native hosted assembly constructed the legacy filesystem pipeline")
	}
	replicaConfig := config
	replicaConfig.NativeInventory = nil
	replicaConfig.NativeInventoryDigest = snapshot.Digest
	replica, err := newNativeCollector(t.Context(), services, operational.Capabilities(), data, replicaConfig, quota, queue)
	if err != nil || replica == nil || replica.config.NativeInventory == nil ||
		replica.config.NativeInventory.Digest != snapshot.Digest {
		t.Fatalf("replica could not restore reviewed configuration without local files: %v", err)
	}
	valueConfig := replicaConfig
	valueConfig.NativeValues = true
	if _, err := newNativeCollector(t.Context(), services, operational.Capabilities(), data, valueConfig, quota, queue); err == nil || !strings.Contains(err.Error(), "requires Node") {
		t.Fatalf("selected Value execution silently ignored its missing Node dependency: %v", err)
	}
	if _, err := collector.Rebuild(t.Context()); err != nil {
		t.Fatal("native cold-start assembly:", err)
	}
	payload := []byte(`{"action":"completed","installation":{"id":7},"repository":{"full_name":"octo/api"},
		"workflow_run":{"id":303,"run_attempt":1,"path":".github/workflows/test.lock.yml","status":"completed"}}`)
	if result, err := collector.Admit(t.Context(), GitHubWebhook{Event: "workflow_run", Delivery: "native-hosted-completion", Payload: payload}); err != nil || result["queued"] != true {
		t.Fatalf("native webhook assembly: %+v %v", result, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- collector.Worker("native-hosted-runtime").Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("native runtime did not join: %v", err)
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, _, err := data.ExecuteSQLPlan(ctx, []query.Definition{{Name: "native-runs", From: "$runs"}}, []string{"native-runs"})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows["native-runs"].Rows) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native hosted worker did not activate an admitted exact attempt")
		case <-ticker.C:
		}
	}
	admission, err := newNativeCollector(t.Context(), services, operational.Capabilities(), data,
		CollectorConfig{NativeAcquisition: true, AppID: 42, AdmitOnly: true}, quota, queue)
	if err != nil || admission.client != nil || admission.native.inventory != nil || admission.runner.CatalogRoot != "" {
		t.Fatalf("native admission acquired worker credentials/files: %v", err)
	}
	if _, err := NewCollector(t.Context(), operational, data, config, ""); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("assembly test bypassed the public audit activation gate: %v", err)
	}
}
