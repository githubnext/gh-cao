package operationalvalue

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestLocalCollectionHealthNeedsNeitherCollectorNorCredential(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	policy := filepath.Join(snapshot, "cao.json")
	content := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{
		"scope":{"allowed-repositories":["org/repo"]},"activity":{"repositories":["org/repo"]},
		"campaigns":{"activity":{"workflow-values":[
			{"repository":"org/repo","workflow":"worker","collector":"not-present.mjs"}
		]}}}}`
	if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root, Repositories: []string{"org/repo"}, Environment: []string{
		"PATH=" + os.Getenv("PATH"), "GH_TOKEN=test-only", "GITHUB_TOKEN=test-only",
	}}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-values-collections.jsonl")) // #nosec G304 -- fixed test-owned temporary output.
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Record map[string]any `json:"workflow_value_collection"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Record["registration"] != "enabled" || envelope.Record["state"] != "deferred" ||
		envelope.Record["reasonCode"] != "shared-budget-unavailable" || envelope.Record["lastAttemptAt"] != nil {
		t.Fatalf("hosted health did not preserve registration without an attempt: %v", envelope.Record)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "workflow-value-workspaces")); !os.IsNotExist(err) {
		t.Fatal("health publication must not start acquisition or create proof workspaces")
	}
}

func TestLocalRuntimeCancellationOwnsDescendantsAndStripsCredentials(t *testing.T) {
	directory := t.TempDir()
	started, leaked := filepath.Join(directory, "started"), filepath.Join(directory, "leaked")
	marker, _ := json.Marshal(started)
	target, _ := json.Marshal(leaked)
	child := `require("node:fs").writeFileSync(` + string(marker) + `,"ready");
		setTimeout(()=>require("node:fs").writeFileSync(` + string(target) + `,"leaked"),500);
		setInterval(()=>{},1000)`
	childSource, _ := json.Marshal(child)
	source := `if(process.env.GH_TOKEN || process.env.GITHUB_TOKEN || process.env.CAO_OPERATIONAL_VALUE_GH_TOKEN)
		throw new Error("credentials crossed local boundary");
		require("node:child_process").spawn(process.execPath,["-e",` + string(childSource) + `]);
		setInterval(()=>{},1000)`
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runLocalRuntime(ctx, Config{Root: directory, WorkerTimeout: 5 * time.Second,
			Environment: []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=test-only", "GITHUB_TOKEN=test-only",
				"CAO_OPERATIONAL_VALUE_GH_TOKEN=test-only"}},
			[]string{"-e", source})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("local child did not start with credentials removed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("local runtime did not start within its bounded deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled local work must report failure")
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatal("a descendant survived local runtime cancellation")
	}
}

func TestHostedSnapshotQueriesCanonicalDataWithoutSQLite(t *testing.T) {
	root := t.TempDir()
	activity := filepath.Join(root, "activity")
	if err := os.MkdirAll(activity, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `import { connect } from "node:net";
import { writeFile } from "node:fs/promises";
const args = process.argv.slice(2);
if (args.includes("--database")) throw new Error("hosted execution received a SQLite database");
const output = args[args.indexOf("--output") + 1];
const socket = connect(process.env.CAO_DASHBOARD_QUERY_SOCKET);
let response = "";
socket.setEncoding("utf8");
socket.on("data", chunk => response += chunk);
const done = new Promise((resolve, reject) => {
  socket.on("end", resolve);
  socket.on("error", reject);
});
socket.end(JSON.stringify({schemaVersion: 1, queries: [{name:"rows", from:"runs"}], requested:["rows"]}));
await done;
const parsed = JSON.parse(response);
if (parsed.sources.rows.rows[0].repository !== "api") throw new Error("canonical rows unavailable");
await writeFile(output, JSON.stringify({schema_version:2,kind:"operational_value",operational_value:{
  timestamp:"2026-01-01T00:00:00Z",repository:"octo/api",campaign:"optimization",
  campaign_id:"campaign:optimization",value_id:"worker.efficiency",value:1
}}) + "\n");
console.log(JSON.stringify({values:1,warnings:[]}));`
	if err := os.WriteFile(filepath.Join(activity, "local-values.mjs"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "operational-values.jsonl")
	queries := 0
	err := ComputeHostedSnapshot(t.Context(), Config{
		Root: root, NodeBinary: "node", Output: output,
		Environment: []string{"PATH=" + os.Getenv("PATH")}, WorkerTimeout: 10 * time.Second,
	}, "unused-policy", "unused-snapshot", output,
		func(_ context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
			queries++
			if len(definitions) != 1 || definitions[0].Name != "rows" ||
				len(requested) != 1 || requested[0] != "rows" {
				t.Fatalf("unexpected canonical query: %#v %#v", definitions, requested)
			}
			return map[string]model.Source{"rows": {
				Source: "rows", Rows: []model.Row{{"repository": "api"}},
				Metadata: model.Metadata{"availability": "available"},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("canonical query count = %d, want 1", queries)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(strings.ToLower(path), ".sqlite") {
			t.Fatalf("hosted execution created SQLite file %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHostedQueryBridgeRejectsOversizedRequests(t *testing.T) {
	queries := 0
	bridge, err := startHostedQueryBridge(t.Context(),
		func(context.Context, []query.Definition, []string) (map[string]model.Source, error) {
			queries++
			return map[string]model.Source{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	connection, err := (&net.Dialer{}).DialContext(t.Context(), "unix", bridge.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte(strings.Repeat(" ", hostedQueryRequestBytes+1))); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if unix, ok := connection.(*net.UnixConn); ok {
		if err := unix.CloseWrite(); err != nil {
			_ = connection.Close()
			t.Fatal(err)
		}
	}
	content, err := io.ReadAll(connection)
	_ = connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	var response hostedQueryResponse
	if err := json.Unmarshal(content, &response); err != nil {
		t.Fatal(err)
	}
	if queries != 0 || !strings.Contains(response.Error, "byte budget") {
		t.Fatalf("oversized request response = %#v, queries = %d", response, queries)
	}
}

func TestReadMeasurementsPreservesMissingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.jsonl")
	content := `{"schema_version":2,"kind":"operational_value","operational_value":{` +
		`"timestamp":"2026-01-01T00:00:00Z","repository":"Octo/API","campaign":"optimization",` +
		`"campaign_id":"campaign:optimization","value_id":"worker.efficiency","value":null,` +
		`"maturity_status":"unavailable","window_start":"2025-12-01T00:00:00Z",` +
		`"window_end":"2026-01-01T00:00:00Z","measurement_kind":"interval",` +
		`"coverage_status":"missing","missing_reason":"no-eligible-runs"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadMeasurements(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["value"] != nil || rows[0]["coverage-status"] != "missing" ||
		rows[0]["missing-reason"] != "no-eligible-runs" ||
		rows[0]["repositoryId"] != "repository:octo%2Fapi" {
		t.Fatalf("canonical missing measurement = %#v", rows)
	}
}

func TestReadMeasurementsRejectsInvalidMissingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.jsonl")
	content := `{"schema_version":2,"kind":"operational_value","operational_value":{` +
		`"timestamp":"2026-01-01T00:00:00Z","repository":"octo/api","campaign":"optimization",` +
		`"value_id":"worker.efficiency","value":null,"maturity_status":"unavailable",` +
		`"coverage_status":"missing","missing_reason":"no-eligible-runs"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeasurements(path); err == nil {
		t.Fatal("invalid unavailable measurement was accepted")
	}
}
