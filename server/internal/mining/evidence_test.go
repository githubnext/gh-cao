package mining

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOutcomeDispositionIsIndependentOfExecution(t *testing.T) {
	at := testInstant()
	for _, tc := range []struct {
		name, state, reason, actor string
		pr, merged, resolved       bool
		status, signal             string
	}{
		{"merged", "closed", "", "User", true, true, true, "accepted", "merged"},
		{"unmerged", "closed", "", "User", true, false, true, "rejected", "closed_without_merge"},
		{"completed", "closed", "completed", "User", false, false, true, "accepted", "completed"},
		{"bot close", "closed", "not_planned", "Bot", false, false, true, "lifecycle", "lifecycle"},
		{"human close", "closed", "not_planned", "User", false, false, true, "rejected", "closed_not_planned"},
		{"actor missing", "closed", "not_planned", "", false, false, true, "unknown", "missing_human_resolution_evidence"},
		{"clock missing", "closed", "not_planned", "User", false, false, false, "unknown", "missing_human_resolution_evidence"},
		{"reason missing", "closed", "", "User", false, false, true, "unknown", "missing_resolution_evidence"},
		{"open", "open", "", "", false, false, false, "pending", "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Outcome{State: tc.state, StateReason: tc.reason, CloseActor: tc.actor, PullRequest: tc.pr, Merged: tc.merged}
			if tc.resolved {
				o.DispositionAt = &at
			}
			if status, signal := o.Evaluation(); status != tc.status || signal != tc.signal {
				t.Fatalf("evaluation = %s/%s, want %s/%s", status, signal, tc.status, tc.signal)
			}
		})
	}
}

func testInstant() time.Time {
	return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
}

func TestOutcomeRejectsInvalidDispositionClock(t *testing.T) {
	now := testInstant()
	base := Outcome{
		Repository: "octo/api", Number: 1, URL: "https://github.com/octo/api/issues/1",
		State: "closed", UpdatedAt: now, ObservedAt: now, Source: "github-webhook",
	}
	for _, change := range []func(*Outcome){
		func(o *Outcome) {
			future := now.Add(time.Minute)
			o.DispositionAt = &future
		},
		func(o *Outcome) {
			o.State = "open"
			o.DispositionAt = &now
		},
	} {
		outcome := base
		change(&outcome)
		if err := outcome.Validate(); err == nil {
			t.Fatalf("accepted invalid disposition clock: %+v", outcome)
		}
	}
}

func TestTargetRequiresExactObjectIdentity(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/octo/api/issues/1", "https://user@github.com/octo/api/issues/1",
		"https://github.com/octo/api/issues/1?x=y", "https://github.com/octo/api/pull/01",
		"https://github.com/octo/api/issues/-1", "https://github.com/octo/api/issues/1/comments",
		"https://github.com/octo/%61pi/issues/1", "https://github.com/octo/api/1",
	} {
		if _, err := ParseTarget(raw); err == nil {
			t.Errorf("accepted invalid target %s", raw)
		}
	}
	target, err := ParseTarget("https://github.com/Octo/API/pull/12")
	if err != nil || target.Repository != "octo/api" || target.Number != 12 || !target.PullRequest {
		t.Fatalf("target = %+v, %v", target, err)
	}
}

func TestEpisodeAuditPreservesRecordedIdentityAndHops(t *testing.T) {
	m := Membership{Repository: "octo/api", RunID: "42", Attempt: 2, ObservedAt: time.Now().UTC(),
		Context: Context{EpisodeID: "recorded-episode", HopID: "recorded-hop", ParentHopID: "recorded-parent",
			Repo: "other/root", RunID: "41", RootRepo: "other/root", RootRunID: "40"}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	rows := m.Audits()
	if len(rows) != 2 || rows[0]["correlationId"] != "recorded-episode" ||
		rows[0]["name"] != "recorded-hop" || rows[0]["code"] != "recorded-parent" ||
		rows[0]["claimRunId"] != "41" || rows[1]["claimRunId"] != "40" ||
		rows[0]["scope"] != "selected_runs" {
		t.Fatalf("lost recorded lineage: %+v", rows)
	}
	m.Context.EpisodeID = ""
	if err := m.Validate(); err == nil {
		t.Fatal("membership was synthesized without recorded identity")
	}
}

func TestObjectDecodesNullableGitHubFields(t *testing.T) {
	var object Object
	if err := json.Unmarshal([]byte(`{"number":1,"merged_at":null,"closed_by":null,"state_reason":null}`), &object); err != nil {
		t.Fatal(err)
	}
}
