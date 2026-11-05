package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
)

func jobFixture(t *testing.T, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func jobFields() map[string]any {
	var fields map[string]any
	_ = decode([]byte(`{"id":91,"run_id":303,"run_attempt":1,"name":"agent","status":"completed","conclusion":"failure","started_at":"2026-10-15T10:00:00Z","completed_at":"2026-10-15T10:01:00Z"}`), &fields)
	return fields
}

type jobProvider struct {
	staticProvider
	jobs []Job
	err  error
}

func (p jobProvider) Jobs(_ context.Context, _ Run, visit func(Job) error) error {
	for _, job := range p.jobs {
		if err := visit(job); err != nil {
			return err
		}
	}
	return p.err
}

func TestNativeJobAcquisitionSignedLogsAndDestinationOrdering(t *testing.T) {
	raw := jobFixture(t, "actions-job.log")
	files := fixtureFiles(t)
	files["activity/summary.json"] = jobFixture(t, "usage-activity-summary.json")
	files["token-usage.jsonl"] = jobFixture(t, "token-usage.jsonl")
	archive := zipArtifact(t, files)
	var captured, downloaded atomic.Bool
	objects := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("signed logs received credentials")
		}
		if !captured.Load() {
			t.Error("job normalization preceded durable session admission")
		}
		downloaded.Store(true)
		_, _ = w.Write(raw)
	}))
	defer objects.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-installation-token" {
			t.Error("missing scoped API credential")
		}
		switch r.URL.Path {
		case "/repos/octo/api/actions/runs/303/artifacts":
			_, _ = w.Write([]byte(`{"artifacts":[{"id":1,"name":"usage","workflow_run":{"id":303}}]}`))
		case "/repos/octo/api/actions/artifacts/1/zip":
			_, _ = w.Write(archive)
		case "/repos/octo/api/actions/runs/303/attempts/1/jobs":
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "100" {
				t.Error("unbounded job listing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []any{jobFields()}})
		case "/repos/octo/api/actions/jobs/91/logs":
			w.Header().Set("Location", objects.URL+"/signed?secret=PRIVATE_SIGNED_URL")
			w.WriteHeader(http.StatusFound)
		default:
			t.Errorf("unscoped endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	provider := GitHub{BaseURL: api.URL, HTTP: objects.Client(), Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	store := &testEvidenceStore{}
	collector := Collector{Provider: provider, Store: store, Destinations: []Destination{DestinationFunc(func(_ context.Context, run Run, content []byte, gap string) error {
		if run.Identity != testIdentity() || gap != "" || !bytes.Equal(content, files["aw_session.jsonl"]) {
			t.Error("destination did not receive exact original session")
		}
		captured.Store(true)
		return nil
	})}}
	t.Setenv("PATH", "/nonexistent")
	// All fixtures are already in memory; this directory has no Activity sources,
	// cache, .git entry or executables for the runtime to consult.
	t.Chdir("testdata")
	if err := collector.collectRun(t.Context(), nativeRun()); err != nil {
		t.Fatal(err)
	}
	if !downloaded.Load() || len(store.published) != 1 {
		t.Fatal("job-log acquisition was not integrated into publication")
	}
	evidence := store.published[0]
	encoded, _, err := evidence.Bytes()
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE")) {
		t.Fatalf("raw job/artifact evidence escaped: %v", err)
	}
	details := array(evidence.Run["job_details"])
	if len(details) != 1 || scalar(object(details[0])["id"]) != "91" ||
		scalar(object(details[0])["run_attempt"]) != "1" || object(details[0])["started_at"] != jobFields()["started_at"] {
		t.Fatal("cached JSONL job metadata contract was not retained")
	}
	for _, field := range []string{"Duration", "run_id", "steps"} {
		if _, present := object(details[0])[field]; present {
			t.Fatalf("internal-only job field %s escaped cached JSONL projection", field)
		}
	}
	if len(array(evidence.Run["actions_annotations"])) != 2 {
		t.Fatal("runner diagnostic classes were not retained")
	}
	usage := object(evidence.Run["token_usage_summary"])
	if scalar(usage["total_input_tokens"]) != "30" || scalar(usage["total_aic"]) != "0.9" {
		t.Fatal("runner stdout was counted as usage or deduplication/cumulative AIC was lost")
	}
	if !slices.Contains(evidence.Gaps, "legacy-audit-unavailable") {
		t.Fatal("bounded subset falsely claimed complete legacy audit parity")
	}
}

func TestGitHubJobsRejectMixupsIncompleteAndCapacity(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{"run", `{"total_count":1,"jobs":[{"id":91,"run_id":304,"run_attempt":1}]}`, ErrIdentity},
		{"attempt", `{"total_count":1,"jobs":[{"id":91,"run_id":303,"run_attempt":2}]}`, ErrIdentity},
		{"missing-attempt", `{"total_count":1,"jobs":[{"id":91,"run_id":303}]}`, ErrIdentity},
		{"duplicate", `{"total_count":2,"jobs":[{"id":91,"run_id":303,"run_attempt":1},{"id":91,"run_id":303,"run_attempt":1}]}`, ErrIdentity},
		{"truncated", `{"total_count":2,"jobs":[{"id":91,"run_id":303,"run_attempt":1}]}`, nil},
		{"missing-jobs", `{"total_count":0}`, nil},
		{"missing-count", `{"jobs":[]}`, nil},
		{"capacity", `{"total_count":999999,"jobs":[]}`, ErrCapacity},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logRequests int
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/logs") {
					logRequests++
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer api.Close()
			provider := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
			err := provider.Jobs(t.Context(), nativeRun(), func(Job) error { return nil })
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("invalid listing accepted: %v", err)
			}
			if logRequests != 0 {
				t.Fatal("unverified page caused scoped log retrieval")
			}
		})
	}
}

