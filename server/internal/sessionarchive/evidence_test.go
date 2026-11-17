package sessionarchive

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func originalEvidence(t *testing.T, c Config) Projection {
	t.Helper()
	raw, err := os.ReadFile("testdata/evidence.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil || decode(policy, &c.Policy) != nil {
		t.Fatal("invalid explicit synthetic approval")
	}
	p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", time.Now().UTC().Format(time.DateOnly), 7, raw, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func eventsOfType(p Projection, kind string) []Event {
	return slices.DeleteFunc(slices.Clone(p.Events), func(e Event) bool { return e.Type != kind })
}

func TestOriginalEvidenceFamilies(t *testing.T) {
	p := originalEvidence(t, nativeTestConfig(t))
	raw, err := os.ReadFile("testdata/evidence.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(p.Events) != len(lines) {
		t.Fatal("original observations were combined, thinned or deduplicated")
	}
	for i, e := range p.Events {
		var original map[string]any
		if err := decode(lines[i], &original); err != nil {
			t.Fatal(err)
		}
		if e.Sequence != int64(i) || e.Type != original["type"] || e.RunAttempt != 7 ||
			e.Repository != "octo/api" || e.ProjectionID != p.Version ||
			e.ArtifactDigest != hash(raw) || ValidateEvent(e) != nil {
			t.Fatalf("original order, identity, versions or typed evidence lost at %d", i)
		}
	}
	t.Run("tools-and-attempt-scopes", func(t *testing.T) {
		tools := eventsOfType(p, "tool.execution_complete")
		if len(tools) != 4 || tools[0].Success == nil || *tools[0].Success ||
			tools[0].IsError == nil || !*tools[0].IsError || tools[0].ProvenanceAttemptID != "startup:1:harness:1" ||
			tools[1].AgentID != "subagent" || tools[1].Success == nil || !*tools[1].Success ||
			!slices.Contains(tools[1].NullFields, "Error") || tools[2].Phase != "detection" ||
			tools[3].ProvenanceAttemptID != "startup:1:harness:2" ||
			tools[3].Success != nil || !slices.Contains(tools[3].InvalidFields, "Success") ||
			!slices.Contains(tools[3].PresentFields, "Error") || slices.Contains(tools[3].NullFields, "Error") {
			t.Fatal("failure observations, false/null/type states, phases or resumed scopes changed")
		}
		for _, e := range tools {
			if strings.Contains(e.PayloadJSON, `"output"`) || strings.Contains(e.PayloadJSON, `"input"`) {
				t.Fatal("new tool arguments or output bodies were captured")
			}
		}
		rpc := eventsOfType(p, "mcp.rpc.request")[0]
		if rpc.RpcIdNumber == nil || *rpc.RpcIdNumber != 0 || rpc.RequestIdString != "" ||
			!slices.Contains(rpc.PresentFields, "RequestIdString") || rpc.ToolCallID != tools[0].ToolCallID {
			t.Fatal("gateway/native duplicate observations or original ID types lost")
		}
	})
	t.Run("usage-observations-and-validity", func(t *testing.T) {
		usage := eventsOfType(p, "firewall.token_usage")
		if len(usage) != 5 || usage[0].InputTokens == nil || *usage[0].InputTokens != 200 ||
			usage[0].DurationMs == nil || *usage[0].DurationMs != 1.25 ||
			usage[1].InputTokens == nil || *usage[1].InputTokens != 0 ||
			usage[1].DurationMs == nil || *usage[1].DurationMs != 0 ||
			usage[1].OutputTokens != nil || !slices.Contains(usage[1].NullFields, "OutputTokens") ||
			usage[0].ProvenanceTimestampMs == nil || *usage[0].ProvenanceTimestampMs != 400 {
			t.Fatal("recorded usage order, fractional duration, zero or null changed")
		}
		if usage[2].InputTokens != nil || usage[2].OutputTokens != nil || usage[2].DurationMs != nil ||
			!slices.Contains(usage[2].InvalidFields, "InputTokens") ||
			!slices.Contains(usage[3].NullFields, "Usage") ||
			!slices.Contains(usage[4].InvalidFields, "Usage") {
			t.Fatal("missing/malformed/invalid measurements were replaced by zero")
		}
		reports := eventsOfType(p, "usage.report")
		if len(reports) != 2 || reports[1].Phase != "evals" || reports[1].InputTokens != nil ||
			!strings.Contains(reports[1].PayloadJSON, "9007199254740993") {
			t.Fatal("usage source precedence inputs or unsafe original numeric value lost")
		}
	})
	t.Run("retries-and-applied-receipts", func(t *testing.T) {
		retry := eventsOfType(p, "harness.retry")[0]
		if !strings.Contains(retry.PayloadJSON, `"attemptResults":[`) ||
			!strings.Contains(retry.PayloadJSON, `"startupAttemptResults":[`) ||
			len(eventsOfType(p, "harness.attempt")) != 3 || len(eventsOfType(p, "engine.retry")) != 1 {
			t.Fatal("canonical attempts and compatibility observations reconciled during capture")
		}
		output := eventsOfType(p, "safe_output.result")
		if len(output) != 4 || slices.Contains(output[0].PresentFields, "Status") ||
			output[1].Status != "failure" || output[1].Phase != "safe_outputs" ||
			output[1].ProvenancePath != "usage/aw_session.jsonl" ||
			output[2].Status != "success" || !slices.Contains(output[3].NullFields, "Status") {
			t.Fatal("failed applied receipts or dispatch/status state were dropped")
		}
	})
	t.Run("supplied-trajectory-and-redaction", func(t *testing.T) {
		ir := eventsOfType(p, "trajectory.ir")
		if len(ir) != 4 || ir[1].Phase != "detection" ||
			!slices.Contains(ir[2].NullFields, "Objectives") ||
			!slices.Contains(ir[2].InvalidFields, "Actions") || !slices.Contains(ir[2].InvalidFields, "States") ||
			slices.Contains(ir[3].PresentFields, "States") {
			t.Fatal("missing/malformed trajectory collections or phase isolation lost")
		}
		var original, approved map[string]any
		if decode(lines[ir[0].Sequence], &original) != nil || decode([]byte(ir[0].PayloadJSON), &approved) != nil {
			t.Fatal("invalid IR fixture")
		}
		data := original["data"].(map[string]any)
		data["objectives"].([]any)[0].(map[string]any)["description"] = "policy guard [REDACTED]"
		if !reflect.DeepEqual(data, approved["data"]) {
			t.Fatal("objective/action identity, index zero, grounding validity, repeated states or IR event mapping lost")
		}
		if !slices.Contains(ir[0].OmissionReasons, "data.objectives:redacted") {
			t.Fatal("redacted objective description was treated as complete evidence")
		}
	})
}

