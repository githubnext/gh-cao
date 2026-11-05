package collectnative

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

type testTokens struct{}

func (testTokens) InstallationToken(_ context.Context, installation int64) (string, error) {
	if installation != 7 {
		return "", ErrIdentity
	}
	return "synthetic-installation-token", nil
}

func testQuota(t *testing.T) *githubquota.Service {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	quota, err := githubquota.New(store, githubquota.Options{SafetyReserve: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "native-test", Installation: 7, Resource: "core"},
		githubquota.Observation{Limit: 5000, Remaining: 4999, ResetAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return quota
}

func zipArtifact(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func nativeRun() Run {
	return Run{Identity: testIdentity(), WorkflowPath: ".github/workflows/test.lock.yml",
		Fields: map[string]any{"created_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T11:00:00Z", "status": "completed"}}
}

func fixtureFiles(t *testing.T) map[string][]byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("../../../dashboard/site/test/fixtures", "unified-telemetry-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		"aw_info.json":     []byte(`{"repository":"octo/api","run_id":303,"run_attempt":"1","context":{"episode_id":"recorded","hop_id":"hop","root_repo":"elsewhere/root","unknown":"PRIVATE_CONTEXT"}}`),
		"aw_session.jsonl": content,
	}
}

func TestGitHubNativePipelineWithoutExecutables(t *testing.T) {
	archive := zipArtifact(t, fixtureFiles(t))
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-installation-token" {
			t.Error("API request did not use the exact installation credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4900")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		switch r.URL.Path {
		case "/repos/octo/api/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[{"id":1,"path":".github/workflows/test.lock.yml"},{"id":2,"path":".github/workflows/ordinary.yml"}]}`))
		case "/repos/octo/api/actions/workflows/1/runs":
			if r.URL.Query().Get("created") != ">=2026-10-01T00:00:00Z" {
				t.Error("native acquisition lost its authoritative date bound")
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":303,"run_attempt":1,"path":".github/workflows/test.lock.yml","repository":{"full_name":"octo/api"},"status":"completed"}]}`))
		case "/repos/octo/api/actions/runs/303/artifacts":
			_, _ = w.Write([]byte(`{"artifacts":[{"id":100,"name":"usage","expired":false,"workflow_run":{"id":303}}]}`))
		case "/repos/octo/api/actions/artifacts/100/zip":
			_, _ = w.Write(archive)
		case "/repos/octo/api/actions/runs/303/attempts/1/jobs":
			_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
		default:
			t.Errorf("unexpected acquisition endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	store := &testEvidenceStore{}
	collector := Collector{Provider: provider, Store: store}
	t.Setenv("PATH", t.TempDir())
	since, _ := time.Parse(time.DateOnly, "2026-10-01")
	if err := collector.Collect(t.Context(), "octo/api", 7, since); err != nil {
		t.Fatal(err)
	}
	if len(store.published) != 1 || calls.Load() != 5 {
		t.Fatalf("native runtime did not execute all three stages: publications=%d calls=%d", len(store.published), calls.Load())
	}
	evidence := store.published[0]
	if object(evidence.Run["cao_unified_session"])["run"] == nil {
		t.Fatal("safe session projection was not handed off")
	}
	if len(evidence.Contexts) != 1 || evidence.Contexts[0].RootRepo != "elsewhere/root" {
		t.Fatal("recorded caller/root context was discarded or used as acquisition scope")
	}
	content, _, err := evidence.Bytes()
	if err != nil || bytes.Contains(content, []byte("PRIVATE_")) {
		t.Fatalf("raw content escaped safe projection: %v", err)
	}
	if slices.Contains(evidence.Gaps, "legacy-audit-unavailable") ||
		!slices.Contains(evidence.Gaps, "job-logs-unavailable") {
		t.Fatal("default audit retained a blanket gap or lost source-specific coverage")
	}
}

type testEvidenceStore struct {
	published []Evidence
	released  int
}

func (*testEvidenceStore) Claim(_ context.Context, identity Identity, _ time.Duration) (Claim, error) {
	return Claim{Identity: identity, Token: "test", Epoch: 1, Generation: 1}, nil
}
func (s *testEvidenceStore) Publish(_ context.Context, claim Claim, evidence Evidence) error {
	if claim.Identity != evidence.Identity {
		return ErrIdentity
	}
	s.published = append(s.published, evidence)
	return nil
}
func (s *testEvidenceStore) Release(context.Context, Claim) error { s.released++; return nil }

type staticProvider struct{ artifacts []Artifact }

func (p staticProvider) Runs(ctx context.Context, _ string, _ int64, _ time.Time, visit func(Run) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(nativeRun())
}
func (p staticProvider) Artifacts(_ context.Context, _ Run, visit func(Artifact) error) error {
	for _, artifact := range p.artifacts {
		if err := visit(artifact); err != nil {
			return err
		}
	}
	return nil
}

func TestNativeArtifactIdentityAndAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name string
		info string
		gap  string
	}{
		{"missing", "", "artifact-identity-unavailable"},
		{"repository", `{"repository":"other/repo","run_id":303,"run_attempt":1}`, "artifact-identity-mismatch"},
		{"run", `{"repository":"octo/api","run_id":404,"run_attempt":1}`, "artifact-identity-mismatch"},
		{"attempt", `{"repository":"octo/api","run_id":303,"run_attempt":2}`, "artifact-other-attempt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fixtureFiles(t)
			if test.info == "" {
				delete(files, "aw_info.json")
			} else {
				files["aw_info.json"] = []byte(test.info)
			}
			collector := Collector{Provider: staticProvider{[]Artifact{{ID: 1, Name: "usage", Files: files}}}, Store: &testEvidenceStore{}}
			evidence, err := collector.extract(t.Context(), nativeRun())
			if err != nil || !slices.Contains(evidence.Gaps, test.gap) || evidence.Run["cao_unified_session"] != nil {
				t.Fatalf("mismatched evidence became a session: %v %v", evidence, err)
			}
		})
	}
	collector := Collector{Provider: staticProvider{[]Artifact{
		{ID: 1, Name: "usage", Files: fixtureFiles(t)}, {ID: 2, Name: "agent-usage", Files: fixtureFiles(t)},
	}}, Store: &testEvidenceStore{}}
	evidence, err := collector.extract(t.Context(), nativeRun())
	if err != nil || !slices.Contains(evidence.Gaps, "ambiguous-session") || evidence.Run["cao_unified_session"] != nil {
		t.Fatalf("ambiguous artifacts selected by arrival order: %v %v", evidence, err)
	}
}

func TestNativeSupplementArtifactOracle(t *testing.T) {
	files := map[string][]byte{
		"usage/aw_info.json":                []byte(`{"repository":"octo/api","run_id":303,"run_attempt":1}`),
		"usage/experiment/assignments.json": []byte(`{"prompt":"candidate"}`),
		"graders/grader_results.json":       []byte(`{"version":2,"run":{"id":"303","attempt":1},"results":[{"id":"quality","value":null,"source":"inline","message":"PRIVATE_RESULT"}]}`),
		"graders/grader_manifest.json":      []byte(`{"version":1,"graders":[{"id":"quality","name":"Quality","direction":"higher_is_better"}]}`),
		"evals/evals.jsonl":                 []byte("{\"id\":\"quality\",\"answer\":\" yes \",\"runId\":\"303\"}\n"),
	}
	run := map[string]any{"updated_at": "2026-10-15T11:00:00Z"}
	if err := supplementArtifacts(run, nil, files, testIdentity()); err != nil {
		t.Fatal(err)
	}
	if object(run["experiments"])["assignments"] == nil {
		t.Fatal("explicit assignments unavailable")
	}
	projected := object(run["graders"])["results"].([]map[string]any)[0]
	if projected["value"] != nil || projected["name"] != "Quality" || projected["sessionPayloadRef"] != "graders/grader_results.json" {
		t.Fatalf("grader null/manifest/source identity lost: %v", projected)
	}
	expected := oracle(t, `
import { parseGraderArtifacts } from './dashboard/site/src/data/adapters/grader-evidence.js';
let content=''; for await (const chunk of process.stdin) content+=chunk;
console.log(JSON.stringify(parseGraderArtifacts(JSON.parse(content),{runId:'303',attempt:1})));
`, mustJSON(t, []map[string]string{
		{"path": "graders/grader_results.json", "content": string(files["graders/grader_results.json"])},
		{"path": "graders/grader_manifest.json", "content": string(files["graders/grader_manifest.json"])},
	}))
	var want any
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, comparable(t, run["graders"])), mustJSON(t, want)) {
		t.Fatalf("grader artifact differs from Activity: %s %s", mustJSON(t, run["graders"]), expected)
	}
}

