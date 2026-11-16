package mining

import (
	"sort"
	"time"
)

const AdoptionRegistryVersion = "gh-aw-v1.2.3-adoption-v1"

type AdoptionEffectClass string

const (
	AdoptionProposal    AdoptionEffectClass = "proposal"
	AdoptionPublication AdoptionEffectClass = "publication"
	AdoptionMutation    AdoptionEffectClass = "mutation"
	AdoptionDelegation  AdoptionEffectClass = "delegation"
	AdoptionInfra       AdoptionEffectClass = "infra"
)

type SafeOutputAdoptionRule struct {
	SafeOutputType      string
	EffectClass         AdoptionEffectClass
	HumanDecision       []string
	Rejection           []string
	Reversal            []string
	ParentResource      string
	FallbackToDelivery  bool
	AttributionEligible bool
	SurvivalRequired    bool
}

type AdoptionEvaluation struct {
	Status           string
	Signal           string
	AcceptanceKind   string
	AcceptedAt       *time.Time
	HumanVerified    bool
	SurvivalRequired bool
}

func safeOutputAdoptionRules() map[string]SafeOutputAdoptionRule {
	rules := map[string]SafeOutputAdoptionRule{}
	add := func(effect AdoptionEffectClass, fallback, eligible bool, types ...string) {
		defaultReversal := map[AdoptionEffectClass]string{
			AdoptionProposal:    "RESOURCE_REJECTED_OR_REVERSED",
			AdoptionPublication: "PUBLICATION_REMOVED_OR_HIDDEN",
			AdoptionMutation:    "EFFECT_REVERTED_OR_REMOVED",
			AdoptionDelegation:  "DOWNSTREAM_FAILED_OR_REVERSED",
		}[effect]
		for _, outputType := range types {
			rule := SafeOutputAdoptionRule{
				SafeOutputType: outputType, EffectClass: effect,
				FallbackToDelivery:  fallback,
				AttributionEligible: eligible, SurvivalRequired: eligible,
			}
			if eligible {
				rule.Reversal = []string{defaultReversal}
			}
			rules[outputType] = rule
		}
	}

	add(AdoptionProposal, false, true,
		"create_discussion", "create_issue", "create_pull_request",
		"create_security_advisory")
	add(AdoptionPublication, true, true,
		"add_comment", "add_security_advisory_comment", "create_check_run",
		"create_project", "create_project_status_update",
		"create_pull_request_review_comment", "reply_to_pull_request_review_comment",
		"submit_pull_request_review", "update_release", "upload_artifact", "upload_asset")
	add(AdoptionMutation, true, true,
		"add_labels", "add_reviewer", "assign_milestone", "assign_to_user",
		"autofix_code_scanning_alert", "close_discussion", "close_issue",
		"close_pull_request", "dismiss_pull_request_review", "hide_comment",
		"link_sub_issue", "mark_pull_request_as_ready_for_review",
		"merge_pull_request", "remove_labels", "replace_label",
		"push_to_pull_request_branch",
		"resolve_pull_request_review_thread", "set_issue_field", "set_issue_type",
		"unassign_from_user", "update_discussion", "update_issue", "update_project",
		"update_pull_request")
	add(AdoptionDelegation, true, true,
		"approve_workflow_run", "assign_to_agent", "call_workflow",
		"create_agent_session", "dispatch_repository", "dispatch_workflow")
	add(AdoptionProposal, false, true,
		"create_code_scanning_alert")
	add(AdoptionInfra, false, false,
		"comment_memory", "ledger_append", "ledger_request_compaction", "noop",
		"push_repo_memory", "report_incomplete", "report_missing_data",
		"report_missing_tool", "upload_code_coverage")

	set := func(outputType, parent string, human, rejection, reversal []string) {
		rule := rules[outputType]
		rule.ParentResource = parent
		rule.HumanDecision = human
		rule.Rejection = rejection
		rule.Reversal = reversal
		rules[outputType] = rule
	}
	set("create_issue", "issue",
		[]string{"ISS_CLOSED_OK", "PR_MERGED_CLOSER"},
		[]string{"ISS_CLOSED_REJ", "ISS_DELETED"},
		[]string{"ISS_REOPENED", "ISS_DELETED"})
	set("create_pull_request", "pull_request",
		[]string{"PR_MERGED", "PR_AUTOMERGE_ENABLED", "PR_REVIEW_APPROVED"},
		[]string{"PR_CLOSED_REJ"},
		[]string{"PR_REVERT", "PR_REOPENED", "PR_HEAD_REWRITE"})
	set("create_discussion", "discussion",
		[]string{"DISC_ANSWERED", "DISC_CLOSED_RESOLVED"},
		[]string{"DISC_CLOSED_DUPLICATE", "DISC_CLOSED_OUTDATED", "DISC_DELETED"},
		[]string{"DISC_UNANSWERED", "DISC_DELETED"})
	set("create_security_advisory", "security_advisory",
		[]string{"ADV_PUBLISHED"}, []string{"ADV_CLOSED"}, []string{"ADV_WITHDRAWN", "ADV_CLOSED"})
	set("create_code_scanning_alert", "code_scanning_alert",
		[]string{"CS_FIXED"}, []string{"CS_DISMISSED"}, []string{"CS_REOPENED"})
	set("add_labels", "issue_or_pull_request",
		[]string{"SUGGESTION_APPROVED_ADD_LABEL"}, nil, []string{"LABEL_REMOVED"})
	set("assign_to_user", "issue_or_pull_request",
		[]string{"SUGGESTION_APPROVED_ADD_ASSIGNEE"}, nil, []string{"ASSIGNEE_REMOVED"})
	set("close_issue", "issue",
		[]string{"SUGGESTION_APPROVED_CLOSE"}, []string{"SUGGESTION_DISMISSED"}, []string{"ISS_REOPENED"})
	set("set_issue_type", "issue",
		[]string{"SUGGESTION_APPROVED_SET_TYPE"}, []string{"SUGGESTION_DISMISSED"}, []string{"ISSUE_TYPE_CHANGED"})
	set("set_issue_field", "issue",
		[]string{"SUGGESTION_APPROVED_SET_FIELD"}, []string{"SUGGESTION_DISMISSED"}, []string{"ISSUE_FIELD_CHANGED"})
	set("add_comment", "issue_pull_request_or_discussion",
		[]string{"COMMENT_PINNED", "HUMAN_REPLY", "HUMAN_REACTION"}, nil,
		[]string{"COMMENT_DELETED", "COMMENT_MINIMIZED"})
	set("create_pull_request_review_comment", "pull_request",
		[]string{"PR_SUGGESTION_APPLIED", "PR_THREAD_RESOLVED"}, nil,
		[]string{"PR_THREAD_UNRESOLVED", "PR_REVIEW_DISMISSED", "COMMENT_DELETED"})
	for _, outputType := range []string{
		"add_reviewer", "create_check_run", "dismiss_pull_request_review",
		"mark_pull_request_as_ready_for_review", "push_to_pull_request_branch",
		"reply_to_pull_request_review_comment", "resolve_pull_request_review_thread",
		"submit_pull_request_review", "update_pull_request",
	} {
		set(outputType, "pull_request", []string{"PR_MERGED"}, []string{"PR_CLOSED_REJ"},
			[]string{"PR_REVERT", "PR_HEAD_REWRITE"})
	}
	set("merge_pull_request", "pull_request",
		[]string{"PR_REVIEW_APPROVED"}, []string{"PR_REVIEW_CHANGES_REQUESTED"}, []string{"PR_REVERT"})
	set("add_security_advisory_comment", "security_advisory",
		[]string{"ADV_PUBLISHED"}, []string{"ADV_CLOSED"}, []string{"ADV_WITHDRAWN", "ADV_CLOSED"})
	set("assign_to_agent", "agent_task",
		[]string{"AGENT_PR_MERGED"}, []string{"AGENT_PR_CLOSED_REJ"},
		[]string{"AGENT_TASK_FAILED", "AGENT_PR_REVERTED"})
	set("create_agent_session", "agent_task",
		[]string{"AGENT_PR_MERGED"}, []string{"AGENT_PR_CLOSED_REJ"},
		[]string{"AGENT_TASK_FAILED", "AGENT_PR_REVERTED"})
	for _, outputType := range []string{"approve_workflow_run", "call_workflow", "dispatch_workflow"} {
		set(outputType, "workflow_run", nil, nil, []string{"RUN_FAILED", "RUN_CANCELLED", "RUN_TIMED_OUT"})
	}
	set("dispatch_repository", "repository_dispatch", nil, nil,
		[]string{"DOWNSTREAM_FAILED", "DOWNSTREAM_UNCORRELATED"})
	set("update_release", "release", nil, nil,
		[]string{"RELEASE_EDITED", "RELEASE_DELETED", "RELEASE_UNPUBLISHED"})
	set("upload_artifact", "artifact", nil, nil, []string{"ARTIFACT_EXPIRED", "ARTIFACT_DELETED"})
	set("upload_asset", "asset_branch", nil, nil, []string{"FILE_REMOVED", "BRANCH_DELETED"})
	set("create_project", "project", nil, nil, []string{"PROJECT_CLOSED", "PROJECT_DELETED"})
	set("create_project_status_update", "project", nil, nil,
		[]string{"PROJECT_STATUS_EDITED", "PROJECT_STATUS_DELETED"})
	set("update_project", "project", nil, nil,
		[]string{"PROJECT_EDITED", "PROJECT_ITEM_REMOVED", "PROJECT_DELETED"})
	return rules
}

