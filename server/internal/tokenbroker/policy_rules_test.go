package tokenbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/admission"
)

const (
	testRuleRef = "acme/project/.github/workflows/triage.lock.yml@refs/heads/main"
	testRuleSHA = "0123456789abcdef0123456789abcdef01234567"
)

func rulesJSON(rule string) []byte {
	return []byte(`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[` + rule + `]}}}}`)
}

func validRule(repo string) string {
	return `{"repository-id":"` + repo + `","owner-id":"7","workflow-ref":"` + testRuleRef + `","workflow-sha":"` + testRuleSHA + `"}`
}

func TestParseWorkflowRulesAcceptsValidPolicy(t *testing.T) {
	rules, err := parseWorkflowRules(rulesJSON(validRule("1") + "," + validRule("2")))
	if err != nil || len(rules) != 2 || rules[1].RepositoryID != "2" {
		t.Fatalf("rules=%v err=%v", rules, err)
	}
	rules, err = parseWorkflowRules(rulesJSON(""))
	if err != nil || len(rules) != 0 {
		t.Fatalf("empty rules=%v err=%v", rules, err)
	}
}

func TestParseWorkflowRulesRejectsUnsafePolicy(t *testing.T) {
	many := strings.TrimSuffix(strings.Repeat(validRule("1")+",", 129), ",")
	for name, data := range map[string][]byte{
		"duplicate rule":  rulesJSON(validRule("1") + "," + validRule("1")),
		"too many":        rulesJSON(many),
		"wildcard repo":   rulesJSON(validRule("*")),
		"missing field":   rulesJSON(`{"repository-id":"1","owner-id":"7","workflow-ref":"` + testRuleRef + `"}`),
		"not an object":   []byte(`[]`),
		"duplicate field": []byte(`{"control-plane":{},"control-plane":{}}`),
		"invalid UTF-8":   {0xff},
	} {
		if rules, err := parseWorkflowRules(data); err == nil || rules != nil {
			t.Fatalf("%s: accepted rules=%v", name, rules)
		}
	}
}

func TestRuleAllowsRequiresExactMatch(t *testing.T) {
	rules, err := parseWorkflowRules(rulesJSON(validRule("1")))
	if err != nil {
		t.Fatal(err)
	}
	id := admission.Identity{RepositoryID: "1", OwnerID: "7", WorkflowRef: testRuleRef, WorkflowSHA: testRuleSHA}
	if !ruleAllows(rules, id) {
		t.Fatal("exact identity denied")
	}
	for name, edit := range map[string]func(*admission.Identity){
		"repository": func(i *admission.Identity) { i.RepositoryID = "2" },
		"owner":      func(i *admission.Identity) { i.OwnerID = "8" },
		"ref":        func(i *admission.Identity) { i.WorkflowRef += "x" },
		"sha":        func(i *admission.Identity) { i.WorkflowSHA = strings.Repeat("a", 40) },
	} {
		changed := id
		edit(&changed)
		if ruleAllows(rules, changed) {
			t.Fatalf("%s mismatch authorized", name)
		}
	}
	if ruleAllows(nil, id) {
		t.Fatal("empty rule set authorized")
	}
}

func TestReadPolicyFileBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if _, err := readPolicyFile(path); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readPolicyFile(path); err != nil || string(data) != "{}" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPolicyFile(path); err == nil {
		t.Fatal("oversized policy accepted")
	}
	if err := os.WriteFile(path, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPolicyFile(path); err != nil {
		t.Fatalf("policy at the bound rejected: %v", err)
	}
}
