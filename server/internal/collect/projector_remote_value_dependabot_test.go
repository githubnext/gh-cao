package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestDependabotValuePacketsReachProductionPostgresQuery(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	writer, err := data.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	path := ".github/workflows/" + dependabotValueWorkflow + ".md"
	repositoryID := "repository:" + model.EncodeCoordinate(dependabotValueRepository)
	workflowID := "workflow:" + model.EncodeCoordinate(dependabotValueRepository+":"+path)
	if err := writer.Append(t.Context(), "$repositories", model.Row{
		"id": repositoryID, "owner": "github", "name": "gh-aw",
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(t.Context(), "$workflows", model.Row{
		"id": workflowID, "repositoryId": repositoryID, "path": path,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "dependabot-parent"); err != nil {
		t.Fatal(err)
	}
	authority := data.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return authority.Enroll(ctx, 9, []string{dependabotValueRepository})
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := authority.CurrentScope(t.Context(), dependabotValueRepository)
	if err != nil {
		t.Fatal(err)
	}
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/github/gh-aw/dependabot/alerts":
			_, _ = fmt.Fprint(writer, `[]`)
		case "/search/issues":
			_, _ = fmt.Fprint(writer, `{"total_count":0,"incomplete_results":false,"items":[]}`)
		default:
			t.Errorf("unreviewed endpoint: %s", request.URL.Path)
		}
	})
	observed := time.Now().UTC().Truncate(time.Microsecond)
	packets, skipped, err := acquireNativeDependabotValue(t.Context(), api, nil, observed)
	if err != nil || skipped || len(packets) != 3 || *calls != 2 {
		t.Fatalf("complete empty lifecycles were not accepted: packets=%d skipped=%v calls=%d err=%v",
			len(packets), skipped, *calls, err)
	}
	if _, err := data.Acquisition().PublishValueBatch(t.Context(), scope, nil, packets, nil, time.Now()); err != nil {
		t.Fatalf("native Dependabot packets rejected by production canonical writer: %v", err)
	}
	restored, _, err := data.Acquisition().ReadValueBatch(t.Context(), scope, dependabotValueWorkflow)
	if err != nil || len(restored) != 3 {
		t.Fatalf("native Dependabot packet batch not retained: %d %v", len(restored), err)
	}
	route := dependabotValueRepository + ":" + path
	defs := []query.Definition{
		{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
		{Name: "dependabot-cutoff", From: "operational-value-evidence", Filter: &query.Filter{
			Predicates: []query.Predicate{{Field: "workflow-route", Equals: route},
				{Field: "fact-id", In: []any{"contract", "cutoff:" + dependabotValueCutoff(observed).Format(time.RFC3339)}},
			}}},
	}
	sources, _, err := data.ExecuteSQLPlan(t.Context(), defs, []string{"dependabot-cutoff"})
	if err != nil || len(sources["dependabot-cutoff"].Rows) != 9 {
		t.Fatalf("production canonical cutoff query missing required facts: %d %v",
			len(sources["dependabot-cutoff"].Rows), err)
	}
	for _, row := range sources["dependabot-cutoff"].Rows {
		if row["report-id"] == nil || row["window-end"] == nil || row["observed-at"] == nil {
			t.Fatalf("canonical Dependabot fact lost its packet identity or cutoff: %+v", row)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	policy := filepath.Join(workspace, "cao.json")
	content := `{"version":1,"gh-aw-version":"v1.0.1","control-plane":{
		"scope":{"allowed-repositories":["github/gh-aw"]},"activity":{"repositories":["github/gh-aw"]},
		"campaigns":{"optimization":{"enabled":false},
			"dependabot":{"targets":{"github/gh-aw":{"mode":"review"}},
				"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}},
			"activity":{"workflow-values":[{"repository":"github/gh-aw","workflow":"dependabot-update-planner",
				"collector":"dependabot/operational-value/dependabot-update-planner.mjs",
				"api-requests":111,"acquisition":{"requirements":[
					{"source":"github","pool":"core","primitive":"github.dependabot-alert-lifecycle.v1","scope":"github/gh-aw","cache":"invocation"},
					{"source":"github","pool":"search","primitive":"github.dependabot-pull-request-lifecycle.v1","scope":"github/gh-aw","cache":"invocation"}]}}]}}}}`
	if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(workspace, "snapshot")
	if err := os.Mkdir(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := operationalvalue.Config{Root: root, Repositories: []string{dependabotValueRepository},
		ObservedAt: observed}
	if err := operationalvalue.WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(workspace, "values.jsonl")
	if err := operationalvalue.ComputeHostedSnapshot(t.Context(), config, policy, snapshot, output,
		func(ctx context.Context, defs []query.Definition, requested []string) (map[string]model.Source, error) {
			rows, _, err := data.ExecuteSQLPlan(ctx, defs, requested)
			return rows, err
		}); err != nil {
		t.Fatalf("native local evaluation of production Dependabot facts failed: %v", err)
	}
	values, err := operationalvalue.ReadMeasurements(output)
	if err != nil || len(values) != 3 {
		t.Fatalf("complete empty lifecycles did not produce three numeric measurements: %+v %v", values, err)
	}
	for _, row := range values {
		if row["value"] != json.Number("0") || row["coverage-status"] != "complete" ||
			row["evidence-digest"] == nil {
			t.Fatalf("empty complete evidence did not produce an honest zero: %+v", row)
		}
	}
}

func testDependabotAPI(t *testing.T, handler http.HandlerFunc) (*remoteValueAPI, *int) {
	t.Helper()
	backend := volatileBackfill(t).Enrollment.Metadata.(operational.GitHubQuotaStore)
	core, err := githubquota.New(backend, githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	search, err := githubquota.New(backend, githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Unix()
	for _, resource := range []string{"core", "search"} {
		if err := core.Observe(t.Context(), githubquota.BucketID{
			App: "dependabot-test", Installation: 9, Resource: resource,
		}, githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: time.Unix(reset, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	calls := new(int)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		*calls++
		resource := "core"
		if strings.HasPrefix(request.URL.Path, "/search/") {
			resource = "search"
		}
		writer.Header().Set("X-RateLimit-Resource", resource)
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(4000-*calls))
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		handler(writer, request)
	}))
	t.Cleanup(upstream.Close)
	base, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &remoteValueAPI{client: upstream.Client(), quota: core, searchQuota: search,
		app: "dependabot-test", installation: 9, repository: dependabotValueRepository,
		collector: dependabotValueCollector, base: base, token: "test-token", max: 111}, calls
}

func testDependabotEntry() remoteValueEntry {
	return remoteValueEntry{
		Repository: dependabotValueRepository, Workflow: dependabotValueWorkflow,
		Collector: dependabotValueCollector, APIRequests: 111,
		Acquisition: &remoteValueAcquisition{Requirements: append([]remoteValueRequirement(nil), dependabotValueRequirements...)},
	}
}

func TestDependabotValueFrozenManifestAndDispatch(t *testing.T) {
	entry := testDependabotEntry()
	if err := reviewedDependabotValue(entry); err != nil {
		t.Fatal(err)
	}
	actionsEntry := entry
	actionsEntry.Collector = dependabotActionsCollector
	if err := reviewedDependabotValue(actionsEntry); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{
		"d73a2676ebb04ebf34093873dc5eb72b8e54a461c8b5f068b7b3bd100fc2507a",
		"c0bcc97030ca99e4ed5bbe1b89b64ebde02bc7b4659682058e407cf155603ed2",
		"774112c80afca79b272669e30a75dc5b1216191f9345e6e14970863bb617652a",
	} {
		if got, _ := dependabotValueContract(dependabotValueMetrics[index]); got != want {
			t.Fatalf("Dependabot contract digest changed: %s vs %s", got, want)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	code := `import { pathToFileURL } from 'node:url';
const {definition} = await import(pathToFileURL(process.argv[1]));
process.stdout.write(JSON.stringify(definition.acquisition?.requirements));`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, filepath.Join(root, dependabotValueCollector)) // #nosec G204 -- fixed reviewed source.
	content, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read frozen acquisition manifest: %v: %s", err, content)
	}
	var manifest []remoteValueRequirement
	if err := json.Unmarshal(content, &manifest); err != nil || len(manifest) != 2 {
		t.Fatalf("Actions manifest diverged: %s %v", content, err)
	}
	for i := range manifest {
		if manifest[i] != dependabotValueRequirements[i] {
			t.Fatalf("native acquisition differs from frozen JS: %+v", manifest)
		}
	}
	for _, mutate := range []func(*remoteValueEntry){
		func(value *remoteValueEntry) { value.APIRequests = 110 },
		func(value *remoteValueEntry) { value.Repository = "octo/other" },
		func(value *remoteValueEntry) { value.Collector = ".github/value-collectors/unreviewed.mjs" },
		func(value *remoteValueEntry) { value.Acquisition = nil },
		func(value *remoteValueEntry) {
			value.Acquisition = &remoteValueAcquisition{Requirements: []remoteValueRequirement{
				dependabotValueRequirements[0], dependabotValueRequirements[0],
			}}
		},
		func(value *remoteValueEntry) {
			value.Acquisition = &remoteValueAcquisition{Requirements: []remoteValueRequirement{
				dependabotValueRequirements[0], dependabotValueRequirements[1],
			}}
			value.Acquisition.Requirements[1].Scope = "octo/other"
		},
	} {
		invalid := entry
		mutate(&invalid)
		if err := reviewedRemoteValue(invalid); err == nil {
			t.Fatalf("unreviewed registration accepted: %+v", invalid)
		}
	}
	valid, failures := supportedRemoteValueEntries([]remoteValueEntry{{Workflow: dependabotValueWorkflow}, entry})
	if failures == nil || len(valid) != 1 || valid[0].Workflow != dependabotValueWorkflow {
		t.Fatalf("failure suppressed reviewed registration: %+v %v", valid, failures)
	}
}

func TestDependabotActionsPacketsMatchHostedNative(t *testing.T) {
	api, _ := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/search/issues" {
			_, _ = fmt.Fprint(writer, `{"total_count":0,"incomplete_results":false,"items":[]}`)
		} else {
			_, _ = fmt.Fprint(writer, `[]`)
		}
	})
	observed, _ := time.Parse(time.RFC3339, "2026-10-02T00:00:00Z")
	packets, skipped, err := acquireNativeDependabotValue(t.Context(), api, nil, observed)
	if err != nil || skipped {
		t.Fatalf("native acquisition failed: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	code := `import { pathToFileURL } from 'node:url';
const {collectDependabotValue} = await import(pathToFileURL(process.argv[1]));
const {definition,buildEvidence,scoreMetric} = await import(pathToFileURL(process.argv[2]));
const read = endpoint => ({body:endpoint.startsWith("search/") ?
  {total_count:0,incomplete_results:false,items:[]} : [],link:""});
const result=await collectDependabotValue({
  repository:"github/gh-aw",workflow:"dependabot-update-planner",
  observedAt:"2026-10-02T00:00:00Z",previous:[]
},definition,buildEvidence,scoreMetric,read);
process.stdout.write(JSON.stringify(result.map(envelope=>envelope.operational_value_evidence)));`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code,
		filepath.Join(root, dependabotActionsCollector), filepath.Join(root, dependabotValueCollector)) // #nosec G204 -- fixed reviewed local modules.
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Actions collector: %v: %s", err, output)
	}
	var actions []model.Row
	if err := json.Unmarshal(output, &actions); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(packets)
	actual, _ := json.Marshal(actions)
	if !bytes.Equal(actual, expected) {
		t.Fatalf("Actions packets differ from native packets:\n%s\n%s", actual, expected)
	}
}

func TestDependabotValueMatchesFrozenJSOracle(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, dependabotValueCollector)
	code := `import { pathToFileURL } from 'node:url';
const {buildEvidence,scoreMetric,definition} = await import(pathToFileURL(process.argv[1]));
let input = ''; for await (const chunk of process.stdin) input += chunk;
const {alerts,pullRequests,cutoff} = JSON.parse(input);
const evidence = buildEvidence({alerts,pullRequests,repository:"github/gh-aw",request:{windowEnd:cutoff}});
process.stdout.write(JSON.stringify(definition.metrics.map(({id}) => scoreMetric(id,evidence))));`
	type scenario struct {
		name        string
		alerts      []map[string]any
		pulls       []map[string]any
		cutoff      string
		wantMissing bool
	}
	cases := []scenario{
		{name: "complete-empty", alerts: []map[string]any{}, pulls: []map[string]any{}, cutoff: "2026-09-24T08:48:00Z"},
		{name: "incomplete", cutoff: "2026-09-24T08:48:00Z", wantMissing: true},
		{name: "lifecycle", cutoff: "2026-09-24T08:48:00Z",
			alerts: []map[string]any{
				{"created_at": "2026-09-01T00:00:00Z", "fixed_at": "2026-09-25T00:00:00Z", "severity": "critical"},
				{"created_at": "2026-09-01T00:00:00Z", "dismissed_at": "2026-09-24T08:48:00Z", "severity": "medium"},
				{"created_at": "2026-09-20T00:00:00Z", "severity": "high"},
				{"created_at": "2026-09-25T00:00:00Z", "severity": "low"},
			},
			pulls: []map[string]any{
				{"created_at": "2026-09-18T00:00:00Z", "closed_at": "2026-09-25T00:00:00Z"},
				{"created_at": "2026-09-20T00:00:00Z", "closed_at": "2026-09-24T08:48:00Z"},
			}},
	}
	for _, testcase := range cases {
		t.Run(testcase.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"alerts": testcase.alerts, "pullRequests": testcase.pulls, "cutoff": testcase.cutoff})
			command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, module) // #nosec G204 -- fixed checked-in oracle module.
			command.Stdin = bytes.NewReader(input)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("frozen oracle: %v: %s", err, output)
			}
			var expected []*int
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err)
			}
			if testcase.wantMissing {
				for _, score := range expected {
					if score != nil {
						t.Fatalf("incomplete oracle was numeric: %s", output)
					}
				}
				return
			}
			observed, _ := time.Parse(time.RFC3339, "2026-10-01T00:00:00Z")
			cutoff, _ := time.Parse(time.RFC3339, testcase.cutoff)
			alerts, pulls := []dependabotValueEvent{}, []dependabotValueEvent{}
			for i, item := range testcase.alerts {
				item["number"], item["state"] = i+1, "open"
				if item["fixed_at"] != nil {
					item["state"] = "fixed"
				}
				if item["dismissed_at"] != nil {
					item["state"] = "dismissed"
				}
				item["security_advisory"] = map[string]any{"severity": item["severity"]}
				content, _ := json.Marshal(item)
				var record dependabotValueLifecycle
				if err := json.Unmarshal(content, &record); err != nil {
					t.Fatal(err)
				}
				event, err := dependabotValueRecord(record, observed, false)
				if err != nil {
					t.Fatal(err)
				}
				alerts = append(alerts, event)
			}
			for i, item := range testcase.pulls {
				item["number"], item["state"] = i+1, "open"
				if item["closed_at"] != nil {
					item["state"] = "closed"
				}
				item["user"] = map[string]any{"login": "dependabot[bot]"}
				item["pull_request"] = map[string]any{"url": "https://api.github.com/repos/github/gh-aw/pulls/1"}
				content, _ := json.Marshal(item)
				var record dependabotValueLifecycle
				if err := json.Unmarshal(content, &record); err != nil {
					t.Fatal(err)
				}
				event, err := dependabotValueRecord(record, observed, true)
				if err != nil {
					t.Fatal(err)
				}
				pulls = append(pulls, event)
			}
			counts := dependabotValueCounts(alerts, pulls, cutoff)
			for i, score := range expected {
				if score == nil || counts[i] != *score {
					t.Fatalf("frozen oracle score %d = %v; Go = %d", i, score, counts[i])
				}
			}
		})
	}
}

func TestDependabotValuePaginationAndMalformedEvidence(t *testing.T) {
	cutoff, _ := time.Parse(time.RFC3339, "2026-09-24T08:48:00Z")
	observed := cutoff.Add(time.Hour)
	alert := `{"number":1,"state":"open","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}`
	pull := `{"number":1,"state":"open","created_at":"2026-09-20T00:00:00Z","user":{"login":"dependabot[bot]"},"pull_request":{"url":"https://api.github.com/pulls/1"}}`
	for _, testcase := range []struct {
		name, alerts, pulls string
		wantErr             bool
	}{
		{"complete-empty", `[]`, `{"total_count":0,"incomplete_results":false,"items":[]}`, false},
		{"complete", `[` + alert + `]`, `{"total_count":1,"incomplete_results":false,"items":[` + pull + `]}`, false},
		{"alert-object", `{}`, `{"total_count":0,"incomplete_results":false,"items":[]}`, true},
		{"alert-future", `[{"number":1,"state":"open","created_at":"2027-01-01T00:00:00Z","security_advisory":{"severity":"high"}}]`, `{"total_count":0,"incomplete_results":false,"items":[]}`, true},
		{"alert-terminal-missing", `[{"number":1,"state":"fixed","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}]`, `{"total_count":0,"incomplete_results":false,"items":[]}`, true},
		{"search-truncated", `[]`, `{"total_count":1,"incomplete_results":false,"items":[]}`, true},
		{"search-incomplete", `[]`, `{"total_count":0,"incomplete_results":true,"items":[]}`, true},
		{"search-overflow", `[]`, `{"total_count":1001,"incomplete_results":false,"items":[]}`, true},
		{"incorrect-author", `[]`, `{"total_count":1,"incomplete_results":false,"items":[` + strings.Replace(pull, "dependabot[bot]", "someone", 1) + `]}`, true},
		{"not-a-pr", `[]`, `{"total_count":1,"incomplete_results":false,"items":[` + strings.Replace(pull, `"pull_request":{"url":"https://api.github.com/pulls/1"}`, `"pull_request":null`, 1) + `]}`, true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/repos/github/gh-aw/dependabot/alerts":
					_, _ = fmt.Fprint(writer, testcase.alerts)
				case "/search/issues":
					if request.URL.Query().Get("q") !=
						"repo:github/gh-aw is:pr author:app/dependabot created:<=2026-09-24T08:48:00Z" {
						t.Error("search escaped frozen repository or cutoff")
					}
					_, _ = fmt.Fprint(writer, testcase.pulls)
				default:
					t.Errorf("unreviewed request: %s", request.URL)
				}
			})
			_, _, err := acquireNativeDependabotValue(t.Context(), api, nil, observed)
			if (err != nil) != testcase.wantErr || *calls > 2 {
				t.Fatalf("acquisition error=%v, calls=%d", err, *calls)
			}
		})
	}
}

func TestDependabotValueSearchPreflightUsesReviewedBudget(t *testing.T) {
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/github/gh-aw/dependabot/alerts":
			_, _ = fmt.Fprint(writer, `[]`)
		case "/rate_limit":
			_, _ = fmt.Fprintf(writer, `{"resources":{"search":{"limit":5000,"remaining":4000,"reset":%d}}}`,
				time.Now().Add(time.Hour).Unix())
		case "/search/issues":
			_, _ = fmt.Fprint(writer, `{"total_count":0,"incomplete_results":false,"items":[]}`)
		default:
			t.Errorf("unreviewed preflight endpoint: %s", request.URL.Path)
		}
	})
	fresh, err := githubquota.New(volatileBackfill(t).Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	api.searchQuota = fresh
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	if packets, skipped, err := acquireNativeDependabotValue(t.Context(), api, nil, observed); err != nil || skipped || len(packets) != 3 || *calls != 3 {
		t.Fatalf("Search-quota preflight was omitted from the reviewed API budget: packets=%d skipped=%v calls=%d err=%v",
			len(packets), skipped, *calls, err)
	}
}

