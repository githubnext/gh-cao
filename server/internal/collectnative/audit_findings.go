package collectnative

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

func auditFinding(code, category, severity, title, description string) map[string]any {
	return map[string]any{"code": code, "category": category, "severity": severity, "title": title, "description": description}
}

func auditRecommendation(priority, action, reason string) map[string]any {
	return map[string]any{"priority": priority, "action": action, "reason": reason}
}

// These default rules consume only independently admitted counters/reports.
// Unsupported expanded audit rules must not become inferred zero totals.
func defaultAuditFindings(fields map[string]any) ([]any, []any) {
	findings, recommendations := []any{}, []any{}
	metrics := object(object(fields["audit"])["metrics"])
	if tokens := engineCounter(first(metrics["token_usage"], fields["token_usage"])); tokens > 50000 {
		findings = append(findings, auditFinding("high_token_usage", "performance", "medium", "High Token Usage",
			fmt.Sprintf("Used %s tokens", auditFormattedNumber(tokens))))
		// The pinned cost recommendation requires a cost-category finding;
		// high_token_usage is performance, not cost.
	}
	if turns := engineCounter(first(metrics["turns"], fields["turns"])); turns > 10 {
		findings = append(findings, auditFinding("many_iterations", "performance", "medium", "Many Iterations",
			fmt.Sprintf("Workflow took %d turns to complete", turns)))
		recommendations = append(recommendations, auditRecommendation("medium",
			"Clarify workflow instructions or break into smaller tasks",
			"Many iterations may indicate unclear objectives or overly complex tasks"))
	}
	audit := object(fields["audit"])
	if count := len(array(audit["errors"])); count > 5 {
		findings = append(findings, auditFinding("multiple_errors", "error", "high", "Multiple Errors",
			fmt.Sprintf("Encountered %d errors during execution", count)))
	}
	failures := array(audit["mcp_failures"])
	if len(failures) > 0 {
		servers := make([]string, 0, len(failures))
		for _, candidate := range failures {
			servers = append(servers, text(object(candidate)["server_name"]))
		}
		findings = append(findings, auditFinding("mcp_server_failures", "tooling", "high", "MCP Server Failures",
			"Failed MCP servers: "+strings.Join(servers, ", ")))
	}
	missing := array(audit["missing_tools"])
	if len(missing) > 0 {
		names := make([]string, 0, min(3, len(missing)))
		for _, candidate := range missing[:min(3, len(missing))] {
			names = append(names, text(object(candidate)["tool"]))
		}
		description := "Missing tools: " + strings.Join(names, ", ")
		if len(missing) > 3 {
			description += fmt.Sprintf(" (and %d more)", len(missing)-3)
		}
		findings = append(findings, auditFinding("tools_not_available", "tooling", "medium", "Tools Not Available", description))
		recommendations = append(recommendations, auditRecommendation("medium", "Add missing tools to workflow configuration",
			"Missing tools limit agent capabilities and may cause failures"))
	}
	if len(failures) > 0 {
		recommendations = append(recommendations, auditRecommendation("high", "Fix MCP server configuration or dependencies",
			"MCP server failures prevent agent from accessing required tools"))
	}
	firewall := object(fields["firewall_analysis"])
	if blocked := engineCounter(firewall["blocked_requests"]); blocked > 0 {
		domains := []string{}
		for _, domain := range slices.Sorted(maps.Keys(object(firewall["requests_by_domain"]))) {
			if domain != "-" && domain != "(unknown)" && engineCounter(object(object(firewall["requests_by_domain"])[domain])["blocked"]) > 0 {
				domains = append(domains, domain)
			}
		}
		description := fmt.Sprintf("%d network request(s) were blocked by firewall", blocked)
		switch {
		case len(domains) == 1:
			description = "Agent attempted to access blocked domain: " + domains[0]
		case len(domains) > 1 && len(domains) <= 3:
			description = "Agent attempted to access blocked domains: " + strings.Join(domains, ", ")
		case len(domains) > 3:
			description = fmt.Sprintf("Agent attempted to access %d blocked domains, including: %s", len(domains), strings.Join(domains[:3], ", "))
		}
		findings = append(findings, auditFinding("blocked_network_requests", "network", "medium", "Blocked Network Requests", description))
		recommendations = append(recommendations, auditRecommendation("medium", "Add blocked domains to the workflow network allow-list",
			"Firewall-blocked domains prevent the agent from completing its tasks"))
	}
	return findings, recommendations
}

func auditFormattedNumber(n int) string {
	text := fmt.Sprint(n)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}
