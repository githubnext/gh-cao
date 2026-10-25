package collect

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func TestRepositoryDiscoveryThroughGitHubSimulator(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	for _, action := range []string{"created", "archived", "unarchived", "renamed", "deleted", "removed"} {
		t.Run(action, func(t *testing.T) {
			scenario := simulator.Scenario{Name: action, Repositories: 101}
			switch action {
			case "created":
				scenario.CreateRepositories = true
			case "archived":
				scenario.ArchiveRepositories = true
			case "unarchived":
				scenario.ArchiveRepositories, scenario.UnarchiveRepositories = true, true
			case "renamed":
				scenario.RenameRepositories = true
			case "deleted":
				scenario.DeleteRepositories = true
			case "removed":
				scenario.RemoveRepositories = true
			}
			api, err := simulator.NewAPIHandler(scenario, 1)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(api)
			defer server.Close()
			client, err := githubapp.New(githubapp.Config{AppID: 1, PrivateKeyPEM: keyPEM, BaseURL: server.URL + "/"})
			if err != nil {
				t.Fatal(err)
			}
			w := refreshWorker(t)
			initial := []string{"simulator/repo-00101"}
			if action != "created" && action != "unarchived" {
				initial = append(initial, "simulator/repo-00001")
			}
			if err := w.Enrollment.AddRepositories(t.Context(), 1, initial); err != nil {
				t.Fatal(err)
			}
			w.ScopeRefresher = ScopeRefresher{Client: client, Quota: &fakeRunQuota{}, QuotaApp: "simulator"}
			admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
			deliveries, err := scenario.Generate()
			if err != nil {
				t.Fatal(err)
			}
			var selected simulator.Delivery
			for _, delivery := range deliveries {
				if delivery.Event == "repository" || action == "removed" && delivery.Event == "installation_repositories" {
					selected = delivery
				}
			}
			result, err := admitter.AdmitDelivery(t.Context(), selected.Event, selected.Payload, selected.ID, time.Hour)
			if err != nil || !result.Enqueued {
				t.Fatalf("simulated webhook not admitted: %+v, %v", result, err)
			}
			if api.Stats().Requests != 0 {
				t.Fatal("webhook admission called GitHub inline")
			}
			w.process(t.Context(), refreshLease(t, w.Queue))
			repositories, err := w.Enrollment.RepositoriesForInstallation(t.Context(), 1)
			want := 101
			if action == "archived" || action == "deleted" || action == "removed" {
				want = 100
			}
			if action == "removed" {
				// An explicit withdrawal removes only the named enrollment.
				want = 1
			}
			if err != nil || len(repositories) != want {
				t.Fatalf("simulator reconciliation produced %d repositories, want %d: %v", len(repositories), want, err)
			}
			if action == "renamed" {
				for name, want := range map[string]bool{"simulator/repo-00001": false, "simulator/repo-00001-renamed": true} {
					if enrolled, err := w.Enrollment.Enrolled(t.Context(), name); err != nil || enrolled != want {
						t.Fatalf("rename enrollment %s: %t, %v", name, enrolled, err)
					}
				}
			}
			if action != "removed" && api.Stats().Requests < 2 {
				t.Fatalf("discovery did not use the real paginated GitHub client: %+v", api.Stats())
			}
			leases, err := w.Queue.Lease(t.Context(), "simulator-followup", 200, 0)
			if err != nil {
				t.Fatal(err)
			}
			erase := 0
			for _, lease := range leases {
				if lease.Task.Erase {
					erase++
					if lease.Task.Repository != "simulator/repo-00001" {
						t.Fatalf("unexpected eviction task: %+v", lease)
					}
				}
			}
			wantErase := 0
			if action == "archived" || action == "deleted" || action == "renamed" || action == "removed" {
				wantErase = 1
			}
			if erase != wantErase {
				t.Fatalf("erasure tasks = %d, want %d", erase, wantErase)
			}
			if pending, err := w.Projector.PendingProjection(t.Context()); err != nil || !pending {
				t.Fatalf("simulator discovery did not schedule canonical refresh: %t, %v", pending, err)
			}
			t.Logf("%s: %d paginated API requests, %d enrolled repositories, %d evictions",
				action, api.Stats().Requests, len(repositories), erase)
		})
	}
}

func TestRepositoryLifecycleCanonicalEviction(t *testing.T) {
	for _, action := range []string{"archived", "deleted", "renamed"} {
		t.Run(action, func(t *testing.T) {
			w := refreshWorker(t)
			ctx := t.Context()
			w.Projector.Data = integrationPostgres(t, ctx)
			w.Projector.DatabaseQueriesPath = databaseQueries
			seedErasureProjection(t, w.Projector)
			if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/withdrawn", "octo/kept"}); err != nil {
				t.Fatal(err)
			}
			// Retained inventory is a legitimate fallback when GitHub is down.
			// It must not reintroduce the removed coordinate on replay.
			if err := WriteFileAtomic(w.Runner.Lake.InventoryPath(), []byte(`{
				"repositories":{"rows":[
					{"organization":"octo","repository":"withdrawn"},
					{"organization":"octo","repository":"kept"}]},
				"workflows":{"rows":[
					{"organization":"octo","repository":"withdrawn","workflow":".github/workflows/test.lock.yml"}]}
			}`)); err != nil {
				t.Fatal(err)
			}
			w.ScopeRefresher = ScopeRefresher{Client: &refreshEnumerator{
				pages: map[int][]githubapp.Repository{1: {{FullName: "octo/kept"}}},
			}, Quota: &fakeRunQuota{}, QuotaApp: "test"}
			body := []byte(fmt.Sprintf(`{"action":%q,"repository":{"id":42,"full_name":"octo/withdrawn"},"installation":{"id":7}}`, action))
			admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
			if _, err := admitter.AdmitDelivery(ctx, "repository", body, action, time.Hour); err != nil {
				t.Fatal(err)
			}
			w.process(ctx, refreshLease(t, w.Queue))
			erasure := refreshLease(t, w.Queue)
			if !erasure.Task.Erase || !w.process(ctx, erasure) {
				t.Fatalf("repository eviction was not applied: %+v", erasure)
			}
			writeErasureManifest(t, w.Runner.Lake)
			result, err := w.Projector.Project(ctx)
			if err != nil {
				t.Fatal(err)
			}
			results, _, err := w.Projector.Data.ExecuteSQLPlan(ctx,
				[]query.Definition{{Name: "repositories", From: "$repositories"}}, []string{"repositories"})
			if err != nil {
				t.Fatal(err)
			}
			if len(results["repositories"].Rows) == 0 {
				t.Fatal("eviction removed unrelated repository evidence")
			}
			for _, row := range results["repositories"].Rows {
				if row["owner"] != "octo" || row["name"] != "kept" {
					t.Fatalf("evicted repository survived canonical publication: %+v", row)
				}
			}
			if result.Counts["$workflows"] != 0 {
				t.Fatalf("evicted workflow remains queryable: %+v", result.Counts)
			}
		})
	}
}