var adoptionRules = safeOutputAdoptionRules()

func SafeOutputAdoptionRuleFor(outputType string) (SafeOutputAdoptionRule, bool) {
	rule, ok := adoptionRules[outputType]
	if !ok {
		return SafeOutputAdoptionRule{}, false
	}
	rule.HumanDecision = append([]string(nil), rule.HumanDecision...)
	rule.Rejection = append([]string(nil), rule.Rejection...)
	rule.Reversal = append([]string(nil), rule.Reversal...)
	return rule, true
}

func SafeOutputAdoptionRules() []SafeOutputAdoptionRule {
	result := make([]SafeOutputAdoptionRule, 0, len(adoptionRules))
	for outputType := range adoptionRules {
		rule, _ := SafeOutputAdoptionRuleFor(outputType)
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SafeOutputType < result[j].SafeOutputType
	})
	return result
}

func copiedTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func EvaluateSafeOutputDelivery(outputType string, deliveredAt time.Time) AdoptionEvaluation {
	rule, ok := SafeOutputAdoptionRuleFor(outputType)
	if !ok {
		return AdoptionEvaluation{Status: "unknown", Signal: "unsupported_safe_output"}
	}
	if !rule.AttributionEligible {
		return AdoptionEvaluation{Status: "not_applicable", Signal: "no_repository_value_effect"}
	}
	if len(rule.HumanDecision) > 0 && !rule.FallbackToDelivery {
		return AdoptionEvaluation{Status: "pending", Signal: "human_decision_pending",
			SurvivalRequired: rule.SurvivalRequired}
	}
	if rule.FallbackToDelivery {
		delivered := deliveredAt.UTC()
		return AdoptionEvaluation{Status: "accepted", Signal: "delivered",
			AcceptanceKind: "fallback_delivery", AcceptedAt: &delivered,
			SurvivalRequired: rule.SurvivalRequired}
	}
	return AdoptionEvaluation{Status: "unknown", Signal: "missing_acceptance_rule",
		SurvivalRequired: rule.SurvivalRequired}
}