func TestOriginalEvidenceParquetAndPublication(t *testing.T) {
	c := testConfig(t)
	p := originalEvidence(t, c)
	if _, _, err := NativeParquet(t.Context(), p, 128<<20); err != nil {
		t.Fatal(err)
	}
	converter := testConverter(t)
	path := filepath.Join(t.TempDir(), "original.parquet")
	if _, err := converter.Write(t.Context(), path, []Projection{p}); err != nil {
		t.Fatal(err)
	}
	var restored []Event
	if err := converter.query(t.Context(), "SELECT "+eventSelect+" FROM read_parquet("+literal(path)+") ORDER BY Sequence", &restored); err != nil ||
		!reflect.DeepEqual(p.Events, restored) {
		t.Fatalf("real DuckDB typed/payload round trip failed: %v", err)
	}
	a := openTestArchive(t, c, nil)
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := pinTestArchive(t, a)
	events, err := s.Timeline(t.Context(), TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 7, Limit: 100})
	if err != nil || !reflect.DeepEqual(p.Events, events) {
		t.Fatalf("published reconstruction changed evidence: %v", err)
	}
	definition := analyticalDefinition(t, `"filter":{"predicates":[{"field":"DurationMs","gte":0.125,"lt":2}]},"select":[{"field":"DurationMs"},{"field":"Success"}]`)
	result, err := s.Analytical(t.Context(), definition)
	if err != nil || len(result.Rows) != 2 || result.Rows[0]["DurationMs"] != json.Number("0.125") ||
		result.Rows[0]["Success"] != nil {
		t.Fatalf("typed real/null analytical delivery lost: %#v %v", result, err)
	}
	definition = analyticalDefinition(t, `"filter":{"predicates":[{"field":"Phase","equals":"agent"},{"field":"Success","equals":false}]},"select":[{"field":"Success"}]`)
	result, err = s.Analytical(t.Context(), definition)
	if err != nil || len(result.Rows) != 2 || result.Rows[0]["Success"] != false || result.Rows[1]["Success"] != false {
		t.Fatalf("typed false was treated as missing or zero: %#v %v", result, err)
	}
}

