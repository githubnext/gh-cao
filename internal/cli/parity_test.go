package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func nodeRuntime(t *testing.T) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 24+ is required for shared-runtime parity checks")
	}
	output, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(output), "v"))
	major, _, _ := strings.Cut(version, ".")
	number, err := strconv.Atoi(major)
	if err != nil || number < 24 {
		t.Skip("Node.js 24+ is required for shared-runtime parity checks")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return node, repository
}

func TestNativePolicyMatchesProductionContract(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
		if args[0] == "aw" {
			return "", "gh-aw v0.92.0", nil
		}
		return "Acme-Org/ops.tools\n", "", nil
	}, nil)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	policy, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	// Complete frozen initializeCaoPolicy result and policy contract.
	const expected = `{"$schema":"https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json","version":1,"gh-aw-version":"v0.92.0","control-plane":{"scope":{"allowed-owners":["Acme-Org"],"allowed-repositories":["Acme-Org/ops.tools"]},"marketplace":{"registries":[{"id":"official","name":"Official CAO catalog","repository":"githubnext/gh-cao","ref":"main","auth":{"type":"none"}},{"id":"agentics","name":"Agentics workflows","repository":"githubnext/agentics","path":"workflows","format":"workflows","ref":"main","auth":{"type":"none"}}]},"campaigns":{}}}`
	var want map[string]any
	decoder := json.NewDecoder(strings.NewReader(expected))
	decoder.UseNumber()
	if err := decoder.Decode(&want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy, want) {
		t.Fatalf("native initial policy = %v, want frozen policy %v", policy, want)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"command": "init", "policy": policyPath, "gh-aw-version": "v0.92.0"}; !reflect.DeepEqual(result, want) {
		t.Fatalf("init result = %v, want %v", result, want)
	}
}
