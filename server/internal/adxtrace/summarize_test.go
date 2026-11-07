package adxtrace

import (
	"testing"
	"time"
)

func TestSummarizeLedger(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fresh, stale := now.Add(time.Hour), now.Add(-time.Hour)
	tests := []struct {
		name   string
		state  *ledger
		config Config
		want   Status
	}{
		{"empty", &ledger{}, Config{}, Status{Stage: "waiting"}},
		{"delivered eligible record", &ledger{
			Frames:  map[string]*frame{"f": {}},
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x"}},
		}, Config{}, Status{Stage: "waiting", Frames: 1, Eligible: 1}},
		{"undelivered gap", &ledger{
			Records: map[string]*record{"a": {State: "excluded"}},
		}, Config{}, Status{Stage: "waiting", Gaps: 1, Pending: 1, Degraded: true}},
		{"fresh ready mining pending", &ledger{
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x", Mining: &miningProjection{State: "ready", FreshUntil: fresh}}},
		}, Config{}, Status{Stage: "waiting", Eligible: 1, Pending: 1}},
		{"stale mining is a gap", &ledger{
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x", Mining: &miningProjection{State: "ready", Delivered: "y", FreshUntil: stale}}},
		}, Config{}, Status{Stage: "waiting", Eligible: 1, Gaps: 1, Degraded: true}},
		{"not ready mining is a gap", &ledger{
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x", Mining: &miningProjection{State: "partial", Delivered: "y", FreshUntil: fresh}}},
		}, Config{}, Status{Stage: "waiting", Eligible: 1, Gaps: 1, Degraded: true}},
		{"checked without mining when enabled", &ledger{
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x", MiningCheckedAt: now}},
		}, Config{MiningEnabled: true}, Status{Stage: "waiting", Eligible: 1, Gaps: 1, Degraded: true}},
		{"checked without mining when disabled", &ledger{
			Records: map[string]*record{"a": {State: "eligible", Delivered: "x", MiningCheckedAt: now}},
		}, Config{}, Status{Stage: "waiting", Eligible: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeLedger(tt.state, tt.config, now); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSummarizeLedgerFreshnessBoundary(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	state := &ledger{Records: map[string]*record{"a": {State: "eligible", Delivered: "x", Mining: &miningProjection{State: "ready", Delivered: "y", FreshUntil: now}}}}
	if got := summarizeLedger(state, Config{}, now); got.Gaps != 1 || !got.Degraded {
		t.Fatalf("mining expiring exactly now must be a gap: %+v", got)
	}
}