func mustJSON(t *testing.T, input any) []byte {
	t.Helper()
	content, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestNativeArchiveBoundsAndTraversal(t *testing.T) {
	for _, name := range []string{"../aw_session.jsonl", "/aw_session.jsonl", `usage\aw_session.jsonl`} {
		if _, _, err := unpack(t.Context(), zipArtifact(t, map[string][]byte{name: []byte("private")}), Limits{}.defaults()); err == nil {
			t.Fatalf("accepted unsafe archive path %q", name)
		}
	}
	archive := zipArtifact(t, map[string][]byte{"ignored.txt": bytes.Repeat([]byte("x"), 1024)})
	if _, _, err := unpack(t.Context(), archive, Limits{Bytes: 32}.defaults()); !errors.Is(err, ErrCapacity) {
		t.Fatalf("ignored ZIP member bypassed decompression budget: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := unpack(ctx, archive, Limits{}.defaults()); err == nil {
		t.Fatal("archive extraction ignored cancellation")
	}
}

func TestGitHubQuotaAndRedirectFailuresAreClosed(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	if err := provider.Runs(t.Context(), "octo/api", 7, time.Time{}, func(Run) error { return nil }); err == nil {
		t.Fatal("secondary quota failure became successful acquisition")
	}
	if err := provider.Runs(t.Context(), "octo/api", 7, time.Time{}, func(Run) error { return nil }); !errors.Is(err, githubquota.ErrParked) {
		t.Fatalf("installation was not parked: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("parked installation issued another API call")
	}
	provider.BaseURL = "http://example.test/"
	if _, err := provider.base(); err == nil {
		t.Fatal("insecure remote API endpoint accepted")
	}
	if strings.Contains(ErrIdentity.Error(), "synthetic-installation-token") {
		t.Fatal("credential leaked in error")
	}
}
