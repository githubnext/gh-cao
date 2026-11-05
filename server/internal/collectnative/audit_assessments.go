package collectnative

import (
	"context"
	"strings"
	"time"
)

func auditOutputs(fields map[string]any) []any {
	if items, ok := fields["safe_outputs"].([]map[string]any); ok {
		result := make([]any, len(items))
		for i, item := range items {
			result[i] = item
		}
		return result
	}
	return array(fields["safe_outputs"])
}

func auditDomain(fields map[string]any, outputs []any, delegated bool) string {
	metadata := strings.ToLower(text(fields["workflow_name"]) + " " + text(fields["workflow_path"]) + " " + text(fields["event"]))
	types := ""
	for _, item := range outputs {
		types += " " + strings.ToLower(text(object(item)["type"]))
	}
	contains := func(value string, words ...string) bool {
		for _, word := range words {
			if strings.Contains(value, word) {
				return true
			}
		}
		return false
	}
	switch {
	case contains(metadata, "release", "deploy", "publish", "backport", "changelog"):
		return "release_ops"
	case contains(metadata, "research", "investigat", "analysis", "analy", "report", "audit"):
		return "research"
	case contains(metadata, "triage", "label", "classif", "route") || contains(types, "add_labels", "remove_labels", "set_issue_type"):
		return "triage"
	case contains(metadata, "fix", "patch", "repair", "refactor", "swe", "code", "review") ||
		contains(types, "create_pull_request_review_comment", "submit_pull_request_review"):
		return "code_fix"
	case contains(metadata, "cleanup", "maint", "update", "deps", "sync", "housekeeping"):
		return "repo_maintenance"
	case contains(metadata, "issue", "discussion", "comment", "support", "reply") || contains(types, "add_comment", "create_discussion"):
		return "issue_response"
	case delegated:
		return "delegated_automation"
	case object(fields["audit"])["native_issue_response_tool"] == true:
		return "issue_response"
	default:
		return "general_automation"
	}
}