func TestDependabotValueAlertCursorPagination(t *testing.T) {
	alerts := make([]string, 100)
	for i := range alerts {
		alerts[i] = fmt.Sprintf(`{"number":%d,"state":"open","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}`, i+1)
	}
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("after") == "" {
			writer.Header().Set("Link", fmt.Sprintf(`<http://%s%s?state=auto_dismissed,dismissed,fixed,open&per_page=100&after=cursor-two>; rel="next"`,
				request.Host, request.URL.Path))
			_, _ = fmt.Fprintf(writer, "[%s]", strings.Join(alerts, ","))
		} else {
			if request.URL.Query().Get("after") != "cursor-two" {
				t.Errorf("unexpected Dependabot pagination cursor: %s", request.URL)
			}
			_, _ = fmt.Fprint(writer, `[{"number":101,"state":"open","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}]`)
		}
	})
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	if events, err := dependabotValueReadAlerts(t.Context(), api, observed); err != nil || len(events) != 101 || *calls != 2 {
		t.Fatalf("cursor-paginated alerts were not complete: events=%d calls=%d error=%v", len(events), *calls, err)
	}
}

func TestDependabotValueRejectsUnreviewedAlertCursors(t *testing.T) {
	api, _ := testDependabotAPI(t, func(http.ResponseWriter, *http.Request) {})
	endpoint := "repos/github/gh-aw/dependabot/alerts?state=auto_dismissed,dismissed,fixed,open&per_page=100"
	valid := api.base.String() + "/" + endpoint + "&after=cursor-two"
	for _, testcase := range []struct {
		name, link string
		valid      bool
	}{
		{"next", "<" + valid + `>; rel="next"`, true},
		{"numeric alias stays on named endpoint", "<" + strings.Replace(valid, "repos/github/gh-aw", "repositories/1036865607", 1) + `>; rel="next"`, true},
		{"invalid numeric alias", "<" + strings.Replace(valid, "repos/github/gh-aw", "repositories/not-an-id", 1) + `>; rel="next"`, false},
		{"previous and next", "<" + valid + `>; rel="prev", <` + valid + `>; rel="next"`, true},
		{"other host", `<https://example.com/` + endpoint + `&after=cursor-two>; rel="next"`, false},
		{"other repository", "<" + api.base.String() + `/repos/octo/other/dependabot/alerts?state=auto_dismissed,dismissed,fixed,open&per_page=100&after=cursor-two>; rel="next"`, false},
		{"changed state", "<" + api.base.String() + `/repos/github/gh-aw/dependabot/alerts?state=open&per_page=100&after=cursor-two>; rel="next"`, false},
		{"no cursor", "<" + api.base.String() + "/" + endpoint + `>; rel="next"`, false},
		{"duplicate next", "<" + valid + `>; rel="next", <` + valid + `>; rel="next"`, false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			headers := http.Header{"Link": []string{testcase.link}}
			next, err := dependabotValueNext(headers, api.base, endpoint)
			if (err == nil) != testcase.valid || testcase.valid && next != endpoint+"&after=cursor-two" {
				t.Fatalf("cursor next=%q error=%v", next, err)
			}
		})
	}
}

