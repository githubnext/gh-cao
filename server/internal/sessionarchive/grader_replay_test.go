package sessionarchive

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type graderQueryAttempt struct {
	ID        string          `json:"id"`
	Component json.RawMessage `json:"component"`
	Full      json.RawMessage `json:"full"`
	Blocker   string          `json:"blocker"`
}

type graderQueryReplay struct {
	ID             string            `json:"id"`
	Component      AnalyticalResult  `json:"component"`
	ComponentQuery json.RawMessage   `json:"componentQuery"`
	FullQuery      json.RawMessage   `json:"fullQuery"`
	FullError      string            `json:"fullError"`
	FullResult     *AnalyticalResult `json:"fullResult,omitempty"`
	Blocker        string            `json:"blocker"`
}

type graderEvidenceReplay struct {
	Name          string              `json:"name"`
	Original      []json.RawMessage   `json:"original"`
	Reconstructed []json.RawMessage   `json:"reconstructed"`
	Omissions     []string            `json:"omissions"`
	Queries       []graderQueryReplay `json:"queries"`
	SchemaVersion int                 `json:"schemaVersion"`
	Companion     []json.RawMessage   `json:"companion"`
	Events        []Event             `json:"events"`
}

func replayQuery(t *testing.T, id string, clauses json.RawMessage) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(clauses, &fields); err != nil {
		t.Fatal(err)
	}
	fields["name"], fields["subject"], fields["from"] = json.RawMessage(`"`+id+`"`),
		json.RawMessage(`"Replay retained approved grader inputs."`), json.RawMessage(`"archive-events"`)
	content, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestGraderReplayEvidence(t *testing.T) {
	measured, err := os.ReadFile("testdata/evidence-measured.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("testdata/evidence.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("testdata/grader-query-attempts.json")
	if err != nil {
		t.Fatal(err)
	}
	var attempts []graderQueryAttempt
	if err := decode(content, &attempts); err != nil {
		t.Fatal(err)
	}
	header := "{\"type\":\"session.format\",\"provenance\":{},\"data\":{\"version\":1}}\n"
	var replays []graderEvidenceReplay
	for _, scenario := range []struct{ name, raw string }{
		{"measured", string(measured)},
		{"original-preservation-fixture", string(original)},
		{"preservation-valid-envelope", strings.ReplaceAll(strings.ReplaceAll(string(original), `"type":"session.format","data":`, `"type":"session.format","provenance":{},"data":`), `"type":"session.collection","data":`, `"type":"session.collection","provenance":{},"data":`)},
		{"no-inputs", header},
		{"usage-only-zero", header + "{\"type\":\"usage.report\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"usage\":{\"inputTokens\":0,\"outputTokens\":0},\"durationMs\":2.5}}\n"},
		{"invalid-measurements-fallback", header + "{\"type\":\"firewall.token_usage\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"usage\":{\"inputTokens\":1.5,\"outputTokens\":null},\"durationMs\":null}}\n" +
			"{\"type\":\"execution.result\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"durationMs\":3.75}}\n"},
		{"partial-usage", header + "{\"type\":\"firewall.token_usage\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"usage\":{\"inputTokens\":100,\"outputTokens\":20},\"durationMs\":0.5}}\n" +
			"{\"type\":\"firewall.token_usage\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"usage\":{\"inputTokens\":null,\"outputTokens\":1},\"durationMs\":1.25}}\n"},
		{"valid-input-missing-output", header + "{\"type\":\"firewall.token_usage\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"usage\":{\"inputTokens\":100,\"outputTokens\":null},\"durationMs\":0.25}}\n"},
		{"incomplete-tools", header + "{\"type\":\"tool.execution_start\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"toolName\":\"read\",\"toolCallId\":\"incomplete\"}}\n" +
			"{\"type\":\"mcp.rpc.request\",\"provenance\":{\"component\":\"gateway\",\"phase\":\"agent\"},\"data\":{\"toolName\":\"gateway\",\"serverName\":\"files\",\"requestId\":0,\"method\":\"tools/call\"}}\n"},
		{"invalid-tool-status", header + "{\"type\":\"tool.execution_complete\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"toolName\":\"read\",\"success\":\"false\",\"isError\":0,\"error\":false}}\n"},
		{"malformed-latest-ir", string(measured) + "{\"type\":\"trajectory.ir\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"objectives\":[{\"id\":\"invalid\",\"satisfiedAtEventIndex\":0.5}],\"actions\":[{\"id\":\"unknown\",\"validAtIssueTime\":null}],\"states\":{},\"events\":[]}}\n"},
		{"excluded-fields", string(measured)},
		{"redacted-descriptions", string(measured)},
		{"invalid-proxy-valid-report", header + usageEvent("firewall.token_usage", `null`, `null`, `null`) + usageEvent("usage.report", `10`, `5`, `100.5`)},
		{"null-first", header + usageEvent("firewall.token_usage", `null`, `2`, `1`) + usageEvent("firewall.token_usage", `100`, `20`, `2`)},
		{"large-totals", header + usageEvent("firewall.token_usage", `9007199254740991`, `9007199254740991`, `1`) + usageEvent("firewall.token_usage", `9007199254740991`, `9007199254740991`, `2`)},
		{"rounded-wide-input", header + usageEvent("firewall.token_usage", `9007199254740991`, `0`, `1`) + usageEvent("firewall.token_usage", `2`, `0`, `2`)},
		{"unsafe-duration-fallback", header + usageEvent("firewall.token_usage", `1`, `1`, `9007199254740991`) + usageEvent("firewall.token_usage", `1`, `1`, `1`) +
			"{\"type\":\"execution.result\",\"provenance\":{\"phase\":\"agent\"},\"data\":{\"durationMs\":3.75}}\n"},
		{"finite-duration-overflow", header + usageEvent("firewall.token_usage", `1`, `1`, `1e308`) + usageEvent("firewall.token_usage", `1`, `1`, `1e308`)},
		{"zero-first-valid-later", header + usageEvent("firewall.token_usage", `0`, `0`, `0`) + usageEvent("firewall.token_usage", `1`, `1`, `0.25`)},
		{"redacted-measurements", header + usageEvent("firewall.token_usage", `"token-secret"`, `2`, `1`)},
		{"non-agent-only", "{\"type\":\"usage.report\",\"provenance\":{\"phase\":\"detection\"},\"data\":{\"usage\":{\"inputTokens\":10,\"outputTokens\":5},\"durationMs\":5}}\n"},
		{"combined-large-first", header + usageEvent("firewall.token_usage", `9007199254740991`, `9007199254740991`, `0.125`)},
		{"duration-sequential-rounding", header + usageEvent("firewall.token_usage", `1`, `1`, `9007199254740990`) +
			strings.Repeat(usageEvent("firewall.token_usage", `1`, `1`, `0.25`), 4)},
		{"chained-edge-cases", header + "{\"type\":\"trajectory.ir\",\"provenance\":{\"component\":\"agent\",\"phase\":\"agent\"},\"data\":{\"states\":[\"x\",\"\",\"x\",{\"id\":\"\"},{\"id\":5},{\"id\":\"x\"},null,{\"id\":\"y\"},\"\"]}}\n" +
			safeOutput("agent", "agent/log", `{"type":"create_issue"}`) + safeOutput("safe_outputs", "usage/aw_session.jsonl", `{"type":"create_issue","status":"failure"}`) +
			safeOutput("agent", "agent/log", `{"type":"add_comment"}`) + safeOutput("agent", "agent/log", `{"status":"success"}`) +
			safeOutput("agent", "agent/log", `{"type":5}`) + safeOutput("agent", "agent/log", `{}`) + safeOutput("agent", "agent/log", `{"type":""}`) +
			safeOutput("safe_outputs", "usage/aw_session.jsonl", `{"type":"","status":null}`) + safeOutput("agent", "agent/log", `{"type":"x","status":"pending"}`) +
			safeOutput("safe_outputs", "other.jsonl", `{"type":"push","status":"failure"}`) + safeOutput("agent", "agent/log", `{"type":"push"}`)},
		{"ir-measured", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":0},{"id":"b","dependsOn":["a"],"satisfiedAtEventIndex":2},{"id":"c","dependsOn":["a","b"],"satisfiedAtEventIndex":1}],`+
			`"events":[{"kind":"state_change","index":0,"ref":"A"},{"kind":"action","index":1},{"kind":"state_change","index":2,"ref":"B"},{"kind":"action","index":3},{"kind":"action","index":4},{"kind":"state_change","index":5,"ref":"C"},{"kind":"action","index":6},{"kind":"action","index":7}],`+
			`"states":[{"id":"A","firstEventIndex":0},{"id":"A","firstEventIndex":1},{"id":"B","firstEventIndex":2},{"id":"A","firstEventIndex":4},{"id":"A","firstEventIndex":3},{"id":"B","firstEventIndex":4},{"id":"C"},{"id":"A","firstEventIndex":7}]}`)},
		{"ir-unknown-dependency", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":0,"dependsOn":["zz"]},{"id":"b","satisfiedAtEventIndex":9}],`+
			`"events":[{"kind":"action","index":0},{"kind":"action","index":5}],`+
			`"states":[{"id":"A","firstEventIndex":0},{"id":"B","firstEventIndex":5},{"id":"A","firstEventIndex":0},{"id":"B","firstEventIndex":5}]}`)},
		{"ir-invalid-indexes", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":0,"dependsOn":"a"},{"id":"b","satisfiedAtEventIndex":0}],`+
			`"events":[{"kind":"x","index":0},{"kind":"y","index":"1"}],"states":["x","","x","y"]}`)},
		{"ir-uncompleted-null-order", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":null},{"id":"b","dependsOn":["a"]}],`+
			`"events":[{"kind":"x","index":0},{"kind":"y","index":1}],`+
			`"states":[{"id":"A","firstEventIndex":null},{"id":5,"firstEventIndex":1},{"id":"B"},{"id":"A"},{"id":"B","firstEventIndex":0}]}`)},
		{"ir-duplicate-ids-no-events", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":0},{"id":"a","satisfiedAtEventIndex":0}],`+
			`"states":[{"id":"A"},{"id":"B"},{"id":"A"},{"id":"B"}]}`)},
		{"ir-positional-no-recurrence", header + trajectoryIR(`{"objectives":[{"id":"a","satisfiedAtEventIndex":3},{"id":"b","dependsOn":["a"],"satisfiedAtEventIndex":3}],`+
			`"events":[{"kind":"x","index":0},{"kind":"y","index":1},{"kind":"y","index":2},{"kind":"y","index":3}],`+
			`"states":[{"id":"A","firstEventIndex":3},{"id":"B","firstEventIndex":2},"C",{"id":"D","firstEventIndex":0}]}`)},
		{"ir-few-visits", header + trajectoryIR(`{"objectives":[{"id":"a","dependsOn":[],"satisfiedAtEventIndex":-1}],`+
			`"events":[{"index":0},{"index":1}],"states":["A",{"id":""},{"id":"B","firstEventIndex":1},"A",null]}`)},
		{"ordered-execution-fallback", header + usageEvent("firewall.token_usage", `1`, `1`, `null`) +
			"{\"type\":\"execution.result\",\"provenance\":{\"phase\":\"agent\"},\"data\":{\"durationMs\":null}}\n" +
			"{\"type\":\"execution.result\",\"provenance\":{\"phase\":\"agent\",\"timestampMs\":500},\"data\":{\"durationMs\":3.75}}\n" +
			"{\"type\":\"execution.result\",\"provenance\":{\"phase\":\"agent\",\"timestampMs\":100},\"data\":{\"durationMs\":0.25}}\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c := testConfig(t)
			if err := decode(policy, &c.Policy); err != nil {
				t.Fatal(err)
			}
			if scenario.name == "excluded-fields" {
				for event, fields := range c.Policy.Events {
					c.Policy.Events[event] = slices.DeleteFunc(fields, func(field string) bool {
						return slices.Contains([]string{"error", "usage", "objectives", "actions", "states", "events"}, field)
					})
				}
			}
			if scenario.name == "redacted-descriptions" {
				c.Policy.RedactPatterns = append(c.Policy.RedactPatterns, `(?i)verify|policy|approval`)
			}
			if scenario.name == "redacted-measurements" {
				c.Policy.RedactPatterns = append(c.Policy.RedactPatterns, `token-secret`)
			}
			now := time.Now().UTC()
			p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", now.Format(time.DateOnly), 7, []byte(scenario.raw), now)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := NativeParquet(t.Context(), p, 128<<20); err != nil {
				t.Fatal(err)
			}
			a := openTestArchive(t, c, nil)
			companionRaw := header + usageEvent("usage.report", `7`, `3`, `100.5`)
			companion, err := Project(c.traceConfig(), c.Namespace, "octo/api", "456", now.Format(time.DateOnly), 2, []byte(companionRaw), now)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Admit(t.Context(), companion); err != nil {
				t.Fatal(err)
			}
			if err := a.Admit(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			s := pinTestArchive(t, a)
			if scenario.name == "excluded-fields" {
				definition := analyticalDefinition(t, `"filter":{"predicates":[{"field":"InputTokens","state":"excluded"}]},"aggregate":{"values":[{"field":"Sequence","as":"n","reducer":"count"}]}`)
				out, err := s.Analytical(t.Context(), definition)
				if err != nil || out.Rows[0]["n"] != json.Number("4") {
					t.Fatalf("generated parent exclusion was not qualified: %#v %v", out, err)
				}
			}
			restored, err := s.Timeline(t.Context(), TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 7, Limit: 100})
			if err != nil || !reflect.DeepEqual(p.Events, restored) {
				t.Fatalf("original evidence changed across real Parquet publication: %v", err)
			}
			result := graderEvidenceReplay{Name: scenario.name, SchemaVersion: ContractVersion}
			result.Events = append(slices.Clone(p.Events), companion.Events...)
			for _, event := range companion.Events {
				result.Companion = append(result.Companion, json.RawMessage(event.PayloadJSON))
			}
			for i, event := range p.Events {
				result.Original = append(result.Original, json.RawMessage(event.PayloadJSON))
				result.Reconstructed = append(result.Reconstructed, json.RawMessage(restored[i].PayloadJSON))
				result.Omissions = append(result.Omissions, event.OmissionReasons...)
			}
			slices.Sort(result.Omissions)
			result.Omissions = slices.Compact(result.Omissions)
			for _, attempt := range attempts {
				component, full := replayQuery(t, attempt.ID+"-component", attempt.Component), replayQuery(t, attempt.ID+"-full", attempt.Full)
				supported := slices.Contains([]string{"execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth",
					"objective-coverage", "premature-termination-gap", "grounding-accuracy", "state-revisit-probability-rep",
					"recurrence-rate", "artifact-production", "dependency-order-violation-rate", "evidence-saturation-stopping-lag",
					"recurrence-laminarity", "recurrence-trapping-time"}, attempt.ID)
				if supported {
					full, err = os.ReadFile("testdata/graders/" + attempt.ID + ".json")
					if err != nil {
						t.Fatal(err)
					}
					var envelope map[string]any
					if json.Unmarshal(full, &envelope) != nil {
						t.Fatal("invalid reviewed definition")
					}
					request, _ := json.Marshal(map[string]any{"kind": "session-query", "repository": "githubnext/gh-cao", "language-version": "0.2.0", "query": envelope})
					if len(request) > 4096 {
						t.Fatalf("%s exceeds actual MCP/HTTP argument budget: %d", attempt.ID, len(request))
					}
				}
				var definition Definition
				if err := json.Unmarshal(component, &definition); err != nil {
					t.Fatal(err)
				}
				out, err := s.Analytical(t.Context(), definition)
				if err != nil {
					t.Fatalf("%s scalar component execution failed: %v", attempt.ID, err)
				}
				err = json.Unmarshal(full, &definition)
				var fullResult AnalyticalResult
				if err == nil {
					fullResult, err = s.Analytical(t.Context(), definition)
				}
				if supported {
					if err != nil {
						t.Fatalf("%s full declarative metric failed: %v", attempt.ID, err)
					}
					if len(fullResult.Rows) != 2 || fullResult.Metadata.QueryDigest == "" {
						t.Fatalf("lost complete trace binding or query provenance: %#v", fullResult)
					}
					for _, row := range fullResult.Rows {
						if row["availability"] != "measured" && row["availability"] != "unavailable" || row["reason"] == "" {
							t.Fatal("missing measurement qualification")
						}
						if _, family := row["family"]; family && row["RunId"] == "456" && row["family"] != "usage.report" {
							t.Fatal("proxy presence leaked across trace/run-attempt bindings")
						}
					}
					result.Queries = append(result.Queries, graderQueryReplay{ID: attempt.ID, Component: out, ComponentQuery: component, FullQuery: full, FullResult: &fullResult})
					continue
				}
				if err == nil {
					t.Fatalf("%s attempted full metric unexpectedly accepted", attempt.ID)
				}

				// Invented syntax fails strict decoding: unknown keys, or a shape that is
				// not the Dashboard Language compute/window array.
				var shape *json.UnmarshalTypeError
				if !errors.Is(err, ErrQuery) && !strings.Contains(err.Error(), "unknown field") && !errors.As(err, &shape) {
					t.Fatalf("unexpected full-query failure: %v", err)
				}
				result.Queries = append(result.Queries, graderQueryReplay{
					ID: attempt.ID, Component: out, ComponentQuery: component, FullQuery: full,
					FullError: err.Error(), Blocker: attempt.Blocker,
				})
			}
			replays = append(replays, result)
		})
	}
	if directory := os.Getenv("CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR"); directory != "" {
		encoded, err := json.Marshal(replays)
		if err != nil {
			t.Fatal(err)
		}
		// #nosec G703 -- The replay runner supplies its own private temporary output directory.
		if err := os.WriteFile(filepath.Join(directory, "evidence.json"), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func safeOutput(phase, path, data string) string {
	return `{"type":"safe_output.result","provenance":{"component":"` + phase + `","phase":"` + phase + `","path":"` + path + `"},"data":` + data + "}\n"
}

func usageEvent(family, input, output, duration string) string {
	return `{"type":"` + family + `","provenance":{"phase":"agent","component":"agent"},"data":{"usage":{"inputTokens":` + input + `,"outputTokens":` + output + `},"durationMs":` + duration + `}}` + "\n"
}

func trajectoryIR(data string) string {
	return `{"type":"trajectory.ir","provenance":{"component":"agent","phase":"agent","path":"agent/log"},"data":` + data + "}\n"
}