func TestOriginalEvidenceExclusionAndVersionFences(t *testing.T) {
	c := nativeTestConfig(t)
	raw := []byte("{\"type\":\"tool.execution_complete\",\"provenance\":{\"phase\":\"agent\",\"attemptId\":\"approved-id\",\"startupAttemptId\":\"startup-id\"},\"data\":{\"error\":{\"message\":\"private\"},\"success\":false}}\n")
	project := func() Projection {
		t.Helper()
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, raw, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	c.Policy.Events["tool.execution_complete"] = []string{"success"}
	unapproved := project()
	e := unapproved.Events[0]
	if strings.Contains(e.PayloadJSON, "approved-id") || strings.Contains(e.PayloadJSON, "private") ||
		slices.Contains(e.PresentFields, "Error") || !slices.Contains(e.OmissionReasons, "data.error:excluded") ||
		!slices.Contains(e.OmissionReasons, "provenance.attemptId:excluded") {
		t.Fatal("excluded evidence leaked via typed fields or omission values")
	}
	c.Policy.ProvenanceFields = []string{"attemptId", "startupAttemptId"}
	approved := project()
	if approved.Version == unapproved.Version || approved.PolicyDigest == unapproved.PolicyDigest ||
		approved.Events[0].ProvenanceAttemptID != "approved-id" {
		t.Fatal("explicit provenance approval reused old immutable projection")
	}
	c.Policy.RedactPatterns = []string{"approved-id"}
	redacted := project().Events[0]
	if redacted.ProvenanceAttemptID != "" || !strings.Contains(redacted.PayloadJSON, "[REDACTED]") ||
		!slices.Contains(redacted.OmissionReasons, "provenance.attemptId:redacted") || redacted.Phase != "agent" {
		t.Fatal("typed column bypassed redaction")
	}
	e = approved.Events[0]
	for _, version := range []int64{1, 2} {
		e.ContractVersion = version
		if !errors.Is(ValidateEvent(e), ErrIntegrity) {
			t.Fatalf("archive contract version %d accepted", version)
		}
	}
	e = approved.Events[0]
	e.EvidenceVersion = "cao-approved-events-v2"
	if !errors.Is(ValidateEvent(e), ErrIntegrity) {
		t.Fatal("v2 preservation transform accepted")
	}
	approved.Events[0].Success = new(true)
	if !errors.Is(validateProjection(approved), ErrIntegrity) {
		t.Fatal("typed evidence substituted for the original payload")
	}
	evidence, err := adxtrace.Project(c.traceConfig(), "octo/api", "123", "", 1, raw, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "cao-approved-events-v2"} {
		evidence.EvidenceVersion = version
		if _, err := FromEvidence(c.Namespace, evidence); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("old preservation transformation %q accepted", version)
		}
	}
	raw = []byte("{\"type\":\"tool.execution_complete\",\"provenance\":{\"attemptId\":{\"unapprovedNested\":\"content\"}},\"data\":{}}\n")
	if _, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, raw, time.Now()); err == nil {
		t.Fatal("scalar provenance approval broadened into structured content")
	}
}

