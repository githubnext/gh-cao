package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeCanonicalActivityOracle(t *testing.T) {
	for _, name := range []string{"unified-telemetry-session.jsonl", "evaluation-session.jsonl"} {
		t.Run(name, func(t *testing.T) {
			raw := readSessionFixture(t, name)
			session, _, err := ProjectSession(t.Context(), raw, "usage/aw_session.jsonl", testIdentity(), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			evidence := Evidence{Version: 1, Identity: testIdentity(), Run: map[string]any{
				"organization": "octo", "repository": "octo/api", "run_id": 303, "run_attempt": 1,
				"workflow_name": "Test", "workflow_path": ".github/workflows/test.lock.yml",
				"status": "completed", "conclusion": "success", "created_at": "2026-10-15T10:00:00Z",
				"started_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T11:00:00Z", "cao_unified_session": session,
			}}
			input, _, err := evidence.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			expected := oracle(t, `
import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from './dashboard/site/src/data/normalize/index.js';
let content=''; for await (const chunk of process.stdin) content += chunk;
const evidence = JSON.parse(content);
const batch = normalize(adaptCachedGhAwJsonl(JSON.stringify({schema_version:5,kind:'run',run:evidence.run})).observations);
for (const run of batch.runs) for (const key of Object.keys(run)) if (key.startsWith('routing')) delete run[key];
for (const [collection, rows] of Object.entries(batch)) {
 if (!rows.length) delete batch[collection];
 for (const row of rows) delete row.sequence;
 rows.sort((a,b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}
console.log(JSON.stringify(batch));
`, input)
			var want map[string]any
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
			if err != nil {
				t.Fatal(err)
			}
			actual := comparable(t, projection.Collections).(map[string]any)
			for collection, rows := range want {
				if !reflect.DeepEqual(actual[collection], rows) {
					reportProjectionDifference(t, collection, actual[collection], rows)
				}
			}
			for collection := range actual {
				if want[collection] == nil {
					t.Errorf("unexpected collection %s", collection)
				}
			}
		})
	}
}

func reportProjectionDifference(t *testing.T, collection string, actual, expected any) {
	t.Helper()
	got := map[string]map[string]any{}
	if actual != nil {
		for _, row := range actual.([]any) {
			value := row.(map[string]any)
			got[value["id"].(string)] = value
		}
	}
	for _, row := range expected.([]any) {
		want := row.(map[string]any)
		id := want["id"].(string)
		value := got[id]
		if value == nil {
			t.Errorf("%s missing %s", collection, id)
			continue
		}
		for key, expected := range want {
			if observed, exists := value[key]; !exists || !reflect.DeepEqual(observed, expected) {
				t.Errorf("%s %s.%s: got=%v want=%v", collection, id, key, observed, expected)
			}
			delete(value, key)
		}
		for key, observed := range value {
			t.Errorf("%s %s.%s unexpected=%v", collection, id, key, observed)
		}
		delete(got, id)
	}
	for id := range got {
		t.Errorf("%s unexpected %s", collection, id)
	}
}

func TestNativeCanonicalContractMatchesAuthoritativeDeclarations(t *testing.T) {
	contract, err := CanonicalContract()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Queries) == 0 || len(contract.RoutingFields) == 0 {
		t.Fatal("empty native canonical contract")
	}
	oracle(t, `process.argv.push('--check'); await import('./scripts/generate-native-collection.mjs');`, nil)
}

func TestNativeCanonicalRejectsOwnershipAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProjectCanonical(ctx, Evidence{}, time.Now(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled projection did not stop before reading evidence")
	}
	evidence := Evidence{Version: 1, Identity: testIdentity(), Run: map[string]any{"repository": "elsewhere/api"}}
	if _, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{}); err == nil {
		t.Fatal("foreign safe evidence accepted")
	}

	for _, value := range []string{"github:run:octo/api:303", "303"} {
		p := Projection{Identity: testIdentity(), RunID: "github:run:octo/api:303"}
		if !p.evalIdentity(map[string]any{"runId": value}) {
			t.Fatalf("canonical/native run ID rejected: %s", value)
		}
	}
	if p := (Projection{Identity: testIdentity(), RunID: "github:run:octo/api:303"}); p.evalIdentity(map[string]any{"runId": "304"}) {
		t.Fatal("foreign eval identity accepted")
	}
	if strings.Contains(string(projectionContract), "PRIVATE") {
		t.Fatal("generated query contract contains fixture evidence")
	}
}

func TestNativeCanonicalUnavailableCoverageAndBounds(t *testing.T) {
	evidence := Evidence{Version: 1, Identity: testIdentity(), Run: map[string]any{
		"repository": "octo/api", "run_id": 303, "run_attempt": 1, "workflow_path": ".github/workflows/test.lock.yml",
	}, Gaps: []string{"session-unavailable", "legacy-audit-unavailable"}}
	published := time.Now().UTC()
	projection, err := ProjectCanonical(t.Context(), evidence, published, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	gaps := 0
	for _, row := range projection.Collections["audits"] {
		if row["source"] != "cao-native" {
			continue
		}
		gaps++
		if row["timestamp"] != nil || row["observedAt"] == nil || row["status"] != "unavailable" {
			t.Fatal("missing source was hidden or gained an invented event clock")
		}
	}
	if gaps != 2 {
		t.Fatal("canonical consumer lost acquisition coverage gaps")
	}
	if _, err := ProjectCanonical(t.Context(), evidence, published, Limits{Entries: 1}); !errors.Is(err, ErrCapacity) {
		t.Fatal("canonical fanout exceeded its explicit row ceiling")
	}
	evidence.Gaps = []string{"upstream raw content"}
	if _, err := ProjectCanonical(t.Context(), evidence, published, Limits{}); err == nil {
		t.Fatal("unclassified source content accepted as a coverage code")
	}
}