// Thresholds, ordering, titles and recommendation actions are pinned to
// gh-aw v1.0.1 audit_agentic_analysis.go and observability_insights.go.
func agenticAudit(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]any, []any, []any, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	audit := object(fields["audit"])
	toolValue, toolMetricsAvailable := audit["native_tool_types"]
	if !toolMetricsAvailable {
		insights := []any{}
		if engineCounter(object(fields["redacted_domains_analysis"])["total_domains"]) > 0 {
			insights = append(insights, map[string]any{"category": "privacy", "severity": "info",
				"title": "Sensitive destinations were redacted"})
		}
		return nil, nil, insights, []string{"agentic-tool-metrics-unavailable"}, nil
	}
	tools, turns := engineCounter(toolValue), engineCounter(fields["turns"])
	outputs := auditOutputs(fields)
	safe := engineCounter(fields["safe_items_count"])
	writes := safe + len(outputs)
	friction := len(array(audit["missing_tools"])) + len(array(audit["mcp_failures"])) + len(array(audit["missing_data"]))
	delegated := false
	if names := candidates(files, "aw_info.json"); len(names) == 1 {
		if len(files[names[0]]) > limits.defaults().Bytes {
			return nil, nil, nil, nil, ErrCapacity
		}
		var info map[string]any
		if decode(files[names[0]], &info) == nil && matches(info, identity) {
			delegated = object(info["context"]) != nil
		}
	}
	domain := auditDomain(fields, outputs, delegated)
	simple := domain == "triage" || domain == "repo_maintenance" || domain == "issue_response"
	start, _ := time.Parse(time.RFC3339Nano, text(fields["started_at"]))
	end, _ := time.Parse(time.RFC3339Nano, text(fields["updated_at"]))
	duration := time.Duration(0)
	if !start.IsZero() && !end.Before(start) {
		duration = end.Sub(start)
	}
	heavy := duration >= 15*time.Minute || turns >= 12 || tools >= 6 || writes >= 8
	moderate := heavy || duration >= 5*time.Minute || turns >= 6 || tools >= 4 || writes >= 3
	exploratory := turns >= 10 || tools >= 6
	directed := turns < 5 && tools < 4
	narrow := tools < 3
	fraction := 0.0
	switch {
	case turns == 0:
	case safe > 0:
		fraction = float64(min(max(safe, min(safe+1, turns)), turns)) / float64(turns)
	case turns <= 3:
		fraction = 1
	default:
		fraction = .5
	}
	findings, recommendations := []any{}, []any{}
	add := func(kind, category, severity, title, action string) {
		findings = append(findings, auditFinding("agentic_"+kind, category, severity, title, title))
		if severity != "info" {
			priority := "medium"
			if severity == "high" {
				priority = "high"
			}
			recommendations = append(recommendations, auditRecommendation(priority, action, title))
		}
	}
	if heavy {
		severity := "medium"
		if turns >= 14 || tools >= 7 || duration >= 20*time.Minute {
			severity = "high"
		}
		add("resource_heavy_for_domain", "performance", severity, "Resource Heavy For Domain",
			"Compare this run to similar successful runs and trim unnecessary turns, tools, or write actions.")
	}
	if simple && !moderate && directed && narrow {
		add("overkill_for_agentic", "optimization", "low", "Potential Deterministic Alternative",
			"Consider whether a scripted rule or deterministic workflow step could replace this agentic path.")
	}
	if friction >= 3 || (friction > 0 && writes >= 3) || (simple && exploratory) {
		severity := "medium"
		if friction >= 4 || (friction > 0 && writes >= 6) {
			severity = "high"
		}
		add("poor_agentic_control", "agentic", severity, "Weak Agentic Control",
			"Tighten instructions, reduce unnecessary tools, or delay write actions until the workflow has stronger evidence.")
	}
	if fraction > 0 && fraction < .6 && moderate {
		severity := "low"
		if fraction < .4 {
			severity = "medium"
		}
		add("partially_reducible", "optimization", severity, "Partially Reducible To Deterministic",
			"Move data-fetching work to frontmatter steps: (pre-agent) writing to /tmp/gh-aw/agent/ or post-steps: (post-agent) to reduce inference cost. See the DeterministicOps guide.")
	}
	if moderate && simple && writes < 6 {
		add("model_downgrade_available", "optimization", "low", "Cheaper Model Available",
			"Try engine.model: gpt-4.1-mini or claude-haiku-4-5 in the workflow frontmatter.")
	}
	if delegated {
		add("delegated_context_present", "coordination", "info", "Dispatch Context Preserved", "")
	}
	insights := []any{}
	insight := func(category, severity, title string) {
		insights = append(insights, map[string]any{"category": category, "severity": severity, "title": title})
	}
	auditTurns := engineCounter(first(object(audit["metrics"])["turns"], fields["turns"]))
	switch {
	case auditTurns >= 12 || tools >= 6:
		insight("execution", "medium", "Exploratory execution path")
	case auditTurns >= 6 || tools >= 4:
		insight("execution", "info", "Adaptive execution path")
	default:
		insight("execution", "info", "Directed execution path")
	}
	if len(outputs) > 0 || safe > 0 {
		insight("actuation", "info", "Write path executed")
	} else {
		insight("actuation", "info", "Read-only posture observed")
	}
	if friction > 0 {
		severity := "medium"
		if len(array(audit["mcp_failures"])) > 0 || friction >= 3 {
			severity = "high"
		}
		insight("tooling", severity, "Capability friction detected")
	}
	network := object(fields["firewall_analysis"])
	total, blocked := engineCounter(network["total_requests"]), engineCounter(network["blocked_requests"])
	if total > 0 {
		severity, title := "info", "Network policy aligned"
		if blocked > 0 {
			severity, title = "medium", "Network friction detected"
			if float64(blocked)/float64(total) >= .5 || (blocked >= 10 && blocked < 50) {
				severity = "high"
			}
		}
		insight("network", severity, title)
	}
	if engineCounter(object(fields["redacted_domains_analysis"])["total_domains"]) > 0 {
		insight("privacy", "info", "Sensitive destinations were redacted")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	drain, err := drainAuditInsights(ctx, fields, limits)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	insights = append(insights, drain...)
	return findings, recommendations, insights, nil, nil
}
