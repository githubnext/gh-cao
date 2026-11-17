package hostedcli

import (
	"strings"
	"testing"
)

func TestLocalModernizationNarrowPlanningAndHostedSeparation(t *testing.T) {
	for _, command := range []string{
		"gh cao modernization status --repo octodemo/control",
		"gh cao modernization preview --repo octodemo/control --action assess --mode review",
		"gh cao modernization preview --repo octodemo/control --action approve-plan --mode review",
		"gh cao modernization preview --repo octodemo/control --action run --mode live",
		"gh cao modernization preview --repo octodemo/control --action verify --mode review",
		"gh cao modernization execute --repo octodemo/control --preview " + strings.Repeat("a", 48),
	} {
		plan, err := PlanLocalOperator(command)
		if err != nil || plan.Kind != KindModernization || plan.Repository != "octodemo/control" {
			t.Fatalf("domain command rejected: %s: %#v %v", command, plan, err)
		}
		if _, err := PlanCommand(command, "octodemo/control"); err == nil {
			t.Fatalf("modernization expanded hosted authority: %s", command)
		}
		if _, err := PlanLocalMaintenance(command); err == nil {
			t.Fatalf("modernization expanded maintenance authority: %s", command)
		}
	}
	for _, suffix := range []string{
		"status --repo octodemo/*", "status --repo octodemo/../other", "status --repo octodemo/control --force",
		"preview --repo octodemo/control --action install --mode review",
		"preview --repo octodemo/control --action assess --mode live",
		"preview --repo octodemo/control --action verify --mode live",
		"preview --repo octodemo/control --action run --mode debug",
		"execute --repo octodemo/control --preview main", "run --repo octodemo/control",
	} {
		if _, err := PlanLocalOperator("gh cao modernization " + suffix); err == nil {
			t.Fatalf("unsafe domain command admitted: %s", suffix)
		}
	}
}

func TestModernizationCatalogInputOnlyForConcreteApproval(t *testing.T) {
	catalog, err := ParseCatalog([]byte(`{"dashboard":{"cli-actions":[
{"id":"modernization-status","command":"gh cao modernization status --repo {{repository}}"},
{"id":"modernization-preview","command":"gh cao modernization preview --repo {{repository}} --action {{operation}} --mode {{mode}}"},
{"id":"modernization-execute","command":"gh cao modernization execute --repo {{repository}} --preview {{preview}}"}
]}}`))
	if err != nil {
		t.Fatal(err)
	}
	input := `{"schema":"cao-modernization/v1"}`
	for _, action := range []string{"assess", "approve-plan", "run", "verify"} {
		request := Request{ID: "modernization-preview", Values: map[string]string{
			"repository": "octodemo/control", "operation": action, "mode": "review",
		}}
		if _, err := catalog.Resolve(request, nil); (err == nil) != (action != "approve-plan") {
			t.Fatalf("approval input requirement lost: %s %v", action, err)
		}
		request.Input = &input
		if _, err := catalog.Resolve(request, nil); (err == nil) != (action == "approve-plan") {
			t.Fatalf("input admitted to wrong operation: %s %v", action, err)
		}
	}
}
