package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testIdentity() Identity { return Identity{"octo/api", 7, 303, 1} }

func oracle(t *testing.T, script string, input []byte) []byte {
	t.Helper()
	_, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required only for the independent Activity oracle")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- scripts are fixed test-only Activity oracles, never request input.
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	command.Dir = root
	command.Stdin = bytes.NewReader(input)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "DO_NOT_TRACK=1", "GH_TELEMETRY=0"}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Activity oracle: %v: %s", err, output)
	}
	return output
}

func comparable(t *testing.T, value any) any {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSessionActivityOracle(t *testing.T) {
	for _, name := range []string{"unified-telemetry-session.jsonl", "evaluation-session.jsonl"} {
		t.Run(name, func(t *testing.T) {
			content := readSessionFixture(t, name)
			expected := oracle(t, `
import { parseUnifiedSessionDocument } from './dashboard/site/src/data/adapters/unified-session.js';
let content = ''; for await (const chunk of process.stdin) content += chunk;
console.log(JSON.stringify({version:1,path:'usage/aw_session.jsonl',
 ...parseUnifiedSessionDocument(content,'usage/aw_session.jsonl',{runId:'303',attempt:1})}));
`, content)
			got, gaps, err := ProjectSession(t.Context(), content, "usage/aw_session.jsonl", testIdentity(), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			if actual := comparable(t, got); !reflect.DeepEqual(actual, want) {
				gotJSON, _ := json.MarshalIndent(actual, "", "  ")
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("safe session projection differs:\ngot=%s\nwant=%s", gotJSON, wantJSON)
			}
			if bytes.Contains(expected, []byte("PRIVATE_")) {
				t.Fatal("oracle leaked private fixture content")
			}
			if len(gaps) != 0 {
				t.Fatalf("unexpected coverage gaps: %v", gaps)
			}
		})
	}
}

func TestSessionNoRuntimeOrFiles(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	content := []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
		"{\"type\":\"user.message\",\"data\":{\"content\":\"PRIVATE_PROMPT\"}}\n" +
		"{\"type\":\"tool.execution_start\",\"data\":{\"toolCallId\":\"1\",\"toolName\":\"github-get_issue\",\"mcpServerName\":\"github\"}}\n" +
		"{\"type\":\"tool.execution_complete\",\"data\":{\"toolCallId\":\"1\",\"success\":false,\"output\":\"PRIVATE_OUTPUT\"}}\n")
	document, _, err := ProjectSession(t.Context(), content, "usage/aw_session.jsonl", testIdentity(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(document)
	if bytes.Contains(result, []byte("PRIVATE")) {
		t.Fatal("unsafe session data retained")
	}
	events := document["events"].([]map[string]any)
	completion := events[len(events)-1]
	if completion["source"] != "mcp" || object(completion["fields"])["status"] != "error" {
		t.Fatalf("lost correlated tool identity or failure: %v", completion)
	}
}

func TestSessionBoundsAndInvalidEvidence(t *testing.T) {
	for _, input := range []string{"", "null", "[]", "{\"type\":\"session.format\",\"data\":null}", "{\"type\":\"x\",\"data\":{}}\nnot-json"} {
		if _, _, err := ProjectSession(t.Context(), []byte(input), "usage/aw_session.jsonl", testIdentity(), Limits{}); err == nil {
			t.Fatalf("accepted invalid session %q", input)
		}
	}
	content := []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n")
	if _, _, err := ProjectSession(t.Context(), content, "usage/aw_session.jsonl", testIdentity(), Limits{Bytes: 1}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("byte budget not enforced: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := ProjectSession(canceled, content, "usage/aw_session.jsonl", testIdentity(), Limits{}); err == nil {
		t.Fatal("cancellation ignored")
	}
	unsupported, _, err := ProjectSession(t.Context(), []byte("{\"type\":\"session.format\",\"data\":{\"version\":2}}\nPRIVATE_INVALID_TAIL"),
		"usage/aw_session.jsonl", testIdentity(), Limits{})
	if err != nil || object(unsupported["events"].([]map[string]any)[0]["fields"])["status"] != "unsupported" {
		t.Fatalf("unsupported format coverage lost: %v %v", unsupported, err)
	}
}

func TestSessionOtherAttemptAndUnknownEventsRemainExplicit(t *testing.T) {
	content := []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
		"{\"type\":\"usage.report\",\"data\":{\"runAttempt\":2,\"aic\":99}}\n" +
		"{\"type\":\"unknown.extension\",\"data\":{\"secret\":\"PRIVATE_SECRET\"}}\n")
	document, gaps, err := ProjectSession(t.Context(), content, "usage/aw_session.jsonl", testIdentity(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(object(document["run"])) != 0 || !reflect.DeepEqual(gaps, []string{"unsupported-session-event"}) {
		t.Fatalf("other attempt or unknown extension misrepresented: %v %v", document, gaps)
	}
}

func TestCompactFailsBeforeWritingForeignEvidence(t *testing.T) {
	for _, input := range []string{
		`{"kind":"run","run":{"repository":"other/repo","run_id":303,"workflow_path":".github/workflows/test.lock.yml"}}`,
		`{"kind":"workflow_runs","request":{"repository":"other/repo"},"payload":[]}`,
		`not-json`,
	} {
		var output bytes.Buffer
		if err := Compact(t.Context(), "octo/api", strings.NewReader(input), &output, Limits{}); err == nil || output.Len() != 0 {
			t.Fatalf("foreign/invalid evidence wrote output: %v %q", err, output.Bytes())
		}
	}
}
