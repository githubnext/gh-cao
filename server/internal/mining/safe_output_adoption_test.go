package mining

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSafeOutputAdoptionRegistryCoversGHAW123(t *testing.T) {
	want := []string{
		"add_comment",
		"add_labels",
		"add_reviewer",
		"add_security_advisory_comment",
		"approve_workflow_run",
		"assign_milestone",
		"assign_to_agent",
		"assign_to_user",
		"autofix_code_scanning_alert",
		"call_workflow",
		"close_discussion",
		"close_issue",
		"close_pull_request",
		"comment_memory",
		"create_agent_session",
		"create_check_run",
		"create_code_scanning_alert",
		"create_discussion",
		"create_issue",
		"create_project",
		"create_project_status_update",
		"create_pull_request",
		"create_pull_request_review_comment",
		"create_security_advisory",
		"dismiss_pull_request_review",
		"dispatch_repository",
		"dispatch_workflow",
		"hide_comment",
		"ledger_append",
		"ledger_request_compaction",
		"link_sub_issue",
		"mark_pull_request_as_ready_for_review",
		"merge_pull_request",
		"noop",
		"push_repo_memory",
		"push_to_pull_request_branch",
		"remove_labels",
		"replace_label",
		"reply_to_pull_request_review_comment",
		"report_incomplete",
		"report_missing_data",
		"report_missing_tool",
		"resolve_pull_request_review_thread",
		"set_issue_field",
		"set_issue_type",
		"submit_pull_request_review",
		"unassign_from_user",
		"update_discussion",
		"update_issue",
		"update_project",
		"update_pull_request",
		"update_release",
		"upload_artifact",
		"upload_asset",
		"upload_code_coverage",
	}
	rules := SafeOutputAdoptionRules()
	got := make([]string, len(rules))
	for index, rule := range rules {
		got[index] = rule.SafeOutputType
		if rule.EffectClass == "" {
			t.Fatalf("%s lacks an effect class", rule.SafeOutputType)
		}
		if rule.AttributionEligible != rule.SurvivalRequired {
			t.Fatalf("%s eligibility and survival requirement diverge", rule.SafeOutputType)
		}
		if rule.AttributionEligible && len(rule.Reversal) == 0 {
			t.Fatalf("%s has no survival reversal marker", rule.SafeOutputType)
		}
		if rule.AttributionEligible && len(rule.HumanDecision) == 0 && !rule.FallbackToDelivery {
			t.Fatalf("%s has neither human adoption nor delivery fallback", rule.SafeOutputType)
		}
		if evaluation := EvaluateSafeOutputDelivery(rule.SafeOutputType, testInstant()); evaluation.Status == "unknown" {
			t.Fatalf("%s did not receive a registry-derived delivery state: %+v", rule.SafeOutputType, evaluation)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry types = %#v, want %#v", got, want)
	}
	if _, ok := SafeOutputAdoptionRuleFor("future_output"); ok {
		t.Fatal("unknown safe output did not fail closed")
	}
}

func TestDashboardAdoptionRegistryMatchesServerRegistry(t *testing.T) {
	content, err := os.ReadFile("../../../dashboard/site/src/data/model/safe-output-adoption-rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var mirror struct {
		Version string `json:"version"`
		Rules   []struct {
			Type                string `json:"type"`
			EffectClass         string `json:"effectClass"`
			FallbackToDelivery  bool   `json:"fallbackToDelivery"`
			AttributionEligible bool   `json:"attributionEligible"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(content, &mirror); err != nil {
		t.Fatal(err)
	}
	if mirror.Version != AdoptionRegistryVersion || len(mirror.Rules) != len(adoptionRules) {
		t.Fatalf("dashboard registry version/count = %s/%d", mirror.Version, len(mirror.Rules))
	}
	for _, mirrored := range mirror.Rules {
		rule, ok := SafeOutputAdoptionRuleFor(mirrored.Type)
		if !ok || string(rule.EffectClass) != mirrored.EffectClass ||
			rule.FallbackToDelivery != mirrored.FallbackToDelivery ||
			rule.AttributionEligible != mirrored.AttributionEligible {
			t.Fatalf("dashboard registry drift for %s: %+v %+v", mirrored.Type, mirrored, rule)
		}
	}
}

func TestAcceptedAuditUsesDispositionTime(t *testing.T) {
	acceptedAt := testInstant()
	outcome := Outcome{
		Repository: "octo/api", Number: 12, PullRequest: true,
		URL:   "https://github.com/octo/api/pull/12",
		State: "closed", Merged: true, CloseActor: "User",
		DispositionAt: &acceptedAt, UpdatedAt: acceptedAt.Add(time.Hour),
		ObservedAt: acceptedAt.Add(2 * time.Hour), Source: "github-webhook",
	}
	audit := outcome.AuditForSafeOutput("output", "run", "create_pull_request")
	if audit["status"] != "accepted" || audit["timestamp"] != acceptedAt ||
		audit["observedAt"] != outcome.ObservedAt {
		t.Fatalf("audit clocks or disposition = %+v", audit)
	}
}

func TestFallbackAcceptanceRequiresSurvival(t *testing.T) {
	outcome := Outcome{UpdatedAt: testInstant()}
	evaluation := outcome.EvaluateSafeOutput("add_comment")
	if evaluation.Status != "accepted" || evaluation.Signal != "delivered" ||
		evaluation.AcceptanceKind != "fallback_delivery" || evaluation.AcceptedAt == nil ||
		!evaluation.SurvivalRequired || evaluation.HumanVerified {
		t.Fatalf("fallback evaluation = %+v", evaluation)
	}
}
