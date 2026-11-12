package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/apicost"
)

func nativeAPICostEvidence(outputs, implementations int) map[string]any {
	facts := []any{map[string]any{
		"fact-kind": "contract", "fact-id": "contract", "metric-id": "oversized-files-reduced",
		"metric-version": "observed-800-line-net-reductions-v1",
		"goal":           "Reduce oversized files", "acceptance": "Accepted reductions",
		"workflow-name": "Daily File Diet", "metric-name": "Reduced files",
		"metric-unit": "files", "metric-direction": "increase",
		"metric-formula": "Accepted file reductions", "measurement-basis": "workflow-contribution",
		"cadence-days": 1, "outputs-available": true,
		"repository-state-available": false, "checks-available": false,
	}, map[string]any{
		"fact-kind": "coverage", "fact-id": "window", "coverage-status": "partial",
		"at": "2025-01-01T00:00:00Z", "end-at": "2026-10-18T11:54:09Z",
	}}
	for index := range outputs {
		facts = append(facts, map[string]any{
			"fact-kind": "output", "fact-id": fmt.Sprintf("output-%d", index),
			"at": "2026-10-18T11:00:00Z", "origin": "corroborated", "state": "open",
			"label": "Issue", "title": "Reduce oversized file",
			"url": fmt.Sprintf("https://github.com/github/gh-aw/issues/%d", index+1),
		})
	}
	for index := range implementations {
		facts = append(facts, map[string]any{
			"fact-kind": "implementation", "fact-id": fmt.Sprintf("implementation-%d", index),
			"at": "2026-10-18T11:00:00Z", "accepted-at": "2026-10-18T11:00:00Z",
			"origin": "receipt-verified", "ref": strings.Repeat("a", 40), "value": 1,
		})
	}
	return map[string]any{
		"schemaVersion": 1,
		"id":            "operational-value-evidence:github%2Fgh-aw%3Adaily-file-diet%3Aoversized-files-reduced%3Aobserved-800-line-net-reductions-v1",
		"repository":    "github/gh-aw", "workflow": "daily-file-diet",
		"repositoryId": "repository:github%2Fgh-aw",
		"workflowId":   "workflow:github%2Fgh-aw%3A.github%2Fworkflows%2Fdaily-file-diet.md",
		"sourcePath":   ".github/workflows/daily-file-diet.md",
		"observedAt":   "2026-10-18T12:05:52.580Z", "windowStart": "2025-01-01T00:00:00Z",
		"windowEnd": "2026-10-18T11:54:09Z", "contractDigest": strings.Repeat("a", 64),
		"facts": facts,
	}
}

