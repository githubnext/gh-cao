package hostedcli

import (
	"strings"
	"testing"
)

func TestLocalMaintenanceExactTargetsAndHostedSeparation(t *testing.T) {
	for _, command := range []string{
		"gh aw update review.md --repo octo/service --create-pull-request",
		"gh aw update review.v2.md --repo octo/service --create-pull-request",
		"gh aw update .github/workflows/review.md --repo octo/service --create-pull-request",
		"gh aw update --repo octo/service --create-pull-request",
		"gh aw disable review.md --repo octo/service",
		"gh aw disable review.v2.md --repo octo/service",
		"gh aw disable .github/workflows/review.md --repo octo/service",
		"gh aw disable --repo octo/service",
	} {
		plan, err := PlanLocalMaintenance(command)
		if err != nil || plan.Repository != "octo/service" {
			t.Fatalf("exact local maintenance rejected: %s: %v", command, err)
		}
		if strings.Contains(command, "disable review.md") || strings.Contains(command, "disable .github") {
			if strings.Join(plan.Args, " ") != "disable review --repo octo/service" {
				t.Fatalf("disable widened the exact workflow: %v", plan.Args)
			}
		}
		if _, err := PlanCommand(command, "octo/control"); err == nil {
			t.Fatalf("local maintenance expanded hosted authority: %s", command)
		}
		for _, target := range []string{"review.md", "review.v2.md", ".github/workflows/review.md"} {
			plan, err := PlanCommand("gh aw update "+target+" --repo octo/control --create-pull-request", "octo/control")
			if err != nil || plan.Kind != KindGhAwUpdate || plan.Repository != "octo/control" {
				t.Fatalf("exact hosted control-repository update unavailable: %v", err)
			}
		}
	}
	for _, command := range []string{
		"gh aw disable", "gh aw disable --org octo", "gh aw disable review* --repo octo/service",
		"gh aw disable review.md other.md --repo octo/service",
		"gh aw disable review.md --repo octo/service --repo octo/other",
		"gh aw disable ../review.md --repo octo/service",
		"gh aw disable review..v2.md --repo octo/service",
		"gh aw update review.md --repo octo/service",
		"gh aw upgrade review.md --repo octo/service --create-pull-request",
		"gh aw update https://evil.example/package --repo octo/service --create-pull-request",
	} {
		if _, err := PlanLocalMaintenance(command); err == nil {
			t.Fatalf("unsafe local maintenance accepted: %s", command)
		}
	}
}
