package dashboardingest

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHydrateUsesProductionAdapterAndNormalizer(t *testing.T) {
	root := "../../dashboard/site/test/fixtures/gh-aw-logs"
	cache, err := os.ReadFile(filepath.Join(root, "cached-v2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	contextFile, err := os.ReadFile(filepath.Join(root, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifacts map[string]any
	if err := json.Unmarshal(contextFile, &artifacts); err != nil {
		t.Fatal(err)
	}
	files := []map[string]any{}
	for _, path := range []string{
		"run-303/mcp-logs/gateway.jsonl",
		"run-303/sandbox/firewall/audit/audit.jsonl",
		"run-303/sandbox/agent/logs/copilot-session-state/session-505/events.jsonl",
	} {
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]any{"path": path, "content": string(content)})
	}
	artifacts["files"] = files
	assertProductionNormalization(t, Input{JSONL: string(cache), Artifacts: []map[string]any{artifacts}})
	session, err := os.ReadFile(filepath.Join(root, "../unified-telemetry-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts["files"] = []map[string]any{{"path": "run-303/usage/aw_session.jsonl", "content": string(session)}}
	assertProductionNormalization(t, Input{JSONL: string(cache), Artifacts: []map[string]any{artifacts}})
}

func assertProductionNormalization(t *testing.T, input Input) {
	t.Helper()
	got, err := Hydrate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []string{"runs", "tools", "domains", "audits"} {
		if len(got[store]) == 0 {
			t.Fatalf("production rich fixture did not hydrate %s", store)
		}
	}
	content, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e",
		`import {hydrate} from './dashboard/report/hydrate-local-logs.mjs';
		let input='';for await(const chunk of process.stdin)input+=chunk;
		console.log(hydrate(JSON.parse(input)));`)
	cmd.Dir = "../.."
	cmd.Stdin = bytes.NewReader(content)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string][]map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("native embedded normalization diverged from the production JavaScript adapter")
	}
}