func TestNativeAPICostReadsCanonicalPostgresEvidence(t *testing.T) {
	file := nativeTestSnapshot(t, map[string][]map[string]any{
		"repositories": {{"id": "repository:github%2Fgh-aw", "owner": "github", "name": "gh-aw"}},
		"workflows": {{"id": "workflow:github%2Fgh-aw%3A.github%2Fworkflows%2Fdaily-file-diet.md",
			"repositoryId": "repository:github%2Fgh-aw", "path": ".github/workflows/daily-file-diet.md"}},
		"operationalValueEvidence": {nativeAPICostEvidence(95, 51)},
	})
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{
		"api-cost", "--postgres-url", file, "--profile", "daily-file-diet",
		"--repositories", "100",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var report apicost.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Detail["trackedIssues"] != float64(95) ||
		report.Detail["retainedImplementations"] != float64(51) ||
		report.Capacity.ModeledMaximumRepositories != 6 ||
		!strings.Contains(strings.Join(report.Assumptions, "\n"), "canonical PostgreSQL") {
		t.Fatalf("report=%+v", report)
	}
}

func TestNativeAPICostSelectsLatestCanonicalEvidence(t *testing.T) {
	unrelated := nativeAPICostEvidence(1, 0)
	unrelated["id"] = "operational-value-evidence:octo%2Fother%3Adaily-file-diet%3Aoversized-files-reduced%3Aobserved-800-line-net-reductions-v1"
	unrelated["repository"] = "octo/other"
	unrelated["repositoryId"] = "repository:octo%2Fother"
	unrelated["workflowId"] = "workflow:octo%2Fother%3A.github%2Fworkflows%2Fdaily-file-diet.md"
	older := nativeAPICostEvidence(95, 51)
	older["id"] = "operational-value-evidence:github%2Fgh-aw%3Adaily-file-diet%3Aoversized-files-reduced%3Aobserved-800-line-net-reductions-v0"
	older["facts"].([]any)[0].(map[string]any)["metric-version"] = "observed-800-line-net-reductions-v0"
	older["observedAt"] = "2026-10-18T12:05:52.580Z"
	older["windowEnd"] = "2026-10-18T11:54:09Z"
	newer := nativeAPICostEvidence(7, 3)
	newer["observedAt"] = "2026-11-03T12:05:52.580Z"
	newer["windowEnd"] = "2026-11-03T11:54:09Z"
	file := nativeTestSnapshot(t, map[string][]map[string]any{
		"repositories": {
			{"id": "repository:github%2Fgh-aw", "owner": "github", "name": "gh-aw"},
			{"id": "repository:octo%2Fother", "owner": "octo", "name": "other"},
		},
		"workflows": {
			{"id": "workflow:github%2Fgh-aw%3A.github%2Fworkflows%2Fdaily-file-diet.md",
				"repositoryId": "repository:github%2Fgh-aw", "path": ".github/workflows/daily-file-diet.md"},
			{"id": "workflow:octo%2Fother%3A.github%2Fworkflows%2Fdaily-file-diet.md",
				"repositoryId": "repository:octo%2Fother", "path": ".github/workflows/daily-file-diet.md"},
		},
		"operationalValueEvidence": {unrelated, older, newer},
	})
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"api-cost", "--postgres-url", file})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var report apicost.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Detail["trackedIssues"] != float64(7) ||
		report.Detail["retainedImplementations"] != float64(3) {
		t.Fatalf("report=%+v", report)
	}
}

func TestNativeAPICostRetainedFailsWithoutDatabaseEvidence(t *testing.T) {
	file := nativeTestSnapshot(t, nil)
	root := NewRootCommand("test")
	root.SetArgs([]string{"api-cost", "--postgres-url", file})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "no retained Daily File Diet evidence") {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeAPICostRejectsCountsOutsideIntRange(t *testing.T) {
	maxIntExclusive := math.Ldexp(1, strconv.IntSize-1)
	for _, field := range []string{"tracked-issues", "retained-implementations"} {
		t.Run(field, func(t *testing.T) {
			row := map[string]any{
				"tracked-issues": 1.0, "retained-implementations": 1.0,
				"observed-at": "2026-11-03T12:05:52.580Z", "window-end": "2026-11-03T11:54:09Z",
			}
			row[field] = maxIntExclusive
			_, err := nativeAPICostPopulation([]any{row}, map[string]any{"availability": "available"}, "test")
			if err == nil {
				t.Fatal("expected an out-of-range count to be rejected")
			}
		})
	}
}

func TestNativeAPICostHardBoundNeedsNoSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"api-cost", "--scenario", "hard-bound"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var report apicost.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil ||
		report.Capacity.ModeledMaximumRepositories != 0 {
		t.Fatalf("report=%+v error=%v", report, err)
	}
}

