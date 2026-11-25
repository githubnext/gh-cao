package adxtrace

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestMiningRequiresRedaction(t *testing.T) {
	clean := miningEvidence{
		CanonicalNamespace: "ns", CanonicalRunID: "github:run:example/project:1", State: "ready",
		CapturedAt: clock, FreshUntil: clock.Add(time.Hour), Gaps: []string{"source-invalidated"},
		Tables: map[string][]model.Row{"TraceOutcomes": {{"OutputId": "o1", "Status": "accepted"}}},
	}
	tests := []struct {
		name     string
		evidence func() miningEvidence
		patterns []string
		want     bool
		wantErr  bool
	}{
		{name: "clean evidence", evidence: func() miningEvidence { return clean }},
		{name: "built-in secret value", evidence: func() miningEvidence {
			e := clean
			e.Tables = map[string][]model.Row{"TraceOutcomes": {{"OutputId": "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"}}}
			return e
		}, want: true},
		{name: "secret-named field", evidence: func() miningEvidence {
			e := clean
			e.Tables = map[string][]model.Row{"TraceOutcomes": {{"Token": "x", "token": "x"}}}
			return e
		}, want: true},
		{name: "policy pattern", evidence: func() miningEvidence {
			e := clean
			e.Gaps = []string{"internal-host-17"}
			return e
		}, patterns: []string{`internal-host-\d+`}, want: true},
		{name: "unmatched policy pattern", evidence: func() miningEvidence { return clean }, patterns: []string{`internal-host-\d+`}},
		{name: "invalid policy pattern", evidence: func() miningEvidence { return clean }, patterns: []string{`(`}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := miningRequiresRedaction(test.evidence(), test.patterns)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("got changed=%t err=%v, want changed=%t err=%t", got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestMiningRequiresRedactionLeavesBuiltInPatternsUnchanged(t *testing.T) {
	before := len(builtInSecrets)
	if _, err := miningRequiresRedaction(miningEvidence{}, []string{`extra`}); err != nil {
		t.Fatal(err)
	}
	if len(builtInSecrets) != before {
		t.Fatal("policy patterns leaked into the shared built-in list")
	}
}
