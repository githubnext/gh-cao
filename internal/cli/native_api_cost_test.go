package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/apicost"
)

func nativeAPICostEvidence(outputs, implementations int) map[string]any {
	facts := []any{map[string]any{
		"fact-kind": "contract", "fact-id": "contract", "metric-id": "oversized-files-reduced",
		"metric-version": "observed-800-line-net-reductions-v1",
	}}
	for index := range outputs {
		facts = append(facts, map[string]any{
			"fact-kind": "output", "fact-id": fmt.Sprintf("output-%d", index),
		})
	}
	for index := range implementations {
		facts = append(facts, map[string]any{
			"fact-kind": "implementation", "fact-id": fmt.Sprintf("implementation-%d", index),
		})
	}
	return map[string]any{
		"id":         "operational-value-evidence:github%2Fgh-aw%3Adaily-file-diet%3Aoversized-files-reduced%3Aobserved-800-line-net-reductions-v1",
		"repository": "github/gh-aw", "workflow": "daily-file-diet",
		"sourcePath": ".github/workflows/daily-file-diet.md",
		"observedAt": "2026-10-18T12:05:52.580Z", "windowStart": "2025-01-01T00:00:00Z",
		"windowEnd": "2026-10-18T11:54:09Z", "contractDigest": strings.Repeat("a", 64),
		"facts": facts,
	}
}

func TestNativeAPICostReadsCanonicalSQLiteEvidence(t *testing.T) {
	file := nativeTestSnapshot(t, map[string][]map[string]any{
		"operationalValueEvidence": {nativeAPICostEvidence(95, 51)},
	})
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{
		"api-cost", "--database", file, "--profile", "daily-file-diet",
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
		!strings.Contains(strings.Join(report.Assumptions, "\n"), "canonical SQLite database") {
		t.Fatalf("report=%+v", report)
	}
}

func TestNativeAPICostSelectsLatestCanonicalEvidence(t *testing.T) {
	unrelated := nativeAPICostEvidence(1, 0)
	unrelated["id"] = "operational-value-evidence:octo%2Fother%3Adaily-file-diet%3Aunrelated"
	unrelated["repository"] = "octo/other"
	unrelated["facts"] = []any{}
	older := nativeAPICostEvidence(95, 51)
	older["id"] = "operational-value-evidence:github%2Fgh-aw%3Adaily-file-diet%3Aolder"
	older["observedAt"] = "2026-10-18T12:05:52.580Z"
	older["windowEnd"] = "2026-10-18T11:54:09Z"
	newer := nativeAPICostEvidence(7, 3)
	newer["id"] = "operational-value-evidence:github%2Fgh-aw%3Adaily-file-diet%3Anewer"
	newer["observedAt"] = "2026-11-03T12:05:52.580Z"
	newer["windowEnd"] = "2026-11-03T11:54:09Z"
	file := nativeTestSnapshot(t, map[string][]map[string]any{
		"operationalValueEvidence": {unrelated, older, newer},
	})
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"api-cost", "--database", file})
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
	root.SetArgs([]string{"api-cost", "--database", file})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "no retained Daily File Diet evidence") {
		t.Fatalf("error=%v", err)
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
