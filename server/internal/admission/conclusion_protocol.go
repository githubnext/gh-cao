package admission

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
)

const (
	MaxConclusionBytes = 64 << 10
	MaxSessionBytes    = 16 << 10
	MaxSessionEvents   = 128
	maxSafeInteger     = 1<<53 - 1
)

type SessionCompaction struct {
	Version        int   `json:"version"`
	IncludedEvents int   `json:"included_events"`
	OmittedEvents  int64 `json:"omitted_events"`
	OversizedLines int64 `json:"oversized_lines"`
	ScanTruncated  bool  `json:"scan_truncated"`
	Truncated      bool  `json:"truncated"`
}

type ConclusionRequest struct {
	Request
	Session           string            `json:"session"`
	SessionCompaction SessionCompaction `json:"session_compaction"`
	UsageArtifactIDs  []string          `json:"usage_artifact_ids"`
}

var compactFields = map[string][]string{
	"session.format":       {"version"},
	"workflow.info":        {"engineId", "agentVersion", "cliVersion", "awfVersion", "mcpgVersion", "model", "requestedModel", "triggerType", "dryRun", "workflowId", "workflowCallId", "repository", "runId", "runAttempt", "ref", "sha"},
	"session.result":       {"sourceEngine", "numTurns", "durationMs", "totalCostUsd", "status", "sourceType", "sessionId", "agentId"},
	"usage.report":         {"stepIndex", "provider", "model", "purpose", "status", "aic", "totalAic", "premiumRequests", "durationMs", "sessionId", "agentId"},
	"firewall.token_usage": {"stepIndex", "provider", "model", "purpose", "status", "aic", "totalAic", "premiumRequests", "durationMs", "sessionId", "agentId"},
	"guardrail.admission":  {"status", "maxAiCredits", "threatDetectionMaxAiCredits", "evalsMaxAiCredits"},
	"guardrail.daily_aic":  {"status", "exceeded", "total", "estimated", "threshold"},
	"workflow.job_result":  {"jobId", "result", "durationMs"},
	"execution.result":     {"outcome", "conclusion", "exitCode", "durationMs", "startedAt", "finishedAt"},
	"grader.result":        {"id", "status", "value", "score", "unit", "passed", "direction", "threshold", "enabled", "min", "max", "version"},
}

var compactUsageFields = []string{
	"totalTokens", "inputTokens", "outputTokens", "reasoningOutputTokens",
	"cacheReadInputTokens", "cacheCreationInputTokens", "inputTokensIncludeCache",
}

func decodeConclusion(data []byte) (ConclusionRequest, error) {
	result, reason := parseConclusion(data)
	if reason != "" {
		admissionLog.Printf("conclusion denied: reason=%s", reason)
		return ConclusionRequest{}, ErrDenied
	}
	return result, nil
}

// parseConclusion decodes a conclusion payload and returns a fixed reason code
// for the first structural or bounds violation, or "" when the payload is valid.
func parseConclusion(data []byte) (ConclusionRequest, string) {
	var fields map[string]json.RawMessage
	if len(data) > MaxConclusionBytes || strictJSON(data, &fields) != nil || len(fields) != 13 {
		return ConclusionRequest{}, "envelope"
	}
	var result ConclusionRequest
	if strictJSON(data, &result) != nil {
		return ConclusionRequest{}, "envelope"
	}
	if !validConclusionIdentity(fields) {
		return ConclusionRequest{}, "identity"
	}
	if reason := conclusionCompactionShapeViolation(fields); reason != "" {
		return ConclusionRequest{}, reason
	}
	if reason := conclusionBoundsViolation(result); reason != "" {
		return ConclusionRequest{}, reason
	}
	if reason := compactSessionViolation(result.Session, result.SessionCompaction.IncludedEvents); reason != "" {
		return ConclusionRequest{}, reason
	}
	return result, ""
}

// validConclusionIdentity reports whether the non-conclusion fields form a
// bounded, valid admission request identity.
func validConclusionIdentity(fields map[string]json.RawMessage) bool {
	identity := make(map[string]json.RawMessage, 10)
	identityBytes := 1
	for name, value := range fields {
		switch name {
		case "session", "session_compaction", "usage_artifact_ids":
		default:
			identity[name] = value
			identityBytes += len(name) + len(value) + 4
		}
	}
	encoded, err := json.Marshal(identity)
	if err != nil || identityBytes > 8192 {
		return false
	}
	_, err = decodeRequest(encoded)
	return err == nil
}