func TestNativeJobFailuresRemainRetryable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/artifacts"):
					_, _ = w.Write([]byte(`{"artifacts":[]}`))
				case strings.HasSuffix(r.URL.Path, "/jobs"):
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []any{jobFields()}})
				default:
					w.WriteHeader(status)
				}
			}))
			defer api.Close()
			store := &testEvidenceStore{}
			c := Collector{Provider: GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}, Store: store}
			if err := c.collectRun(t.Context(), nativeRun()); err == nil || len(store.published) != 0 || store.released != 1 {
				t.Fatal("failed acquisition was published or claim was not released")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []any{jobFields()}})
			return
		}
		cancel()
		<-r.Context().Done()
	}))
	defer api.Close()
	provider := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	if err := provider.Jobs(ctx, nativeRun(), func(Job) error { t.Error("canceled job delivered"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestNativeJobQuotaAndRedirectFailures(t *testing.T) {
	var requests atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []any{jobFields()}})
			return
		}
		w.Header().Set("Location", "https://user:PRIVATE@objects.example/log")
		w.WriteHeader(http.StatusFound)
	}))
	defer api.Close()
	quota := testQuota(t)
	provider := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: quota, App: "native-test"}
	if err := provider.Jobs(t.Context(), nativeRun(), func(Job) error { return nil }); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("unsafe signed redirect accepted or exposed")
	}
	requests.Store(0)
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "native-test", Installation: 7, Resource: "core"},
		githubquota.Observation{Limit: 5000, Remaining: 0, ResetAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Jobs(t.Context(), nativeRun(), func(Job) error { return nil }); err == nil || requests.Load() != 0 {
		t.Fatal("exhausted shared quota made an API request")
	}
}

func TestNativeJobGapsAndLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		log  []byte
		gap  string
	}{
		{"absent", nil, "job-log-unavailable"},
		{"malformed", []byte{0, 0xff}, "invalid-job-log"},
		{"unsupported", []byte("PRIVATE_UNTIMED_STDOUT"), "unsupported-job-log"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := jobProvider{jobs: []Job{{Identity: testIdentity(), ID: 91, Fields: jobFields(), Log: test.log}}}
			fields := map[string]any{}
			used := 0
			gaps, err := acquireJobs(t.Context(), p, nativeRun(), fields, Limits{}, &used)
			if err != nil || !slices.Contains(gaps, test.gap) {
				t.Fatalf("invalid log became complete: %v %v", gaps, err)
			}
			content, _ := json.Marshal(fields)
			if bytes.Contains(content, []byte("PRIVATE")) {
				t.Fatal("unsupported stdout persisted")
			}
		})
	}
	p := jobProvider{jobs: []Job{{Identity: testIdentity(), ID: 91, Fields: jobFields(), Log: jobFixture(t, "actions-job.log")}}}
	for _, limits := range []Limits{{Bytes: 4}, {LineBytes: 4}, {Entries: 1}} {
		used := 0
		if _, err := acquireJobs(t.Context(), p, nativeRun(), map[string]any{}, limits, &used); !errors.Is(err, ErrCapacity) {
			t.Fatalf("resource bound ignored: %+v %v", limits, err)
		}
	}
	used := 0
	p.jobs[0].Attempt = 2
	if _, err := acquireJobs(t.Context(), p, nativeRun(), map[string]any{}, Limits{}, &used); !errors.Is(err, ErrIdentity) {
		t.Fatal("provider job callback escaped exact-attempt contract")
	}
}

func TestGitHubJobMissingLogsQuotaObservationAndResponseLimit(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4200")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(2*time.Hour).Unix()))
				if strings.HasSuffix(r.URL.Path, "/jobs") {
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []any{jobFields()}})
					return
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
				}
			}))
			defer api.Close()
			quota := testQuota(t)
			p := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: quota, App: "native-test", Limits: Limits{Bytes: 512}}
			visits := 0
			err := p.Jobs(t.Context(), nativeRun(), func(job Job) error {
				visits++
				if job.Gap != "job-log-unavailable" || len(job.Log) != 0 {
					t.Fatal("missing log did not preserve a source gap")
				}
				return nil
			})
			if status == http.StatusOK {
				if !errors.Is(err, ErrCapacity) || visits != 0 {
					t.Fatal("oversized HTTP log was accepted")
				}
			} else if err != nil || visits != 1 {
				t.Fatalf("missing logs did not yield scoped metadata: %v", err)
			}
			state, err := quota.State(t.Context(), githubquota.BucketID{App: "native-test", Installation: 7, Resource: "core"})
			if err != nil || state.Remaining != 4200 {
				t.Fatalf("job/log responses did not update shared quota: %+v %v", state, err)
			}
		})
	}
}

func TestGitHubJobsPaginationBoundsAndDuplicatePages(t *testing.T) {
	for _, pages := range []int{1, 2} {
		t.Run(fmt.Sprint(pages), func(t *testing.T) {
			requests := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				jobs := []any{}
				for id := 1; id <= 100; id++ {
					jobs = append(jobs, map[string]any{"id": id, "run_id": 303, "run_attempt": 1, "status": "queued"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 101, "jobs": jobs})
			}))
			defer api.Close()
			p := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test", Limits: Limits{Pages: pages}}
			err := p.Jobs(t.Context(), nativeRun(), func(Job) error { return nil })
			want := ErrCapacity
			if pages == 2 {
				want = ErrIdentity
			}
			if !errors.Is(err, want) || requests != pages {
				t.Fatalf("paged source did not fail closed: %v requests=%d", err, requests)
			}
		})
	}
}

