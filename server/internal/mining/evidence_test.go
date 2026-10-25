package mining

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOutcomeDispositionIsIndependentOfExecution(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason, actor string
		pr, merged                 bool
		status, signal             string
	}{
		{"merged", "closed", "", "", true, true, "accepted", "merged"},
		{"unmerged", "closed", "", "", true, false, "rejected", "closed_without_merge"},
		{"completed", "closed", "completed", "", false, false, "accepted", "completed"},
		{"bot close", "closed", "not_planned", "Bot", false, false, "lifecycle", "lifecycle"},
		{"human close", "closed", "not_planned", "User", false, false, "rejected", "closed_not_planned"},
		{"actor missing", "closed", "not_planned", "", false, false, "unknown", "missing_resolution_evidence"},
		{"reason missing", "closed", "", "User", false, false, "unknown", "missing_resolution_evidence"},
		{"open", "open", "", "", false, false, "pending", "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Outcome{State: tc.state, StateReason: tc.reason, CloseActor: tc.actor, PullRequest: tc.pr, Merged: tc.merged}
			if status, signal := o.Evaluation(); status != tc.status || signal != tc.signal {
				t.Fatalf("evaluation = %s/%s, want %s/%s", status, signal, tc.status, tc.signal)
			}
		})
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

func TestWebhookUsesClosingActorNotLaterEditor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	base := `{"action":"%s","repository":{"full_name":"Octo/API"},"sender":{"type":"Bot"},
		"issue":{"number":12,"state":"closed","state_reason":"not_planned",
		"updated_at":"2026-01-02T03:04:05Z","html_url":"https://github.com/octo/api/issues/12"}}`
	closed, err := ParseWebhook("issues", []byte(fmt.Sprintf(base, "closed")), now)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := closed.Evaluation(); status != "lifecycle" {
		t.Fatalf("bot close = %+v", closed)
	}
	edited, err := ParseWebhook("issues", []byte(fmt.Sprintf(base, "edited")), now)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := edited.Evaluation(); status != "unknown" {
		t.Fatalf("editor was treated as closing actor: %+v", edited)
	}
	bad := strings.Replace(fmt.Sprintf(base, "closed"), `"state":"closed"`, `"state":"open"`, 1)
	if _, err := ParseWebhook("issues", []byte(bad), now); err == nil {
		t.Fatal("contradictory webhook was accepted")
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
