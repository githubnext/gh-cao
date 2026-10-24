package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativePopulatedNamedQueriesMatchProduction(t *testing.T) {
	node, repository := nodeRuntime(t)
	directory := t.TempDir()
	database := filepath.Join(directory, "snapshot.sqlite")
	fixture := filepath.Join(directory, "fixture.json")
	batch := map[string][]map[string]any{
		"campaigns":    {{"id": "campaign:dependabot", "slug": "dependabot", "name": "Dependabot", "enabled": true, "mode": "review", "workerCount": 1}},
		"repositories": {{"id": "repository:org/repo", "owner": "org", "name": "repo", "fullName": "org/repo", "visibility": "public"}},
		"workflows": {{
			"id": "workflow:org/repo/planner", "repositoryId": "repository:org/repo", "campaignId": "campaign:dependabot",
			"name": "Dependabot planner", "path": ".github/workflows/dependabot-update-planner.lock.yml", "role": "orchestrator",
		}},
		"runs": {
			{"id": "github:run:org/repo:1", "repositoryId": "repository:org/repo", "workflowId": "workflow:org/repo/planner",
				"owner": "org", "repository": "repo", "workflowPath": ".github/workflows/dependabot-update-planner.lock.yml",
				"githubRunId": 1, "status": "completed", "conclusion": "failure", "startedAt": "2026-01-01T00:00:00.000Z",
				"createdAt": "2026-01-01T00:00:00.000Z", "updatedAt": "2026-01-01T00:01:00.000Z",
				"observedAt": "2026-01-02T00:00:00.000Z", "runLink": "https://github.com/org/repo/actions/runs/1",
				"totalTokens": 1000, "totalCost": 0.4, "durationMs": 60000, "event": "schedule", "rolloutMode": "review"},
			{"id": "github:run:org/repo:2", "repositoryId": "repository:org/repo", "workflowId": "workflow:org/repo/planner",
				"owner": "org", "repository": "repo", "workflowPath": ".github/workflows/dependabot-update-planner.lock.yml",
				"githubRunId": 2, "status": "completed", "conclusion": "success", "startedAt": "2026-01-02T00:00:00.000Z",
				"createdAt": "2026-01-02T00:00:00.000Z", "updatedAt": "2026-01-02T00:02:00.000Z",
				"observedAt": "2026-01-02T00:03:00.000Z", "runLink": "https://github.com/org/repo/actions/runs/2",
				"totalTokens": 2000, "totalCost": 0.6, "durationMs": 120000, "event": "workflow_dispatch", "rolloutMode": "review"},
		},
		"tools": {{"id": "tool:1", "runId": "github:run:org/repo:1", "type": "mcp.tool_call", "source": "mcp",
			"mcpServer": "github", "mcpTool": "search_issues", "summary": "search_issues", "status": "success",
			"timestamp": "2026-01-01T00:00:30.000Z", "observedAt": "2026-01-02T00:00:00.000Z"}},
		"domains": {{"id": "domain:1", "runId": "github:run:org/repo:1", "domain": "example.com", "status": "blocked",
			"type": "firewall", "summary": "Blocked", "timestamp": "2026-01-01T00:00:30.000Z", "observedAt": "2026-01-02T00:00:00.000Z"}},
		"issues": {
			{"id": "issue:1", "runId": "github:run:org/repo:2", "type": "safe_output.result", "safeOutputType": "create_issue",
				"isPullRequest": false, "correlationId": "https://github.com/org/target/issues/42", "status": "success",
				"summary": "Dependency review", "timestamp": "2026-01-02T00:01:30.000Z", "observedAt": "2026-01-02T00:03:00.000Z"},
			{"id": "issue:2", "runId": "github:run:org/repo:1", "type": "safe_output.result", "safeOutputType": "create_pull_request",
				"isPullRequest": true, "correlationId": "https://github.com/org/target/pull/17", "status": "success",
				"summary": "Dependency update", "timestamp": "2026-01-01T00:01:30.000Z", "observedAt": "2026-01-02T00:03:00.000Z"},
		},
	}
	ghCases := []struct {
		Resource string            `json:"resource"`
		Options  map[string]string `json:"options"`
		Args     []string          `json:"-"`
	}{
		{Resource: "runs", Args: []string{"gh", "runs"}},
		{Resource: "runs", Options: map[string]string{"repo": "ORG/REPO", "status": "failure", "workflow": "dependabot-update-planner.lock"}, Args: []string{"gh", "runs", "-R", "ORG/REPO", "--status", "failure", "--workflow", "dependabot-update-planner.lock"}},
		{Resource: "runs", Options: map[string]string{"limit": "1", "since": "2026-01-01", "until": "2026-01-01"}, Args: []string{"gh", "runs", "--limit", "1", "--since", "2026-01-01", "--until", "2026-01-01"}},
		{Resource: "issues", Options: map[string]string{"repo": "org/target"}, Args: []string{"gh", "issues", "-R", "org/target"}},
		{Resource: "issues", Options: map[string]string{"repo": "org/repo"}, Args: []string{"gh", "issues", "-R", "org/repo"}},
		{Resource: "prs", Options: map[string]string{"workflow": "Dependabot planner"}, Args: []string{"gh", "prs", "--workflow", "Dependabot planner"}},
	}
	ghJSON, err := json.Marshal(ghCases)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fixture, string(content))
	program := `
import {readFile} from 'node:fs/promises';
const root = process.argv[1];
const {createDatabase} = await import(root + '/activity/cli/ingestion.mjs');
const {upsertCanonicalBatch,openCanonicalDatabase} = await import(root + '/dashboard/site/src/data/storage/indexeddb.js');
const {readAgentCatalog,runNamedDashboardQuery} = await import(root + '/activity/agent-catalog.mjs');
const {queryGhData} = await import(root + '/activity/cli/queries.mjs');
const indexedDB = await createDatabase(process.argv[2]);
await upsertCanonicalBatch(indexedDB, JSON.parse(await readFile(process.argv[3],'utf8')), {validateRelationships:false});
// This fixture supplies available evidence, not an exhaustive upstream history.
const db = await openCanonicalDatabase(indexedDB);
const transaction = db.transaction('dailyRunCountState', 'readwrite');
transaction.objectStore('dailyRunCountState').put({
  id:'daily-run-counts',status:'ready',coverage:'available-evidence-only',excludedRuns:0,conflictedRuns:0
});
await new Promise((resolve,reject) => { transaction.oncomplete=resolve;transaction.onerror=() => reject(transaction.error); });
db.close();
const catalog = await readAgentCatalog();
const results = {};
for (const query of catalog.queries) {
  const parameters = Object.fromEntries(query.parameters.filter(p => p.required).map(p => [p.name,
    p.schema?.default ?? p.schema?.enum?.[0] ?? (p.type === 'number' ? 1 : p.type === 'boolean' ? false : 'test')]));
  try {
    results[query.id] = {parameters,result:await runNamedDashboardQuery({indexedDB,queryId:query.id,parameters,limit:50})};
  } catch (error) {
    results[query.id] = {parameters,error:error.message};
  }
}
const gh = [];
for (const entry of JSON.parse(process.argv[4])) gh.push(await queryGhData(indexedDB,entry.resource,entry.options ?? {}));
process.stdout.write(JSON.stringify({queries:results,gh}));
`
	command := exec.Command(node, "--input-type=module", "-e", program, repository, database, fixture, string(ghJSON))
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("prepare production snapshot: %v: %s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	var baseline struct {
		Queries map[string]struct {
			Parameters map[string]any `json:"parameters"`
			Result     map[string]any `json:"result"`
			Error      string         `json:"error"`
		} `json:"queries"`
		Gh []any `json:"gh"`
	}
	if err := json.Unmarshal(output, &baseline); err != nil {
		t.Fatal(err)
	}
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	db, err := openNativeSnapshot(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, definition := range catalog.queries {
		id := nativeText(definition["name"])
		t.Run(id, func(t *testing.T) {
			want := baseline.Queries[id]
			got, err := catalog.runQuery(context.Background(), db, id, want.Parameters, 50)
			if want.Error != "" {
				if err == nil || err.Error() != want.Error {
					t.Fatalf("error=%v, want %q", err, want.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var normalized map[string]any
			if err := json.Unmarshal(content, &normalized); err != nil {
				t.Fatal(err)
			}
			if id == "engines-models-sample-distribution" {
				frame, ok := normalized["metadata"].(map[string]any)["statistical-frame"].(map[string]any)
				if !ok || frame["population-count"] != float64(2) || frame["capacity"] != float64(8000) {
					t.Fatalf("named query lost its populated SQLite sampling frame: %v", normalized)
				}
			}
			if !reflect.DeepEqual(normalized, want.Result) {
				actual, _ := json.Marshal(normalized)
				expected, _ := json.Marshal(want.Result)
				t.Fatalf("native/production named query differs:\nGo: %s\nJS: %s", actual, expected)
			}
		})
	}
	for index, entry := range ghCases {
		t.Run(strings.Join(entry.Args, " "), func(t *testing.T) {
			root := NewRootCommand("test")
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(append(entry.Args, "--database", database))
			if err := root.Execute(); err != nil {
				t.Fatalf("native GitHub listing: %v: %s", err, &output)
			}
			var got any
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, baseline.Gh[index]) {
				t.Fatalf("native/production listing differs: Go=%v JS=%v", got, baseline.Gh[index])
			}
		})
	}
}