// conclusionCompactionShapeViolation returns a fixed reason code when the
// session_compaction object lacks exactly its six counters or any top-level
// field is an explicit null, which must not become a Go zero value.
func conclusionCompactionShapeViolation(fields map[string]json.RawMessage) string {
	var compaction map[string]json.RawMessage
	if strictJSON(fields["session_compaction"], &compaction) != nil || len(compaction) != 6 {
		return "compaction_shape"
	}
	for _, value := range fields {
		if string(value) == "null" {
			return "explicit_null"
		}
	}
	for _, name := range []string{"version", "included_events", "omitted_events", "oversized_lines", "scan_truncated", "truncated"} {
		value, exists := compaction[name]
		if !exists || string(value) == "null" {
			return "compaction_shape"
		}
	}
	return ""
}

// conclusionBoundsViolation returns a fixed reason code for the first numeric,
// size, or artifact-identifier bound a conclusion exceeds, or "" when valid.
func conclusionBoundsViolation(result ConclusionRequest) string {
	c := result.SessionCompaction
	if c.Version != 1 || c.IncludedEvents < 0 || c.IncludedEvents > MaxSessionEvents ||
		c.OmittedEvents < 0 || c.OmittedEvents > maxSafeInteger ||
		c.OversizedLines < 0 || c.OversizedLines > maxSafeInteger ||
		len(result.UsageArtifactIDs) > 2 || len(result.Session) > MaxSessionBytes {
		return "compaction_bounds"
	}
	for n, id := range result.UsageArtifactIDs {
		if len(id) > 20 || !decimal.MatchString(id) || n > 0 && id == result.UsageArtifactIDs[0] {
			return "artifact_ids"
		}
	}
	return ""
}

// compactSessionViolation returns a fixed reason code when the session is not
// a newline-terminated list of exactly includedEvents valid compact events.
func compactSessionViolation(session string, includedEvents int) string {
	if session == "" {
		if includedEvents != 0 {
			return "session_count"
		}
		return ""
	}
	if !strings.HasSuffix(session, "\n") {
		return "session_terminator"
	}
	lines := strings.Split(strings.TrimSuffix(session, "\n"), "\n")
	if len(lines) != includedEvents {
		return "session_count"
	}
	for _, line := range lines {
		if !validCompactEvent([]byte(line)) {
			return "session_event"
		}
	}
	return ""
}

func validCompactEvent(line []byte) bool {
	var event map[string]json.RawMessage
	if strictJSON(line, &event) != nil {
		return false
	}
	var kind string
	if json.Unmarshal(event["type"], &kind) != nil {
		return false
	}
	fields, ok := compactFields[kind]
	if !ok {
		return false
	}
	var data map[string]json.RawMessage
	if strictJSON(event["data"], &data) != nil || data == nil {
		return false
	}
	for name, value := range event {
		switch name {
		case "type", "data":
		case "timestamp":
			if !compactScalar(value) {
				return false
			}
		case "provenance":
			if !compactScalars(value, []string{"component", "phase", "path", "index"}) {
				return false
			}
		default:
			return false
		}
	}
	for name, value := range data {
		switch {
		case name == "usage" && (kind == "session.result" || kind == "usage.report" || kind == "firewall.token_usage"):
			if !compactScalars(value, compactUsageFields) {
				return false
			}
		case name == "run" && kind == "grader.result":
			if !compactScalars(value, []string{"id", "attempt", "graderCount", "passed", "failed", "errors"}) {
				return false
			}
		default:
			if !slices.Contains(fields, name) || !compactScalar(value) {
				return false
			}
		}
	}
	return true
}

func compactScalars(raw []byte, fields []string) bool {
	var values map[string]json.RawMessage
	if strictJSON(raw, &values) != nil || values == nil {
		return false
	}
	for name, value := range values {
		if !slices.Contains(fields, name) || !compactScalar(value) {
			return false
		}
	}
	return true
}

func compactScalar(raw []byte) bool {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch scalar := value.(type) {
	case string:
		return len(scalar) <= 256
	case bool:
		return true
	case json.Number:
		number, err := scalar.Float64()
		return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
	default:
		return false
	}
}
