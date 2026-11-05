package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func modelAuditFiles(t *testing.T) map[string][]byte {
	t.Helper()
	// github/gh-aw@v1.0.1 pkg/cli/testdata/model_resolution_startup/39499464895.
	// Session and stdio are verbatim; reflection is whitespace-only reformatted
	// from the same authoritative run.
	return map[string][]byte{
		"usage/aw_session.jsonl":            jobFixture(t, "gh-aw-v1.0.1-model-resolution.jsonl"),
		"agent/agent-stdio.log":             jobFixture(t, "gh-aw-v1.0.1-model-stdio.log"),
		"sandbox/firewall/awf-reflect.json": jobFixture(t, "gh-aw-v1.0.1-model-reflection.json"),
	}
}

func modelAuditCodes(findings []any) []string {
	codes := make([]string, 0, len(findings))
	for _, item := range findings {
		codes = append(codes, text(object(item)["code"]))
	}
	return codes
}

func TestNativeModelAuditPinnedStartupAndCanonicalOracle(t *testing.T) {
	files := fixtureFiles(t)
	for location, content := range modelAuditFiles(t) {
		// All sources are bound to this run by the artifact's aw_info.json.
		files[location] = content
	}
	delete(files, "usage/aw_session.jsonl")
	files["aw_session.jsonl"] = jobFixture(t, "gh-aw-v1.0.1-model-resolution.jsonl")
	run := nativeRun()
	run.Fields["conclusion"] = "failure"
	c := Collector{Provider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: files}}},
		Store: &testEvidenceStore{}}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	evidence, err := c.extract(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	findings := array(object(evidence.Run["audit"])["key_findings"])
	if !slices.Equal(modelAuditCodes(findings), []string{"workflow_failed", "model_unavailable"}) {
		t.Fatalf("pinned upstream causal finding missing: %s", mustJSON(t, findings))
	}
	model := object(findings[1])
	if model["severity"] != "high" || model["category"] != "configuration" ||
		!strings.Contains(text(model["description"]), "mini-whoami") ||
		!strings.Contains(text(model["description"]), "gpt-5.4-mini") {
		t.Fatal("upstream model finding classification/identifiers changed")
	}
	if slices.Contains(evidence.Gaps, "model-resolution-audit-unavailable") ||
		!slices.Contains(evidence.Gaps, "legacy-audit-unavailable") {
		t.Fatal("supported model rule was dropped or full parity inferred")
	}
	projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Collections["runs"][0]["highPriorityAuditItems"] != nil {
		t.Fatal("supported subset implied complete audit totals")
	}
	// Restore Node only for the independent, test-only canonical Activity oracle.
	t.Setenv("PATH", originalPath)
	want := oracle(t, `
import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from './dashboard/site/src/data/normalize/index.js';
let raw=''; for await (const chunk of process.stdin) raw+=chunk;
const run=JSON.parse(raw);
// Expected titles, codes and priorities are pinned upstream test assertions,
// not values produced by the native finding implementation.
run.audit={key_findings:[
 {code:'workflow_failed',category:'error',severity:'critical',title:'Workflow Failed'},
 {code:'model_unavailable',category:'configuration',severity:'high',title:'Model Unavailable'}
], recommendations:[{priority:'high',action:'Replace the unavailable model pin with a model advertised by the same provider, verify endpoint compatibility, and recompile the workflow'}]};
const audits=normalize(adaptCachedGhAwJsonl(JSON.stringify({schema_version:5,kind:'run',run})).observations).audits
 .filter(row=>['audit.finding','audit.recommendation'].includes(row.type));
for(const row of audits) delete row.sequence;
console.log(JSON.stringify(audits.sort((a,b)=>a.id.localeCompare(b.id))));`, mustJSON(t, evidence.Run))
	native := []any{}
	for _, row := range projection.Collections["audits"] {
		if row["type"] == "audit.finding" || row["type"] == "audit.recommendation" {
			native = append(native, comparable(t, row))
		}
	}
	slices.SortFunc(native, func(a, b any) int { return strings.Compare(text(object(a)["id"]), text(object(b)["id"])) })
	var expected []any
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(native, expected) {
		t.Fatalf("model canonical projection differs from Activity:\n%s\n%s", mustJSON(t, native), want)
	}
}

