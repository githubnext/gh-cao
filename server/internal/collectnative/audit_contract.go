package collectnative

import (
	"fmt"
	"strings"
)

// AuditRequest selects semantics, not acquisition authority. Its zero value is
// the supported pinned, default per-run canonical audit. Expanded CLI reports,
// training and externally selected baselines/weights are not this contract.
type AuditRequest struct {
	ExpandedReport bool
	Training       bool
	Baseline       string
	Weights        map[string]float64
}

func (request AuditRequest) Validate() error {
	if request.ExpandedReport || request.Training || request.Baseline != "" || request.Weights != nil {
		return fmt.Errorf("%w: native audit supports only default per-run semantics", ErrUnsupported)
	}
	return nil
}

func validateAuditEngine(engine any) error {
	switch engine {
	case nil, "", "copilot", "claude", "codex", "pi", "gemini":
		return nil
	default:
		return fmt.Errorf("%w: native audit engine declaration", ErrUnsupported)
	}
}

func unsupportedAuditReport(kind string) bool {
	return strings.HasPrefix(kind, "report_") || strings.HasSuffix(kind, "_report") || kind == "intelligence"
}