func TestDependabotValueFullAlertPageWithoutCursorIsIncomplete(t *testing.T) {
	alerts := make([]string, 100)
	for i := range alerts {
		alerts[i] = fmt.Sprintf(`{"number":%d,"state":"open","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}`, i+1)
	}
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(writer, "[%s]", strings.Join(alerts, ","))
	})
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	if _, err := dependabotValueReadAlerts(t.Context(), api, observed); err == nil || *calls != 1 {
		t.Fatalf("full alert page without cursor was treated as complete: calls=%d error=%v", *calls, err)
	}
}

func TestDependabotValueAlertCapDoesNotTruncate(t *testing.T) {
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		page := 1
		if after := request.URL.Query().Get("after"); after != "" {
			page, _ = strconv.Atoi(after)
		}
		writer.Header().Set("Link", fmt.Sprintf(`<http://%s%s?state=auto_dismissed,dismissed,fixed,open&per_page=100&after=%d>; rel="next"`,
			request.Host, request.URL.Path, page+1))
		items := make([]string, 100)
		for i := range items {
			items[i] = fmt.Sprintf(`{"number":%d,"state":"open","created_at":"2026-09-20T00:00:00Z","security_advisory":{"severity":"high"}}`, (page-1)*100+i+1)
		}
		_, _ = fmt.Fprintf(writer, "[%s]", strings.Join(items, ","))
	})
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	if _, err := dependabotValueReadAlerts(t.Context(), api, observed); err == nil || *calls != 100 {
		t.Fatalf("100 full alert pages were accepted or overran bound: calls=%d error=%v", *calls, err)
	}
}

