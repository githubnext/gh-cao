package admission

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestConclusionBoundsViolation(t *testing.T) {
	valid := ConclusionRequest{SessionCompaction: SessionCompaction{Version: 1}}
	for _, test := range []struct {
		name string
		edit func(*ConclusionRequest)
		want string
	}{
		{"valid", func(*ConclusionRequest) {}, ""},
		{"version", func(r *ConclusionRequest) { r.SessionCompaction.Version = 2 }, "compaction_bounds"},
		{"negative included", func(r *ConclusionRequest) { r.SessionCompaction.IncludedEvents = -1 }, "compaction_bounds"},
		{"too many events", func(r *ConclusionRequest) { r.SessionCompaction.IncludedEvents = MaxSessionEvents + 1 }, "compaction_bounds"},
		{"unsafe omitted", func(r *ConclusionRequest) { r.SessionCompaction.OmittedEvents = maxSafeInteger + 1 }, "compaction_bounds"},
		{"oversized session", func(r *ConclusionRequest) { r.Session = strings.Repeat("x", MaxSessionBytes+1) }, "compaction_bounds"},
		{"three artifacts", func(r *ConclusionRequest) { r.UsageArtifactIDs = []string{"1", "2", "3"} }, "compaction_bounds"},
		{"non decimal artifact", func(r *ConclusionRequest) { r.UsageArtifactIDs = []string{"abc"} }, "artifact_ids"},
		{"long artifact", func(r *ConclusionRequest) { r.UsageArtifactIDs = []string{strings.Repeat("1", 21)} }, "artifact_ids"},
		{"duplicate artifact", func(r *ConclusionRequest) { r.UsageArtifactIDs = []string{"7", "7"} }, "artifact_ids"},
		{"distinct artifacts", func(r *ConclusionRequest) { r.UsageArtifactIDs = []string{"7", "8"} }, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.edit(&input)
			if got := conclusionBoundsViolation(input); got != test.want {
				t.Fatalf("violation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCompactSessionViolation(t *testing.T) {
	event := "{\"type\":\"session.format\",\"data\":{}}\n"
	for _, test := range []struct {
		name     string
		session  string
		included int
		want     string
	}{
		{"empty", "", 0, ""},
		{"empty with count", "", 1, "session_count"},
		{"unterminated", strings.TrimSuffix(event, "\n"), 1, "session_terminator"},
		{"count mismatch", event, 2, "session_count"},
		{"unknown event", "{\"type\":\"nope\",\"data\":{}}\n", 1, "session_event"},
		{"valid", event + event, 2, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := compactSessionViolation(test.session, test.included); got != test.want {
				t.Fatalf("violation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseConclusionReasons(t *testing.T) {
	valid := conclusionInput(newFixture(t))
	encode := func(t *testing.T, edit func(map[string]any)) []byte {
		t.Helper()
		body, err := json.Marshal(valid)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(body, &values); err != nil {
			t.Fatal(err)
		}
		edit(values)
		data, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	compaction := func(m map[string]any) map[string]any { return m["session_compaction"].(map[string]any) }
	for _, test := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"valid", func(map[string]any) {}, ""},
		{"extra field", func(m map[string]any) { m["body"] = "x" }, "envelope"},
		{"missing counter", func(m map[string]any) { delete(compaction(m), "omitted_events") }, "compaction_shape"},
		{"missing field", func(m map[string]any) { delete(m, "session") }, "envelope"},
		{"invalid identity", func(m map[string]any) { m["repo"] = "not a repository" }, "identity"},
		{"extra counter rejected by strict decode", func(m map[string]any) { compaction(m)["extra"] = 0 }, "envelope"},
		{"null counter", func(m map[string]any) { compaction(m)["omitted_events"] = nil }, "compaction_shape"},
		{"null session", func(m map[string]any) { m["session"] = nil }, "explicit_null"},
		{"bounds", func(m map[string]any) { compaction(m)["version"] = 2 }, "compaction_bounds"},
		{"artifact ids", func(m map[string]any) { m["usage_artifact_ids"] = []string{"abc"} }, "artifact_ids"},
		{"session count", func(m map[string]any) { compaction(m)["included_events"] = 2 }, "session_count"},
		{"session terminator", func(m map[string]any) { m["session"] = strings.TrimSuffix(valid.Session, "\n") }, "session_terminator"},
		{"session event", func(m map[string]any) { m["session"] = "{\"type\":\"nope\",\"data\":{}}\n" }, "session_event"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, reason := parseConclusion(encode(t, test.edit))
			if reason != test.want {
				t.Fatalf("reason = %q, want %q", reason, test.want)
			}
			if _, err := decodeConclusion(encode(t, test.edit)); (err != nil) != (test.want != "") || err != nil && !errors.Is(err, ErrDenied) {
				t.Fatalf("decodeConclusion error = %v, want denial=%t", err, test.want != "")
			}
			if test.want == "" && result.Session != valid.Session {
				t.Fatal("valid conclusion not decoded")
			}
			if test.want != "" && result.Session != "" {
				t.Fatal("rejected conclusion leaked a partial result")
			}
		})
	}
}

func TestParseConclusionOversizedEnvelope(t *testing.T) {
	data := append([]byte(`{"x":"`), make([]byte, MaxConclusionBytes)...)
	if _, reason := parseConclusion(data); reason != "envelope" {
		t.Fatalf("reason = %q, want envelope", reason)
	}
}