func (o Outcome) EvaluateSafeOutput(outputType string) AdoptionEvaluation {
	rule, ok := SafeOutputAdoptionRuleFor(outputType)
	if !ok {
		return AdoptionEvaluation{Status: "unknown", Signal: "unsupported_safe_output"}
	}
	if !rule.AttributionEligible {
		return AdoptionEvaluation{Status: "not_applicable", Signal: "no_repository_value_effect"}
	}
	if outputType == "create_pull_request" {
		switch {
		case o.State == "open":
			return AdoptionEvaluation{Status: "pending", Signal: "open", SurvivalRequired: true}
		case o.State != "closed":
			return AdoptionEvaluation{Status: "unknown", Signal: "missing_resolution_evidence"}
		case o.CloseActor == "Bot":
			return AdoptionEvaluation{Status: "lifecycle", Signal: "lifecycle", SurvivalRequired: true}
		case o.CloseActor != "User" || o.DispositionAt == nil:
			return AdoptionEvaluation{Status: "unknown", Signal: "missing_human_resolution_evidence", SurvivalRequired: true}
		case o.Merged:
			return AdoptionEvaluation{Status: "accepted", Signal: "merged", AcceptanceKind: "explicit_human",
				AcceptedAt: copiedTime(o.DispositionAt), HumanVerified: true, SurvivalRequired: true}
		default:
			return AdoptionEvaluation{Status: "rejected", Signal: "closed_without_merge",
				AcceptedAt: copiedTime(o.DispositionAt), HumanVerified: true, SurvivalRequired: true}
		}
	}
	if outputType == "create_issue" {
		switch {
		case o.State == "open":
			return AdoptionEvaluation{Status: "pending", Signal: "open", SurvivalRequired: true}
		case o.State != "closed":
			return AdoptionEvaluation{Status: "unknown", Signal: "missing_resolution_evidence"}
		case o.CloseActor == "Bot":
			return AdoptionEvaluation{Status: "lifecycle", Signal: "lifecycle", SurvivalRequired: true}
		case o.CloseActor != "User" || o.DispositionAt == nil:
			return AdoptionEvaluation{Status: "unknown", Signal: "missing_human_resolution_evidence", SurvivalRequired: true}
		case o.StateReason == "completed":
			return AdoptionEvaluation{Status: "accepted", Signal: "completed", AcceptanceKind: "explicit_human",
				AcceptedAt: copiedTime(o.DispositionAt), HumanVerified: true, SurvivalRequired: true}
		case o.StateReason == "not_planned":
			return AdoptionEvaluation{Status: "rejected", Signal: "closed_not_planned",
				AcceptedAt: copiedTime(o.DispositionAt), HumanVerified: true, SurvivalRequired: true}
		default:
			return AdoptionEvaluation{Status: "unknown", Signal: "missing_resolution_evidence", SurvivalRequired: true}
		}
	}
	return EvaluateSafeOutputDelivery(outputType, o.UpdatedAt)
}
