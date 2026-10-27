package dashboardquery

import (
	"fmt"
	"strings"
	"testing"
)

func TestSnapshotIdentityExpansionBudget(t *testing.T) {
	facts := make([]any, 2000)
	for index := range facts {
		facts[index] = map[string]any{"fact-kind": "snapshot", "fact-id": fmt.Sprint(index)}
	}
	packet := map[string]any{
		"repository": "octo/example",
		"id":         "operational-value-evidence:octo%2Fexample%3Averify-bugs%3A" + strings.Repeat("m", 10000) + "%3Av1",
		"facts":      facts,
	}
	if _, err := OperationalValueEvidenceRows([]map[string]any{packet}); err == nil ||
		!strings.Contains(err.Error(), "snapshot identities exceed bounded read budget") {
		t.Fatalf("snapshot identity expansion must fail before exceeding the read budget: %v", err)
	}
}