func TestNativeDestinationFailurePreventsJobAcquisitionAndPublication(t *testing.T) {
	source := &orderedJobProvider{staticProvider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: fixtureFiles(t)}}}}
	store := &testEvidenceStore{}
	c := Collector{Provider: source, Store: store, Destinations: []Destination{DestinationFunc(func(context.Context, Run, []byte, string) error {
		return ErrBusy
	})}}
	if err := c.collectRun(t.Context(), nativeRun()); !errors.Is(err, ErrBusy) ||
		source.called || len(store.published) != 0 || store.released != 1 {
		t.Fatal("failed durable admission allowed normalization or publication")
	}
	c.Destinations = nil
	if err := c.collectRun(t.Context(), nativeRun()); err != nil || !source.called || len(store.published) != 1 {
		t.Fatalf("destination failure spent retry eligibility: %v", err)
	}
}

type orderedJobProvider struct {
	staticProvider
	called bool
}

func (p *orderedJobProvider) Jobs(context.Context, Run, func(Job) error) error {
	p.called = true
	return nil
}

func TestNativeLegacyAuditCanonicalActivityOracle(t *testing.T) {
	for _, unified := range []bool{false, true} {
		t.Run(fmt.Sprint(unified), func(t *testing.T) {
			files := map[string][]byte{"usage/activity/summary.json": jobFixture(t, "usage-activity-summary.json"),
				"usage/token-usage.jsonl": jobFixture(t, "token-usage.jsonl"),
				"usage/agent_output.json": []byte(`{"items":[
					{"type":"report_missing_tool","tool":"github.search","timestamp":"2026-10-15T10:00:00Z","reason":"PRIVATE_REASON"},
					{"type":"missing_data","data_type":"repository.metrics","context":"PRIVATE_CONTEXT"}]}`),
				"usage/github_rate_limits.jsonl": []byte(
					"{\"source\":\"rate_limit_api\",\"used\":900}\n" +
						"{\"source\":\"response_headers\",\"operation\":\"PRIVATE_OPERATION\",\"resource\":\"core\",\"used\":901}\n")}
			fields := map[string]any{"repository": "octo/api", "organization": "octo", "run_id": 303, "run_attempt": 1,
				"workflow_name": "Test", "workflow_path": ".github/workflows/test.lock.yml",
				"status": "completed", "conclusion": "timed_out", "created_at": "2026-10-15T10:00:00Z",
				"started_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T10:01:01Z"}
			gaps, err := enrichAudit(t.Context(), fields, files, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 {
				t.Fatalf("valid summary not acquired: %v %v", gaps, err)
			}
			if unified {
				session, _, err := ProjectSession(t.Context(), readSessionFixture(t, "unified-telemetry-session.jsonl"),
					"usage/aw_session.jsonl", testIdentity(), Limits{})
				if err != nil {
					t.Fatal(err)
				}
				fields["cao_unified_session"] = session
			}
			evidence := Evidence{Version: 1, Identity: testIdentity(), Run: fields}
			input, _, err := evidence.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(input, []byte("PRIVATE")) {
				t.Fatal("summary was copied rather than safely projected")
			}
			expected := oracle(t, `
import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from './dashboard/site/src/data/normalize/index.js';
let content=''; for await (const chunk of process.stdin) content+=chunk;
const e=JSON.parse(content), batch=normalize(adaptCachedGhAwJsonl(JSON.stringify({schema_version:5,kind:'run',run:e.run})).observations);
for(const run of batch.runs) for(const key of Object.keys(run)) if(key.startsWith('routing')) delete run[key];
for(const [name,rows] of Object.entries(batch)) {
 if(!rows.length) delete batch[name];
 for(const row of rows) delete row.sequence;
 rows.sort((a,b)=>a.id<b.id?-1:a.id>b.id?1:0);
}
console.log(JSON.stringify(batch));`, input)
			var want map[string]any
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			got := comparable(t, projection.Collections).(map[string]any)
			for collection, rows := range want {
				if !reflect.DeepEqual(got[collection], rows) {
					reportProjectionDifference(t, collection, got[collection], rows)
				}
			}
			for collection := range got {
				if want[collection] == nil {
					t.Errorf("unexpected collection %s", collection)
				}
			}
		})
	}
}