func TestDependabotValueCancellationAndScope(t *testing.T) {
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprint(writer, `[]`)
	})
	for _, endpoint := range []string{
		"search/issues?q=repo:other/repo+is:pr+author:app/dependabot",
		"repos/other/repo/dependabot/alerts?state=open",
	} {
		if _, _, _, err := api.request(t.Context(), endpoint); err == nil {
			t.Fatalf("out-of-scope request forwarded: %s", endpoint)
		}
	}
	if *calls != 0 {
		t.Fatal("out-of-scope search used quota")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	if _, err := dependabotValueReadAlerts(ctx, api, observed); err == nil {
		t.Fatal("cancelled acquisition succeeded")
	}
	if *calls != 0 {
		t.Fatal("cancelled acquisition reached GitHub")
	}
}

func TestDependabotValuePacketsPreserveOnlyAcquiredWindows(t *testing.T) {
	observed, _ := time.Parse(time.RFC3339, "2026-09-25T00:00:00Z")
	api, calls := testDependabotAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/search/issues" {
			_, _ = fmt.Fprint(writer, `{"total_count":0,"incomplete_results":false,"items":[]}`)
		} else {
			_, _ = fmt.Fprint(writer, `[]`)
		}
	})
	first, skipped, err := acquireNativeDependabotValue(t.Context(), api, nil, observed)
	if err != nil || skipped || len(first) != 3 {
		t.Fatalf("first collection: %v skip=%v packets=%d", err, skipped, len(first))
	}
	for i, packet := range first {
		if packet["windowStart"] != dependabotValueAdoption || packet["windowEnd"] != "2026-09-24T08:48:00Z" {
			t.Fatal("first cutoff or adoption changed")
		}
		facts := packet["facts"].([]any)
		if len(facts) != 3 || facts[0].(map[string]any)["metric-id"] != dependabotValueMetrics[i].id ||
			facts[1].(map[string]any)["fact-id"] != "cutoff:2026-09-24T08:48:00Z" ||
			facts[2].(map[string]any)["at"] != dependabotValueAdoption {
			t.Fatalf("incorrect native facts: %+v", facts)
		}
		root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
		code := `import { pathToFileURL } from 'node:url';
const {validateOperationalValueEvidence} = await import(pathToFileURL(process.argv[1]));
let input = ''; for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(validateOperationalValueEvidence(JSON.parse(input))));`
		command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code,
			filepath.Join(root, "dashboard/site/src/data/model/operational-value-evidence.js")) // #nosec G204 -- fixed checked-in schema validator.
		encoded, _ := json.Marshal(packet)
		command.Stdin = bytes.NewReader(encoded)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("native evidence rejected by independent packet validator: %v: %s", err, output)
		}
	}
	startCalls := *calls
	if _, skipped, err := acquireNativeDependabotValue(t.Context(), api, first, observed); err != nil || !skipped || *calls != startCalls {
		t.Fatalf("complete same cutoff reread GitHub: %v, %v, %d", err, skipped, *calls)
	}
	next := observed.Add(21 * 24 * time.Hour)
	second, skipped, err := acquireNativeDependabotValue(t.Context(), api, first, next)
	if err != nil || skipped {
		t.Fatalf("new cutoff: %v, %v", err, skipped)
	}
	for i, packet := range second {
		facts := packet["facts"].([]any)
		if len(facts) != 5 || packet["contractDigest"] != first[i]["contractDigest"] ||
			facts[1].(map[string]any)["fact-id"] != "cutoff:2026-09-24T08:48:00Z" ||
			facts[4].(map[string]any)["at"] != "2026-10-08T08:48:00Z" {
			t.Fatalf("retained history or skipped-week coverage changed: %+v", facts)
		}
	}
	partial := append([]model.Row(nil), first[:2]...)
	if _, _, err := acquireNativeDependabotValue(t.Context(), api, partial, next); err == nil {
		t.Fatal("incomplete prior batch was treated as zero")
	}
	malformed := append([]model.Row(nil), first...)
	altered := model.Row{}
	for key, value := range first[0] {
		altered[key] = value
	}
	facts := append([]any(nil), first[0]["facts"].([]any)...)
	snapshot := map[string]any{}
	for key, value := range facts[1].(map[string]any) {
		snapshot[key] = value
	}
	snapshot["value"] = "missing"
	facts[1] = snapshot
	altered["facts"] = facts
	malformed[0] = altered
	if _, _, err := acquireNativeDependabotValue(t.Context(), api, malformed, observed); err == nil {
		t.Fatal("malformed same-cutoff snapshot was reused")
	}
}
