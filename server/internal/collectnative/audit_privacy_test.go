package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNativeAgentFirewallWarningsPinnedUpstream(t *testing.T) {
	// gh-aw v1.0.1 firewall_log.go deduplicates stderr domains, then adds
	// these requests to raw access logs; a usage summary is fallback only.
	for _, withRaw := range []bool{false, true} {
		t.Run(map[bool]string{false: "warnings-replace-summary", true: "warnings-add-to-raw"}[withRaw], func(t *testing.T) {
			fields := map[string]any{"firewall_analysis": map[string]any{"total_requests": 100}}
			files := map[string][]byte{
				"agent-stdio.log": []byte("blocked: --allow-domains \"api.example.com,blocked.example.net\"\n" +
					"blocked again: --allow-domains api.example.com\n"),
			}
			want := 2
			if withRaw {
				files["sandbox/firewall/logs/squid-logs/access.log"] = []byte(
					"1761332531.123 172.30.0.20:35289 api.example.com 140.82.112.23:443 1.1 CONNECT 403 NONE_NONE:HIER_NONE api.example.com:443 \"-\"\n")
				want++
			}
			gaps, err := enrichNetworkAudit(context.Background(), fields, files, Limits{})
			analysis := object(fields["firewall_analysis"])
			if err != nil || len(gaps) != 0 || engineCounter(analysis["total_requests"]) != want ||
				engineCounter(analysis["blocked_requests"]) != want {
				t.Fatalf("analysis=%#v gaps=%v err=%v", analysis, gaps, err)
			}
			native := map[string]any{"audit": map[string]any{}}
			native["firewall_analysis"] = analysis
			findings, _ := defaultAuditFindings(native)
			if len(findings) != 1 || text(object(findings[0])["code"]) != "blocked_network_requests" ||
				!strings.Contains(text(object(findings[0])["description"]), "api.example.com") ||
				!strings.Contains(text(object(findings[0])["description"]), "blocked.example.net") {
				t.Fatalf("deduplicated blocked findings=%#v", findings)
			}
		})
	}
}

func TestNativeNetworkCombinedBudgetAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := enrichNetworkAudit(ctx, map[string]any{}, nil, Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled network: %v", err)
	}
	files := map[string][]byte{"agent-stdio.log": []byte("--allow-domains api.example.com\n\n"),
		"sandbox/firewall/logs/squid-logs/access.log": []byte("\n\n")}
	if _, err := enrichNetworkAudit(context.Background(), map[string]any{}, files, Limits{Entries: 3}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("combined budget: %v", err)
	}
	fields := map[string]any{}
	gaps, err := enrichNetworkAudit(context.Background(), fields, map[string][]byte{
		"agent-stdio.log": []byte("--allow-domains https://user:secret@example.com/private\n"),
	}, Limits{})
	if err != nil || !slices.Contains(gaps, "invalid-agent-firewall-log") || fields["firewall_analysis"] != nil {
		t.Fatalf("unsafe warning: fields=%#v gaps=%v err=%v", fields, gaps, err)
	}
}

func TestNativeRedactedDomainsPinnedUpstream(t *testing.T) {
	fields := map[string]any{}
	files := map[string][]byte{
		"agent_outputs/tmp/gh-aw/redacted-urls.log": []byte(
			"# one domain per line\n\n private.example.com \nsecret.internal\nprivate.example.com\n"),
	}
	gaps, err := enrichPrivacyAudit(context.Background(), fields, files, Limits{})
	if err != nil || len(gaps) != 0 || engineCounter(object(fields["redacted_domains_analysis"])["total_domains"]) != 2 {
		t.Fatalf("privacy fields=%#v gaps=%v err=%v", fields, gaps, err)
	}
	fields["audit"] = map[string]any{"native_tool_types": 0}
	_, _, insights, gaps, err := agenticAudit(context.Background(), fields, nil, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 {
		t.Fatalf("privacy insight gaps=%v err=%v", gaps, err)
	}
	found := false
	for _, value := range insights {
		if text(object(value)["title"]) == "Sensitive destinations were redacted" && text(object(value)["severity"]) == "info" {
			found = true
		}
	}
	encoded, err := json.Marshal(fields)
	if !found || err != nil || strings.Contains(string(encoded), "private.example.com") || strings.Contains(string(encoded), "secret.internal") {
		t.Fatalf("unsafe or missing privacy insight: insights=%#v fields=%s err=%v", insights, encoded, err)
	}
	delete(fields, "audit")
	_, _, insights, gaps, err = agenticAudit(context.Background(), fields, nil, testIdentity(), Limits{})
	if err != nil || !slices.Contains(gaps, "agentic-tool-metrics-unavailable") || len(insights) != 1 ||
		text(object(insights[0])["title"]) != "Sensitive destinations were redacted" {
		t.Fatalf("unrelated tool gap dropped known redaction evidence: insights=%#v gaps=%v err=%v", insights, gaps, err)
	}
	files["another/redacted-urls.log"] = []byte("other.internal\n")
	gaps, err = enrichPrivacyAudit(context.Background(), map[string]any{}, files, Limits{})
	if err != nil || !slices.Contains(gaps, "ambiguous-redacted-domains-log") {
		t.Fatalf("ambiguous privacy source: gaps=%v err=%v", gaps, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := enrichPrivacyAudit(ctx, fields, nil, Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled privacy: %v", err)
	}
}