func TestNativeModelAuditPinnedCounterEvidence(t *testing.T) {
	for _, test := range []struct {
		name, conclusion, code string
		change                 func(map[string][]byte)
	}{
		{"confirmed", "failure", "model_unavailable", nil},
		{"success-advisory", "success", "model_resolution_failed", nil},
		{"missing-stdio", "failure", "model_resolution_failed", func(f map[string][]byte) { delete(f, "agent/agent-stdio.log") }},
		{"optional-warning", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["agent/agent-stdio.log"] = []byte("optional repository agent mini-whoami unavailable; unrelated startup failure\n")
		}},
		{"other-role-stop", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["agent/agent-stdio.log"] = bytes.ReplaceAll(f["agent/agent-stdio.log"], []byte("sub-agent 'mini-whoami'"), []byte("sub-agent 'other'"))
		}},
		{"missing-reflection", "failure", "model_availability_unknown", func(f map[string][]byte) { delete(f, "sandbox/firewall/awf-reflect.json") }},
		{"incomplete-reflection", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["sandbox/firewall/awf-reflect.json"] = []byte(`{"models_fetch_complete":false}`)
		}},
		{"advertised-contradiction", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["sandbox/firewall/awf-reflect.json"] = []byte(`{"models_fetch_complete":true,"endpoints":[{"provider":"copilot","configured":true,"models":["gpt-5.4-mini"]}]}`)
		}},
		{"ambiguous-endpoints", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["sandbox/firewall/awf-reflect.json"] = []byte(`{"models_fetch_complete":true,"endpoints":[{"provider":"copilot","configured":true,"models":[]},{"provider":"github-copilot","configured":true,"models":[]}]}`)
		}},
		{"unsafe-inventory", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["sandbox/firewall/awf-reflect.json"] = []byte(`{"models_fetch_complete":true,"endpoints":[{"provider":"copilot","configured":true,"models":["PRIVATE INVENTORY"]}]}`)
		}},
		{"invalid-inventory-type", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["sandbox/firewall/awf-reflect.json"] = []byte(`{"models_fetch_complete":true,"endpoints":["bad",{"provider":"copilot","configured":true,"models":[]}]}`)
		}},
		{"other-cause-preserved", "failure", "model_availability_unknown", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"cause":"model_unavailable"`), []byte(`"cause":"catalog_unavailable"`))
		}},
		{"unsupported-endpoint", "failure", "model_endpoint_unsupported", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"cause":"model_unavailable"`), []byte(`"cause":"unsupported_endpoint"`))
		}},
		{"zero-exit", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"exitCode":1`), []byte(`"exitCode":0`))
		}},
		{"string-exit", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"exitCode":1`), []byte(`"exitCode":"1"`))
		}},
		{"fractional-exit", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"exitCode":1`), []byte(`"exitCode":1.0`))
		}},
		{"invalid-execution-categories", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = bytes.ReplaceAll(f["usage/aw_session.jsonl"], []byte(`"categories":["model_not_supported_error"]`), []byte(`"categories":"bad"`))
		}},
		{"native-continuation", "failure", "model_resolution_failed", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = append(f["usage/aw_session.jsonl"], []byte("{\"type\":\"session.init\",\"data\":{},\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"}}\n")...)
		}},
		{"harness-is-not-continuation", "failure", "model_unavailable", func(f map[string][]byte) {
			f["usage/aw_session.jsonl"] = append(f["usage/aw_session.jsonl"], []byte("{\"type\":\"session.start\",\"data\":{},\"provenance\":{\"component\":\"harness\",\"phase\":\"agent\"}}\n")...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := modelAuditFiles(t)
			if test.change != nil {
				test.change(files)
			}
			findings, recommendations, _, err := modelAuditFindings(t.Context(), map[string]any{"conclusion": test.conclusion}, files, testIdentity(), Limits{})
			if err != nil || !slices.Equal(modelAuditCodes(findings), []string{test.code}) {
				t.Fatalf("counter-evidence attribution changed: %s %v", mustJSON(t, findings), err)
			}
			retained := mustJSON(t, []any{findings, recommendations})
			if bytes.Contains(retained, []byte("PRIVATE")) {
				t.Fatal("raw inventory or log content persisted")
			}
			if test.code == "model_resolution_failed" && len(recommendations) != 0 {
				t.Fatal("advisory incorrectly recommended model replacement")
			}
		})
	}
}

func TestNativeModelAuditInvalidAtomicAndBounded(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"malformed-tail", func(b []byte) []byte { return append(b, '{') }},
		{"wrong-attempt", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"role":"mini-whoami"`), []byte(`"role":"mini-whoami","run_attempt":2`), 1)
		}},
		{"wrong-provenance", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"component":"model_resolution"`), []byte(`"component":"agent"`), 1)
		}},
		{"resolved-failure", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"resolved_model":null`), []byte(`"resolved_model":"model"`), 1)
		}},
		{"duplicate-execution", func(b []byte) []byte {
			return append(b, []byte("{\"type\":\"agent.execution\",\"data\":{\"exitCode\":1},\"provenance\":{\"component\":\"execution\",\"phase\":\"agent\"}}\n")...)
		}},
		{"invalid-header", func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1) }},
		{"string-header-version", func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":"1"`), 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := modelAuditFiles(t)
			files["usage/aw_session.jsonl"] = test.edit(files["usage/aw_session.jsonl"])
			findings, _, gaps, err := modelAuditFindings(t.Context(), map[string]any{"conclusion": "failure"}, files, testIdentity(), Limits{})
			if err != nil || len(findings) != 0 || !slices.Contains(gaps, "invalid-model-resolution-audit") {
				t.Fatalf("invalid source partially attributed: %s %v %v", mustJSON(t, findings), gaps, err)
			}
		})
	}
	for _, limit := range []Limits{{Bytes: 10}, {LineBytes: 10}, {Entries: 1}} {
		if _, _, _, err := modelAuditFindings(t.Context(), nil, modelAuditFiles(t), testIdentity(), limit); !errors.Is(err, ErrCapacity) {
			t.Fatalf("audit resource budget ignored: %+v %v", limit, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := modelAuditFindings(ctx, nil, modelAuditFiles(t), testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("model audit ignored cancellation")
	}
}

func TestNativeModelAuditLegacyPiAndInventoryAliases(t *testing.T) {
	for _, header := range []string{"", "{\"type\":\"session.format\",\"data\":{\"version\":1},\"provenance\":{\"component\":\"collector\"}}\n"} {
		files := map[string][]byte{
			"agent/agent-stdio.log":             []byte("[gh-aw/pi-models-json] fatal: Pi model \"github-copilot/gpt-5.4-mini?effort=high\" is not advertised by the configured copilot proxy endpoint;\n"),
			"sandbox/firewall/awf-reflect.json": jobFixture(t, "gh-aw-v1.0.1-model-reflection.json"),
		}

		if header != "" {
			files["usage/aw_session.jsonl"] = []byte(header)
		}
		findings, _, _, err := modelAuditFindings(t.Context(), map[string]any{"conclusion": "failure"}, files, testIdentity(), Limits{})
		if err != nil || !slices.Equal(modelAuditCodes(findings), []string{"model_unavailable"}) {
			t.Fatalf("legacy Pi compatibility missing: %s %v", mustJSON(t, findings), err)
		}
	}
	for _, pair := range [][2]string{{"copilot-responses-2", "github"}, {"codex", "openai"}, {"gemini", "google"}} {
		if normalizedModelProvider(pair[0]) != normalizedModelProvider(pair[1]) {
			t.Fatalf("pinned provider aliases differ: %v", pair)
		}
	}
}

func TestGitHubNativeModelAuditUsesExactAttemptAgentArchive(t *testing.T) {
	for _, otherAttempt := range []bool{false, true} {
		t.Run(fmt.Sprint(otherAttempt), func(t *testing.T) {
			usage := fixtureFiles(t)
			usage["aw_session.jsonl"] = jobFixture(t, "gh-aw-v1.0.1-model-resolution.jsonl")
			info := usage["aw_info.json"]
			if otherAttempt {
				info = bytes.ReplaceAll(info, []byte(`"run_attempt":"1"`), []byte(`"run_attempt":"2"`))
			}
			// This is the pinned unified agent upload layout. The sibling runtime
			// identity binds both ephemeral stdio and reflection to this attempt.
			agent := map[string][]byte{
				"runtime/aw_info.json": info,
				"agent-stdio.log": append(jobFixture(t, "gh-aw-v1.0.1-model-stdio.log"),
					[]byte("PRIVATE_STDOUT PRIVATE_TOKEN\n")...),
				"sandbox/firewall/awf-reflect.json": jobFixture(t, "gh-aw-v1.0.1-model-reflection.json"),
				"other.log":                         []byte("PRIVATE_UNSUPPORTED_LOG"),
			}
			archives := map[string][]byte{"100": zipArtifact(t, usage), "101": zipArtifact(t, agent)}
			downloaded := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-installation-token" {
					t.Error("model audit acquisition lost installation scoping")
				}
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4900")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
				switch r.URL.Path {
				case "/repos/octo/api/actions/runs/303/attempts/1":
					_, _ = w.Write([]byte(`{"id":303,"run_attempt":1,"path":".github/workflows/test.lock.yml","repository":{"full_name":"octo/api"},"status":"completed","conclusion":"failure"}`))
				case "/repos/octo/api/actions/runs/303/artifacts":
					_, _ = w.Write([]byte(`{"artifacts":[
								{"id":100,"name":"usage","workflow_run":{"id":303}},
								{"id":101,"name":"agent","workflow_run":{"id":303}}]}`))
				case "/repos/octo/api/actions/artifacts/100/zip", "/repos/octo/api/actions/artifacts/101/zip":
					downloaded++
					parts := strings.Split(r.URL.Path, "/")
					_, _ = w.Write(archives[parts[len(parts)-2]])
				case "/repos/octo/api/actions/runs/303/attempts/1/jobs":
					_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
				default:
					t.Errorf("unexpected acquisition endpoint %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer api.Close()
			store := &testEvidenceStore{}
			c := Collector{Provider: GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}, Store: store}
			t.Setenv("PATH", "/nonexistent")
			if err := c.CollectAttempt(t.Context(), testIdentity()); err != nil {
				t.Fatal(err)
			}
			if downloaded != 2 || len(store.published) != 1 {
				t.Fatal("model parser was not wired through real native archive acquisition")
			}
			evidence := store.published[0]
			want := "model_unavailable"
			if otherAttempt {
				want = "model_resolution_failed"
				if !slices.Contains(evidence.Gaps, "artifact-other-attempt") {
					t.Fatal("cross-attempt source loss was hidden")
				}
			}
			if !slices.Equal(modelAuditCodes(array(object(evidence.Run["audit"])["key_findings"])), []string{"workflow_failed", want}) {
				t.Fatalf("real source acquisition lost causal audit: %s", mustJSON(t, evidence.Run["audit"]))
			}
			raw, _, _ := evidence.Bytes()
			if bytes.Contains(raw, []byte("PRIVATE")) {
				t.Fatal("ephemeral engine/debug source leaked into durable evidence")
			}
			if _, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeModelAuditAmbiguityGapsAndWorkBudget(t *testing.T) {
	for _, name := range []string{"aw_session.jsonl", "agent-stdio.log", "awf-reflect.json"} {
		t.Run(name, func(t *testing.T) {
			files := modelAuditFiles(t)
			source := candidates(files, name)[0]
			files["other/"+name] = files[source]
			findings, _, gaps, err := modelAuditFindings(t.Context(), map[string]any{"conclusion": "failure"}, files, testIdentity(), Limits{})
			if err != nil || len(gaps) == 0 {
				t.Fatalf("ambiguous causal evidence was hidden: %v %v", gaps, err)
			}
			for _, gap := range gaps {
				if !gapCode.MatchString(gap) {
					t.Fatalf("non-canonical model coverage classification %q", gap)
				}
			}
			if slices.Contains(modelAuditCodes(findings), "model_unavailable") {
				t.Fatal("ambiguous evidence established model absence")
			}
		})
	}
	files := modelAuditFiles(t)
	delete(files, "sandbox/firewall/awf-reflect.json")
	files["agent/agent-stdio.log"] = []byte(strings.Repeat("unrelated log line\n", 8))
	raw := files["usage/aw_session.jsonl"]
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"type":"model.resolution"`)) {
			continue
		}
		for i := range 4 {
			record := bytes.ReplaceAll(line, []byte("mini-whoami"), []byte(fmt.Sprintf("role-%d", i)))
			files["usage/aw_session.jsonl"] = append(files["usage/aw_session.jsonl"], append(record, '\n')...)
		}
		break
	}
	if _, _, _, err := modelAuditFindings(t.Context(), map[string]any{"conclusion": "failure"}, files, testIdentity(), Limits{Entries: 20}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("role/log Cartesian work was not bounded: %v", err)
	}
}
