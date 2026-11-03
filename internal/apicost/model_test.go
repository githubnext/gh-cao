package apicost

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDailyFileDietRetainedCapacity(t *testing.T) {
	scenario, err := DailyFileDietScenario(DailyFileDietOptions{
		Repositories: pointer(100),
		Population: &Population{
			Outputs: 95, Implementations: 51,
			ObservedAt: "2026-10-18T12:05:52.580Z", WindowEnd: "2026-10-18T11:54:09Z",
			Source: "canonical database revision 17",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Detail["trackedIssues"] != 95 || scenario.Detail["corePerWorkflow"] != 99 ||
		scenario.Detail["searchRequestsPerWorkflow"] != 6 ||
		scenario.Detail["graphqlRequestsPerWorkflow"] != 10 ||
		scenario.Detail["totalRequestsPerWorkflow"] != 115 {
		t.Fatalf("unexpected Daily File Diet detail: %#v", scenario.Detail)
	}
	wantViolations := []string{"core:peak", "reviewed-collector-grant:modeled-admission", "runner:peak"}
	if report.Requested.Supported || !equalStrings(report.Requested.Violations, wantViolations) {
		t.Fatalf("requested capacity=%+v", report.Requested)
	}
	if report.Capacity.ModeledMaximumRepositories != 6 ||
		report.Capacity.GuaranteedMaximumRepositories != 4 ||
		!equalStrings(report.Capacity.Bottlenecks, []string{"runner:peak"}) {
		t.Fatalf("capacity=%+v", report.Capacity)
	}
}

func TestDailyFileDietRetainedRequiresCanonicalPopulation(t *testing.T) {
	if _, err := DailyFileDietScenario(DailyFileDietOptions{}); err == nil ||
		!strings.Contains(err.Error(), "canonical database evidence") {
		t.Fatalf("error=%v", err)
	}
}

func TestDailyFileDietHardBound(t *testing.T) {
	scenario, err := DailyFileDietScenario(DailyFileDietOptions{Scenario: "hard-bound"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Detail["totalRequestsPerWorkflow"] != 4084 ||
		report.Requested.Pools["search"].PeakDemandPerWindow != 6 ||
		!report.Requested.Admissions[0].GrantExceeded ||
		report.Capacity.ModeledMaximumRepositories != 0 ||
		report.Capacity.GuaranteedMaximumRepositories != 0 {
		t.Fatalf("hard-bound report=%+v detail=%#v", report, scenario.Detail)
	}
}

func TestCustomScenariosModelPeakAndCache(t *testing.T) {
	base := Scenario{
		SchemaVersion: 1, Name: "Repository metadata", Repositories: 100,
		WorkflowsPerRepository: 1, HorizonHours: 24, CollectionWindowHours: 1,
		Quotas: map[string]Quota{"core": {Limit: 100, Reserve: 20, WindowHours: 1}},
		Operations: []Operation{{
			ID: "repository-read", Pool: "core", CadenceHours: 24, BurstHours: 1,
			RequestCount: 1, UnitCost: 1, Scale: Scale{PerRepository: 10},
		}},
	}
	oneHour, err := Simulate(base)
	if err != nil {
		t.Fatal(err)
	}
	base.CollectionWindowHours = 2
	twoHours, err := Simulate(base)
	if err != nil {
		t.Fatal(err)
	}
	if oneHour.Capacity.ModeledMaximumRepositories != 8 ||
		!equalStrings(oneHour.Capacity.Bottlenecks, []string{"core:peak"}) ||
		twoHours.Capacity.ModeledMaximumRepositories != 16 {
		t.Fatalf("oneHour=%+v twoHours=%+v", oneHour.Capacity, twoHours.Capacity)
	}
	base.CollectionWindowHours = 1
	base.Repositories = 16
	base.Operations[0].CacheHitRate = .5
	cached, err := Simulate(base)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Requested.Supported || cached.Requested.Pools["core"].PeakDemandPerWindow != 80 ||
		cached.Capacity.ModeledMaximumRepositories != 16 {
		t.Fatalf("cached=%+v", cached)
	}
}

func TestMarkdownIncludesCapacity(t *testing.T) {
	report, err := Simulate(Scenario{
		SchemaVersion: 1, Name: "Workflow registry", Repositories: 5,
		WorkflowsPerRepository: 1, HorizonHours: 24, CollectionWindowHours: 1,
		Quotas: map[string]Quota{"core": {Limit: 100, Reserve: 20, WindowHours: 1}},
		Operations: []Operation{{
			ID: "workflow-pages", Pool: "core", CadenceHours: 24, BurstHours: 1,
			RequestCount: 1, UnitCost: 1, Scale: Scale{PerRepository: 2},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if markdown := FormatMarkdown(report); !strings.Contains(markdown, "Modeled maximum: **40 repositories**") {
		t.Fatal(markdown)
	}
}

func TestCustomScenarioRejectsNegativeScale(t *testing.T) {
	_, err := Simulate(Scenario{
		SchemaVersion: 1, Name: "Invalid", Repositories: 1,
		WorkflowsPerRepository: 1, HorizonHours: 24, CollectionWindowHours: 1,
		Quotas: map[string]Quota{"core": {Limit: 100, WindowHours: 1}},
		Operations: []Operation{{
			ID: "negative", Pool: "core", CadenceHours: 24, BurstHours: 1,
			RequestCount: 1, UnitCost: 1, Scale: Scale{PerRepository: -1},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "negative.scale.perRepository") {
		t.Fatalf("error=%v", err)
	}
}

func TestCustomScenarioJSONDistinguishesOmittedAndExplicitZero(t *testing.T) {
	var zeroRepositories Scenario
	if err := json.Unmarshal([]byte(`{
		"schemaVersion":1,"name":"Invalid zero","repositories":0,
		"quotas":{"core":{"limit":100}},
		"operations":[{"id":"read","pool":"core","scale":{"perRepository":1}}]
	}`), &zeroRepositories); err != nil {
		t.Fatal(err)
	}
	if _, err := Simulate(zeroRepositories); err == nil || !strings.Contains(err.Error(), "repositories") {
		t.Fatalf("error=%v", err)
	}

	var zeroCost Scenario
	if err := json.Unmarshal([]byte(`{
		"schemaVersion":1,"name":"Zero cost",
		"quotas":{"core":{"limit":100}},
		"operations":[{
			"id":"cached","pool":"core","scale":{"perRepository":1},
			"requestCount":0,"unitCost":0
		}]
	}`), &zeroCost); err != nil {
		t.Fatal(err)
	}
	report, err := Simulate(zeroCost)
	if err != nil {
		t.Fatal(err)
	}
	if zeroCost.Repositories != 1 || zeroCost.HorizonHours != 24 ||
		zeroCost.Operations[0].RequestCount != 0 || zeroCost.Operations[0].UnitCost != 0 ||
		report.Requested.Pools["core"].DemandOverHorizon != 0 {
		t.Fatalf("scenario=%+v report=%+v", zeroCost, report)
	}
}

func pointer[T any](value T) *T {
	return &value
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