func TestNativeAPICostReadsCustomScenarioWithoutDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(file, []byte(`{
		"schemaVersion":1,"name":"Cached metadata",
		"quotas":{"core":{"limit":100}},
		"operations":[{
			"id":"cached","pool":"core","scale":{"perRepository":1},
			"requestCount":0,"unitCost":0
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"api-cost", "--input", file})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var report apicost.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Profile != "custom" || report.Requested.Pools["core"].DemandOverHorizon != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestNativeAPICostReadsHostedCanonicalEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/query" ||
			request.Header.Get("Authorization") != "Bearer local-token" {
			t.Fatalf("request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}

		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if sources, _ := body["sourceNames"].([]any); len(sources) != 1 || sources[0] != nativeAPICostQuery {
			t.Fatalf("body=%v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"revision":17,"evaluatedAt":"2026-11-03T00:00:00Z","sources":{%q:{"source":%q,"rows":[{"tracked-issues":95,"retained-implementations":51,"observed-at":"2026-10-18T12:05:52.580Z","window-end":"2026-10-18T11:54:09Z"}],"metadata":{"availability":"available"}}},"metrics":{}}`,
			nativeAPICostQuery, nativeAPICostQuery)
	}))
	defer server.Close()
	population, err := nativeDailyFileDietServerPopulation(t.Context(), server.URL, "github/gh-aw",
		server.Client(), func(name string) string {
			if name == "CAO_SERVER_TOKEN" {
				return "local-token"
			}
			return ""
		})
	if err != nil {
		t.Fatal(err)
	}
	if population.Outputs != 95 || population.Implementations != 51 ||
		population.Source != "canonical server database revision 17" {
		t.Fatalf("population=%+v", population)
	}
}

func TestNativeAPICostReadsHostedCalibration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if sources, _ := body["sourceNames"].([]any); len(sources) != 1 || sources[0] != nativeAPICostCostQuery {
			t.Fatalf("body=%v", body)
		}
		queries, _ := body["queries"].([]any)
		if len(queries) != 1 {
			t.Fatalf("cost calibration query is absent: %v", body)
		}
		query, _ := queries[0].(map[string]any)
		aggregate, _ := query["aggregate"].(map[string]any)
		if len(aggregate) == 0 || query["limit"] != float64(1) {
			t.Fatalf("cost calibration was not aggregated declaratively: %v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"revision":19,"sources":{%q:{"rows":[
			{"attempts":2,"core-requests":6,"search-requests":4,"graphql-requests":2,
			"graphql-points-observed":6,"wall-ms":90000,"max-wall-ms":60000,
			"output-bytes":100,"transport-bytes":200}],"metadata":{"availability":"available"}}}}`,
			nativeAPICostCostQuery)
	}))
	defer server.Close()
	calibration, err := nativeDailyFileDietServerCalibration(t.Context(), server.URL,
		server.Client(), func(name string) string {
			if name == "CAO_SERVER_TOKEN" {
				return "local-token"
			}
			return ""
		})
	if err != nil {
		t.Fatal(err)
	}
	if calibration.Attempts != 2 || calibration.CoreRequests != 6 ||
		calibration.GraphQLPointsObserved != 6 || calibration.MaxWallMS != 60000 ||
		!strings.Contains(calibration.Source, "revision 19") {
		t.Fatalf("calibration=%+v", calibration)
	}
}

func TestNativeAPICostRejectsEmptyHostedCalibration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"revision":19,"sources":{%q:{"rows":[{
			"attempts":0,"core-requests":0,"search-requests":0,"graphql-requests":0,
			"graphql-points-observed":0,"wall-ms":0,"max-wall-ms":null,
			"output-bytes":0,"transport-bytes":0}],"metadata":{"availability":"available"}}}}`,
			nativeAPICostCostQuery)
	}))
	defer server.Close()
	_, err := nativeDailyFileDietServerCalibration(t.Context(), server.URL,
		server.Client(), func(name string) string {
			if name == "CAO_SERVER_TOKEN" {
				return "local-token"
			}
			return ""
		})
	if err == nil || !strings.Contains(err.Error(), "no hosted Daily File Diet") {
		t.Fatalf("empty calibration error=%v", err)
	}
}
