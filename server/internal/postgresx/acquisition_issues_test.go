package postgresx_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeAcquisitionSharedIssueConvergesAcrossRuns(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
			acquisition := store.Acquisition()
			identity := acquisitionIdentity()
			if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
				t.Fatal(err)
			}
			clock := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
			evidence := make([]collectnative.Evidence, 0, 3)
			for index := range 3 {
				id := identity
				id.RunID += int64(index)
				record := acquisitionEvidence(id)
				record.Run["created_at"] = clock.Add(time.Duration(index) * time.Minute).Format(time.RFC3339)
				// The last two observations intentionally have equal clocks:
				// source identity, not publication arrival, breaks this tie.
				observed := clock.Add(time.Duration(min(index, 1)) * time.Minute).Format(time.RFC3339)
				fields := map[string]any{"githubEntityType": "issue", "correlationId": "https://github.com/octo/api/issues/42",
					"issueState": "open", "issueClosed": false}
				switch index {
				case 0:
					fields["summary"] = "Known original evidence"
				case 2:
					fields["issueState"], fields["issueClosed"] = "closed", true
					fields["issueClosedAt"] = observed
				}
				record.Run["cao_unified_session"] = map[string]any{
					"version": 1, "path": "usage/aw_session.jsonl",
					"events": []any{map[string]any{"line": 1, "source": "safe-outputs", "type": "safe_output.result",
						"timestamp": observed, "fields": fields}},
				}
				evidence = append(evidence, record)
			}
			expected := nativeOracleBatch(t, evidence)["issues"]
			if len(expected) != 1 {
				t.Fatal("Activity oracle did not converge one shared issue")
			}
			t.Setenv("PATH", "/nonexistent")
			for position := range evidence {
				index := position
				if reverse {
					index = len(evidence) - 1 - position
				}
				publication := publishNative(t, acquisition, evidence[index])
				if _, err := acquisition.Activate(t.Context(), publication); err != nil {
					t.Fatal(err)
				}
			}
			fields := []string{"id", "runId", "state", "closed", "closedAt", "summary", "observedAt"}
			selected := make([]query.SelectedField, 0, len(fields))
			for _, field := range fields {
				selected = append(selected, query.SelectedField{Field: field})
			}
			result, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
				{Name: "issues", From: "$issues", Select: selected},
			}, []string{"issues"})
			if err != nil || len(result["issues"].Rows) != 1 {
				t.Fatalf("shared canonical issue did not converge: %v %+v", err, result["issues"].Rows)
			}
			for _, field := range fields {
				if got, want := result["issues"].Rows[0][field], expected[0][field]; !canonicalValuesEqual(got, want) {
					t.Errorf("shared issue %s: got=%v want=%v", field, got, want)
				}
			}
		})
	}
}

func nativeOracleBatch(t *testing.T, evidence []collectnative.Evidence) map[string][]map[string]any {
	t.Helper()
	content, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from './dashboard/site/src/data/normalize/index.js';
let content=''; for await (const chunk of process.stdin) content += chunk;
const lines=JSON.parse(content).map(e => JSON.stringify({schema_version:5,kind:'run',run:e.run})).join('\n');
console.log(JSON.stringify(normalize(adaptCachedGhAwJsonl(lines).observations)));`)
	command.Dir = "../../.."
	command.Stdin = bytes.NewReader(content)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("multi-run Activity oracle: %v %s", err, output)
	}
	var batch map[string][]map[string]any
	if err := json.Unmarshal(output, &batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

func canonicalValuesEqual(actual, expected any) bool {
	if a, ok := actual.(string); ok {
		if b, ok := expected.(string); ok {
			left, leftErr := time.Parse(time.RFC3339Nano, a)
			right, rightErr := time.Parse(time.RFC3339Nano, b)
			if leftErr == nil && rightErr == nil {
				return left.Equal(right)
			}
		}
	}
	a, actualErr := json.Marshal(actual)
	b, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && routingValuesEqual(a, b)
}
