package auditdelivery

import (
	"slices"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestVerificationAnchors(t *testing.T) {
	head := operational.AuditAnchor{Sequence: 10, Hash: "h"}
	tests := []struct {
		name           string
		delivered, end operational.AuditAnchor
		want           []operational.AuditAnchor
	}{
		{"first page", operational.AuditAnchor{}, operational.AuditAnchor{Sequence: 3, Hash: "e"},
			[]operational.AuditAnchor{head, {Sequence: 3, Hash: "e"}}},
		{"later page", operational.AuditAnchor{Sequence: 3, Hash: "d"}, operational.AuditAnchor{Sequence: 5, Hash: "e"},
			[]operational.AuditAnchor{head, {Sequence: 3, Hash: "d"}, {Sequence: 5, Hash: "e"}}},
		{"idle", operational.AuditAnchor{Sequence: 3, Hash: "d"}, operational.AuditAnchor{Sequence: 3, Hash: "d"},
			[]operational.AuditAnchor{head, {Sequence: 3, Hash: "d"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verificationAnchors(head, tt.delivered, tt.end); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoversPage(t *testing.T) {
	delivered := operational.AuditAnchor{Sequence: 4}
	end := operational.AuditAnchor{Sequence: 6}
	good := operational.AuditVerification{AnchorsChecked: 2, FirstSequence: 5, LastSequence: 9}
	if !coversPage(good, delivered, end) {
		t.Fatal("expected contiguous verification to cover the page")
	}
	cases := map[string]func(*operational.AuditVerification){
		"expired anchor":  func(v *operational.AuditVerification) { v.AnchorsExpired = 1 },
		"no anchors":      func(v *operational.AuditVerification) { v.AnchorsChecked = 0 },
		"retention gap":   func(v *operational.AuditVerification) { v.FirstSequence = 6 },
		"short of target": func(v *operational.AuditVerification) { v.LastSequence = 5 },
	}
	for name, mutate := range cases {
		v := good
		mutate(&v)
		if coversPage(v, delivered, end) {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