func TestOriginalNumericEvidenceIsExactAndBounded(t *testing.T) {
	c := nativeTestConfig(t)
	c.Policy.Events["usage.report"] = []string{"usage", "durationMs"}
	for _, test := range []struct {
		raw   string
		value int64
		valid bool
	}{
		{"1.0e3", 1000, true}, {"1.25", 0, false}, {"-1", 0, false},
		{"9007199254740991", 9007199254740991, true}, {"9007199254740993", 0, false},
		{"1e999999999", 0, false}, {"0e999999999", 0, true}, {"1e-999999999", 0, false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			raw := []byte(`{"type":"usage.report","data":{"usage":{"inputTokens":` + test.raw + `},"durationMs":0.125}}` + "\n")
			p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, raw, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			e := p.Events[0]
			if !strings.Contains(e.PayloadJSON, test.raw) || (e.InputTokens != nil) != test.valid ||
				test.valid && *e.InputTokens != test.value || slices.Contains(e.InvalidFields, "InputTokens") == test.valid {
				t.Fatal("original measurement was rounded, coerced or allowed unbounded numeric expansion")
			}
		})
	}
}

func TestPreviousArchiveSchemaIsRejectedWithoutErasure(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(c.Directory, "state.json")
	content, err := os.ReadFile(statePath) // #nosec G304 -- State is inside the test-owned t.TempDir archive.
	if err != nil {
		t.Fatal(err)
	}
	var state archiveState
	if err := decode(content, &state); err != nil {
		t.Fatal(err)
	}
	state.Version, state.Manifest.Version = 1, 1
	content, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(c); !errors.Is(err, ErrIntegrity) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("old archive opened or migrated: %v", err)
	}
	after, err := os.ReadFile(statePath) // #nosec G304 -- Verify the same test-owned archive state was not modified.
	if err != nil || !bytes.Equal(content, after) {
		t.Fatal("schema rejection erased or rewrote old archive state")
	}
}

// E1 collections are user-approved whole subtrees, default-off: unapproved
// collections record exclusion and approved ones keep their field states.
func TestTrajectoryIRCollectionApproval(t *testing.T) {
	c := nativeTestConfig(t)
	raw := []byte(`{"type":"trajectory.ir","provenance":{"phase":"agent"},"data":{"toolCalls":[{"id":"t1","arguments":{"q":"secret-text"}}],` +
		`"observations":[{"id":"o1","sourceToolCallId":"t1","consumedByActionIds":["a1"]}],"resources":[{"id":"r1","kind":"file","uri":"a.go"}],` +
		`"provenanceEdges":[{"from":"o1","to":"a1"}],"reference":null}}` + "\n")
	project := func() Event {
		t.Helper()
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, raw, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		return p.Events[0]
	}
	collections := []string{"toolCalls", "observations", "resources", "provenanceEdges", "reference"}
	families := []string{"ToolCalls", "Observations", "Resources", "ProvenanceEdges", "Reference"}
	c.Policy.Events["trajectory.ir"] = []string{}
	excluded := project()
	for i, collection := range collections {
		if slices.Contains(excluded.PresentFields, families[i]) || !slices.Contains(excluded.OmissionReasons, "data."+collection+":excluded") ||
			strings.Contains(excluded.PayloadJSON, collection) {
			t.Fatalf("unapproved %s was admitted or its exclusion was invisible", collection)
		}
	}
	c.Policy.Events["trajectory.ir"] = collections
	approved := project()
	for _, family := range families {
		if !slices.Contains(approved.PresentFields, family) {
			t.Fatalf("approved %s lost its field state", family)
		}
	}
	if !slices.Contains(approved.NullFields, "Reference") || len(approved.OmissionReasons) != 0 ||
		!strings.Contains(approved.PayloadJSON, `"consumedByActionIds":["a1"]`) {
		t.Fatal("approved collections were not preserved whole")
	}
	c.Policy.RedactPatterns = []string{"secret-text"}
	redacted := project()
	if !slices.Contains(redacted.OmissionReasons, "data.toolCalls:redacted") || slices.Contains(redacted.OmissionReasons, "data.observations:redacted") {
		t.Fatal("collection redaction was not recorded per family")
	}
}
