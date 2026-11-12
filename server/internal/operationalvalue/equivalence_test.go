package operationalvalue

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const adapter = `
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const request = JSON.parse(Buffer.concat(chunks).toString());
console.log(JSON.stringify({
  kind: "operational_value_definition",
  workflowSlug: "example-worker",
  adoptedAt: "2026-09-15T23:30:36Z",
  evaluationMode: "baseline-comparable",
  cadenceDays: 1,
  repositories: ["githubnext/gh-cao"],
  valueIds: ["example-worker.current"]
}));
console.log(JSON.stringify({
  timestamp: request.timestamp,
  repository: request.repositories[0],
  valueId: "example-worker.current",
  value: 3,
  metricRole: "primary",
  metricName: "Current value",
  metricUnit: "items",
  cadenceDays: 2.5,
  metricDirection: "decrease",
  maturityStatus: "matured",
  adoptionAt: "2026-09-15T23:30:36Z",
  evaluationMode: "baseline-comparable",
  workflowSlug: "example-worker",
  workflowName: "Example Worker",
  windowStart: new Date(Date.parse(request.timestamp) - 7 * 86400000).toISOString(),
  windowEnd: request.timestamp,
  measurementKind: "interval",
  coverageStatus: "unknown",
  missingReason: "source-window-coverage-unverified",
  eligibleCount: 2,
  populationDigest: "a".repeat(64),
  evidenceDigest: "b".repeat(64),
  rollupNumerator: 3,
  rollupDenominator: 1
}));
`

func TestPublicationMatchesActivityCollector(t *testing.T) {
	compareActivityCollector(t, adapter)
}

func TestUnavailablePeriodsMatchActivityCollector(t *testing.T) {
	script := strings.Replace(adapter, "value: 3,", "value: null,", 1)
	script = strings.Replace(script, `maturityStatus: "matured",`, `maturityStatus: "unavailable",`, 1)
	script = strings.Replace(script, "rollupNumerator: 3,", "", 1)
	script = strings.Replace(script, "rollupDenominator: 1", "", 1)
	compareActivityCollector(t, script)
}

func compareActivityCollector(t *testing.T, script string) {
	t.Helper()
	root := repositoryRoot(t)
	temporary := t.TempDir()
	packages := filepath.Join(temporary, "packages")
	campaign := filepath.Join(packages, "example")
	if err := os.MkdirAll(campaign, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(campaign, "operational-value.mjs"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeOutput := filepath.Join(temporary, "node.jsonl")
	goOutput := filepath.Join(temporary, "go.jsonl")
	for _, output := range []string{nodeOutput, goOutput} {
		writeRetainedFixture(t, output)
	}
	observedAt, err := time.Parse(time.RFC3339, "2026-09-24T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext( // #nosec G204 -- fixed Node binary and test-owned temporary paths.
		t.Context(),
		"node", filepath.Join(root, "activity", "cao.mjs"),
		"operational-value",
		"--database", filepath.Join(temporary, "activity.sqlite"),
		"--root", packages,
		"--output", nodeOutput,
		"--timestamp", observedAt.Format(time.RFC3339),
		"--repository", "githubnext/gh-cao",
		"--retention-days", "30",
	)
	node.Dir = root
	node.Env = testEnvironment()
	if output, err := node.CombinedOutput(); err != nil {
		t.Fatalf("Activity collector failed: %v\n%s", err, output)
	}

	// Execute the fixture only in this test. Production Go retains the
	// publication contract, not the Activity adapter execution lifecycle.
	fixture := exec.CommandContext(t.Context(), "node", filepath.Join(campaign, "operational-value.mjs")) // #nosec G204 -- fixed Node binary and test-owned fixture.
	fixture.Env = testEnvironment()
	request, err := json.Marshal(map[string]any{
		"timestamp": formatTimestamp(observedAt), "repositories": []string{"githubnext/gh-cao"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.Stdin = bytes.NewReader(request)
	content, err := fixture.Output()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOutput(content, "fixture", []string{"githubnext/gh-cao"})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := readRetained(goOutput, observedAt.Add(-30*day))
	if err != nil {
		t.Fatal(err)
	}
	active := map[string]map[string]struct{}{"example": {}}
	definitions := map[string]definition{}
	for _, item := range parsed.definitions {
		for _, id := range item.ValueIDs {
			active["example"][id] = struct{}{}
			definitions[definitionKey("example", id)] = item
		}
	}
	fresh := testEnvelopes(parsed.values)
	merged := persistValues(retained, fresh, active, definitions)
	if err := writeEnvelopes(goOutput, merged); err != nil {
		t.Fatal(err)
	}
	nodeRecords := readJSONL(t, nodeOutput)
	goRecords := readJSONL(t, goOutput)
	if len(goRecords) != 3 {
		t.Fatalf("got %d records, want current, retained history, and unrelated evidence", len(goRecords))
	}
	if !reflect.DeepEqual(goRecords, nodeRecords) {
		t.Fatalf("Go and Activity publication differ:\nGo:   %#v\nNode: %#v", goRecords, nodeRecords)
	}

	if repeated := persistValues(merged, fresh, active, definitions); !reflect.DeepEqual(repeated, merged) {
		t.Fatal("repeated publication changed retained history")
	}
}

func writeRetainedFixture(t *testing.T, path string) {
	t.Helper()
	content := `{"schema_version":2,"kind":"operational_value","operational_value":{"timestamp":"2026-09-21T10:00:00.000Z","repository":"githubnext/gh-cao","campaign":"example","campaign_id":"campaign:example","value_id":"example-worker.retired","value":1,"metric_role":"primary","metric_name":"retired","metric_direction":"increase","maturity_status":"matured"}}
{"schema_version":2,"kind":"operational_value","operational_value":{"timestamp":"2026-09-21T10:00:00.000Z","repository":"githubnext/gh-cao","campaign":"unrelated","campaign_id":"campaign:unrelated","value_id":"preserved","value":4,"metric_role":"primary","metric_name":"preserved","metric_direction":"increase","maturity_status":"matured"}}
{"schema_version":2,"kind":"operational_value","operational_value":{"timestamp":"2026-09-20T10:00:00.000Z","repository":"githubnext/gh-cao","campaign":"example","campaign_id":"campaign:example","value_id":"example-worker.current","value":2,"metric_role":"primary","metric_name":"Current value","metric_direction":"decrease","maturity_status":"matured"}}
{"schema_version":2,"kind":"operational_value","operational_value":{"timestamp":"2026-08-20T10:00:00.000Z","repository":"githubnext/gh-cao","campaign":"unrelated","campaign_id":"campaign:unrelated","value_id":"expired","value":1,"metric_role":"primary","metric_name":"expired","metric_direction":"increase","maturity_status":"matured"}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	records := make([]map[string]any, 0, bytes.Count(content, []byte{'\n'}))
	for _, line := range bytesLines(content) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func bytesLines(content []byte) [][]byte {
	var lines [][]byte
	start := 0
	for index, value := range content {
		if value == '\n' {
			if index > start {
				lines = append(lines, content[start:index])
			}
			start = index + 1
		}
	}
	return lines
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func testEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=" + os.Getenv("LANG"),
	}
}
