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
	ID             string           `json:"id"`
	Component      AnalyticalResult `json:"component"`
	ComponentQuery json.RawMessage  `json:"componentQuery"`
	FullQuery      json.RawMessage  `json:"fullQuery"`
	FullError      string           `json:"fullError"`
	Blocker        string           `json:"blocker"`
}

type graderEvidenceReplay struct {
	Name          string              `json:"name"`
	Original      []json.RawMessage   `json:"original"`
	Reconstructed []json.RawMessage   `json:"reconstructed"`
	Omissions     []string            `json:"omissions"`
	Queries       []graderQueryReplay `json:"queries"`
	SchemaVersion int                 `json:"schemaVersion"`
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
			now := time.Now().UTC()
			p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", now.Format(time.DateOnly), 7, []byte(scenario.raw), now)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := NativeParquet(t.Context(), p, 128<<20); err != nil {
				t.Fatal(err)
			}
			a := openTestArchive(t, c, nil)
			if err := a.Admit(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			s := pinTestArchive(t, a)
			restored, err := s.Timeline(t.Context(), TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 7, Limit: 100})
			if err != nil || !reflect.DeepEqual(p.Events, restored) {
				t.Fatalf("original evidence changed across real Parquet publication: %v", err)
			}
			result := graderEvidenceReplay{Name: scenario.name, SchemaVersion: ContractVersion}
			for i, event := range p.Events {
				result.Original = append(result.Original, json.RawMessage(event.PayloadJSON))
				result.Reconstructed = append(result.Reconstructed, json.RawMessage(restored[i].PayloadJSON))
				result.Omissions = append(result.Omissions, event.OmissionReasons...)
			}
			slices.Sort(result.Omissions)
			result.Omissions = slices.Compact(result.Omissions)
			for _, attempt := range attempts {
				component, full := replayQuery(t, attempt.ID+"-component", attempt.Component), replayQuery(t, attempt.ID+"-full", attempt.Full)
				var definition Definition
				if err := json.Unmarshal(component, &definition); err != nil {
					t.Fatal(err)
				}
				out, err := s.Analytical(t.Context(), definition)
				if err != nil {
					t.Fatalf("%s scalar component execution failed: %v", attempt.ID, err)
				}
				err = json.Unmarshal(full, &definition)
				if err == nil {
					_, err = s.Analytical(t.Context(), definition)
				}
				if err == nil {
					t.Fatalf("%s attempted full metric unexpectedly accepted", attempt.ID)
				}
				if !errors.Is(err, ErrQuery) && !strings.Contains(err.Error(), "unknown field") {
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
