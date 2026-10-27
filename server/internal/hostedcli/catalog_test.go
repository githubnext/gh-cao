package hostedcli

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func loadTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := LoadCatalog(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func testContext(t *testing.T) TemplateContext {
	t.Helper()
	context, err := NewTemplateContext("octo/cao", "https://github.com", "https://cao.example.com/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	return context
}

func TestResolveDeclaredUpgradeProducesHostedPlan(t *testing.T) {
	catalog := loadTestCatalog(t)
	invocation, err := catalog.Resolve(Request{
		ID: "upgrade-target-repository", Values: map[string]string{"repository": "octo/app"},
		Arguments: map[string]bool{"auto-merge": true},
	}, testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(invocation.Command, "https://github.com/octo/cao") ||
		!strings.Contains(invocation.Command, "https://cao.example.com/dashboard/#page-maintenance") {
		t.Fatalf("context was not rendered: %s", invocation.Command)
	}
	plan, err := PlanCommand(invocation.Command, "octo/cao")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != KindGhAwUpgrade || plan.Repository != "octo/app" || plan.Program != "gh-aw" {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if !slices.Contains(plan.Args, "--create-pull-request") || !slices.Contains(plan.Args, "--auto-merge") {
		t.Fatalf("publishing flags missing: %v", plan.Args)
	}
	if plan.Permissions["contents"] != "write" || plan.Permissions["pull_requests"] != "write" || plan.Permissions["issues"] != "" {
		t.Fatalf("unexpected permissions %v", plan.Permissions)
	}
}

func TestResolveRejectsInvalidRequests(t *testing.T) {
	catalog := loadTestCatalog(t)
	context := testContext(t)
	cases := []struct {
		name    string
		request Request
		status  int
	}{
		{"unknown", Request{ID: "missing-action"}, 404},
		{"copy-only", Request{ID: "add-marketplace-package", Values: map[string]string{"package-source": "a/b"}}, 403},
		{"undeclared argument", Request{ID: "upgrade-target-repository", Values: map[string]string{"repository": "o/r"}, Arguments: map[string]bool{"force": true}}, 400},
		{"missing value", Request{ID: "upgrade-target-repository"}, 400},
		{"extra value", Request{ID: "upgrade-target-repository", Values: map[string]string{"repository": "o/r", "x": "y"}}, 400},
		{"injection", Request{ID: "upgrade-target-repository", Values: map[string]string{"repository": "o/r; rm -rf /"}}, 400},
		{"bad id", Request{ID: "../x"}, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := catalog.Resolve(test.request, context)
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.Status != test.status {
				t.Fatalf("want status %d, got %v", test.status, err)
			}
		})
	}
}

func TestResolveDeclaredSourceUpdatesUseNativeGhAwCommand(t *testing.T) {
	catalog := loadTestCatalog(t)
	for _, test := range []struct {
		name, id, prefix string
		values           map[string]string
	}{
		{"campaign", "update-campaign", "gh aw update https://github.com/githubnext/gh-cao/security --repo octo/cao",
			map[string]string{"campaign-name": "Security", "campaign-source": "githubnext/gh-cao/security"}},
		{"standalone workflow", "update-workflow-source", "gh aw update https://github.com/octo/workflows/review --repo octo/cao",
			map[string]string{"workflow-name": "Review", "workflow-source": "octo/workflows/review"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation, err := catalog.Resolve(Request{
				ID: test.id, Values: test.values,
				Arguments: map[string]bool{"create-pull-request": true},
			}, testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(invocation.Command, "cao.sh") || !strings.HasPrefix(invocation.Command, test.prefix) {
				t.Fatalf("source update must use the native gh-aw command: %s", invocation.Command)
			}
			plan, err := PlanCommand(invocation.Command, "octo/cao")
			if err != nil {
				t.Fatal(err)
			}
			if plan.Kind != KindGhAwUpdate || plan.Program != "gh-aw" || plan.Repository != "octo/cao" {
				t.Fatalf("unexpected hosted source update plan %+v", plan)
			}
		})
	}
}

func TestResolveNativeCaoActionsPreservesTheHostedExecutionBoundary(t *testing.T) {
	catalog := loadTestCatalog(t)
	for _, tc := range []struct {
		id, field, value, command string
	}{
		{"set-campaign-live", "campaign", "security", "gh cao campaigns mode live security"},
		{"set-campaign-preview", "campaign", "security", "gh cao campaigns mode preview security"},
		{"enable-campaign", "campaign", "security", "gh cao campaigns enable security"},
		{"disable-campaign", "campaign", "security", "gh cao campaigns disable security"},
		{"add-repository", "repository", "octo/app", "gh cao repos add octo/app"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			invocation, err := catalog.Resolve(Request{
				ID: tc.id, Values: map[string]string{tc.field: tc.value},
			}, testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			if invocation.Command != tc.command {
				t.Fatalf("native command = %q, want %q", invocation.Command, tc.command)
			}
			if _, err := PlanCommand(invocation.Command, "octo/cao"); !errors.Is(err, ErrNotHosted) {
				t.Fatalf("local-checkout mutation must not gain hosted authority: %v", err)
			}
		})
	}
}

func TestPlanCommandAllowlist(t *testing.T) {
	update, err := PlanCommand("gh aw update https://github.com/githubnext/gh-cao/security --repo octo/cao --pr-message 'msg' --create-pull-request --auto-merge", "octo/cao")
	if err != nil {
		t.Fatal(err)
	}
	if update.Kind != KindGhAwUpdate || update.Program != "gh-aw" || update.Repository != "octo/cao" {
		t.Fatalf("unexpected update plan %+v", update)
	}
	if strings.Join(update.Args, " ") != "update https://github.com/githubnext/gh-cao/security --repo octo/cao --pr-message msg --create-pull-request --auto-merge" {
		t.Fatalf("unexpected update args %v", update.Args)
	}
	if _, err := PlanCommand("gh aw update https://github.com/github/.github/workflows/review --repo octo/cao --create-pull-request", "octo/cao"); err != nil {
		t.Fatalf("dot-prefixed GitHub repository source was rejected: %v", err)
	}
	run, err := PlanCommand("gh workflow run cao-evolution.lock.yml -f mode=review", "octo/cao")
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != KindWorkflowRun || run.Repository != "octo/cao" || run.Permissions["actions"] != "write" || run.Permissions["contents"] != "read" {
		t.Fatalf("unexpected workflow plan %+v", run)
	}
	if !slices.Contains(run.Args, "--repo") {
		t.Fatalf("workflow argv must name the repository explicitly: %v", run.Args)
	}

	for _, command := range []string{
		"./cao.sh update --campaign security",
		"./cao.sh mode live security",
		"gh aw update https://attacker.example/repo/security --repo octo/cao --create-pull-request",
		"gh aw update https://github.com/attacker/repo/../security --repo octo/cao --create-pull-request",
		"gh aw update https://github.com/githubnext/gh-cao/security --repo octo/other --create-pull-request",
		"gh agent-task create --from-file -",
		"gh aw upgrade --repo octo/app",
		"gh aw upgrade --repo octo/app --auto-merge",
		"gh aw upgrade --repo octo/app --create-pull-request --force",
		"gh api repos/octo/app",
		"./cao.sh update --campaign Bad_Slug --create-pull-request",
		"gh workflow run w.yml -f 'bad key=x'",
	} {
		if _, err := PlanCommand(command, "octo/cao"); err == nil {
			t.Errorf("expected rejection for %q", command)
		}
	}
	if _, err := PlanCommand("gh api /user", "octo/cao"); !errors.Is(err, ErrNotHosted) {
		t.Fatalf("want ErrNotHosted, got %v", err)
	}
	if _, err := PlanCommand("gh aw update https://github.com/githubnext/gh-cao/security --repo octo/cao --create-pull-request", ""); err == nil {
		t.Fatal("gh-aw updates require a configured control repository")
	}
}
