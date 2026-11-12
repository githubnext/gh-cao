package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func ncFixture() ncData {
	data := ncData{
		campaigns: []map[string]any{
			{"id": "campaign:maintenance", "slug": "maintenance", "mode": "review", "enabled": true, "targets": []any{ncRecord{"repository": "octo/expected"}}, "monthlyAiCreditBudget": 0},
			{"id": "campaign:blocked", "slug": "blocked"},
			{"id": "campaign:unobserved", "slug": "unobserved"},
		},
		repositories: []map[string]any{{"id": "repository:expected", "owner": "octo", "name": "expected"}, {"id": "repository:extra", "fullName": "octo/extra"}},
		workflows: []map[string]any{
			{"id": "workflow:orchestrator", "campaignId": "campaign:maintenance", "role": "orchestrator", "state": "active"},
			{"id": "workflow:worker", "campaignId": "campaign:maintenance", "role": "worker", "state": "active", "maxAiCredits": 123},
			{"id": "workflow:recovery", "campaignId": "campaign:maintenance", "role": "worker"},
			{"id": "workflow:blocked", "campaignId": "campaign:blocked", "role": "orchestrator"},
			{"id": "workflow:blocked-worker", "campaignId": "campaign:blocked", "role": "worker"},
			{"id": "workflow:unobserved", "campaignId": "campaign:unobserved", "role": "orchestrator"},
		},
	}
	run := func(id int, workflow, conclusion, target string) ncRecord {
		return ncRecord{
			"id": fmt.Sprintf("github:run:%d:attempt:1", id), "githubRunId": id, "workflowId": workflow, "attempt": 1,
			"startedAt": fmt.Sprintf("2026-10-01T10:%02d:00Z", id), "status": "completed", "conclusion": conclusion,
			"targetRepository": target, "runLink": fmt.Sprintf("https://github.com/octo/control/actions/runs/%d", id),
			"engine": "copilot", "requestedModel": "model-1",
		}
	}
	data.runs = []map[string]any{
		run(1, "workflow:orchestrator", "success", ""),
		run(2, "workflow:worker", "success", "octo/expected"),
		run(3, "workflow:worker", "failure", "octo/expected"),
		run(4, "workflow:worker", "failure", "octo/expected"),
		run(5, "workflow:worker", "failure", "octo/expected"),
		run(6, "workflow:worker", "failure", "octo/extra"),
		run(7, "workflow:blocked", "failure", ""),
		run(8, "workflow:blocked-worker", "success", "octo/expected"),
		run(9, "workflow:recovery", "failure", "octo/expected"),
		run(10, "workflow:recovery", "", "octo/expected"),
	}
	data.runs[9]["status"], data.runs[9]["conclusion"] = "in_progress", nil
	data.runs[4]["requestedModel"] = "model-2"
	for _, row := range data.runs {
		if row["conclusion"] == "failure" {
			row["failureKind"], row["failureJob"], row["failureStep"] = "driver-exit", "agent", "Run agent"
		}
	}
	failedID := data.runs[4]["id"]
	data.audits = []map[string]any{{"id": "audit:1", "runId": failedID, "summary": "Post-run failed", "raw": "do not expose"}}
	data.tools = []map[string]any{{"id": "tool:1", "runId": failedID, "status": "error", "mcpServer": "github", "mcpTool": "get_file_contents", "arguments": "do not expose"}}
	data.domains = []map[string]any{{"id": "domain:1", "runId": failedID, "decision": "blocked", "domain": "example.com", "requestCount": 2}}
	data.issues = []map[string]any{{"id": "issue:1", "runId": failedID, "summary": "Review required", "body": "do not expose"}}
	return data
}

func ncDataJSON(data ncData) ncRecord {
	return ncRecord{
		"campaigns": data.campaigns, "workflows": data.workflows, "repositories": data.repositories, "runs": data.runs,
		"audits": data.audits, "tools": data.tools, "domains": data.domains, "issues": data.issues,
	}
}

// Frozen independent outputs captured from the pre-retirement contract.
func ncOracle(t *testing.T, operation string, input any) any {
	t.Helper()
	content, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	fixture, err := os.ReadFile(filepath.Join("testdata", "computation-contract", operation, hex.EncodeToString(digest[:1])[:1]+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]any
	if err := json.Unmarshal(fixture, &cases); err != nil {
		t.Fatal(err)
	}
	expected, ok := cases[operation+":"+hex.EncodeToString(digest[:])]
	if !ok {
		t.Fatalf("no independent contract fixture for %s / %x", operation, digest)
	}
	return expected
}

func ncAssertJSON(t *testing.T, got, want any) {
	t.Helper()
	normalize := func(value any) any {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var result any
		if err := json.Unmarshal(content, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	a, b := normalize(got), normalize(want)
	var compare func(string, any, any)
	compare = func(path string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		if left, ok := a.(map[string]any); ok {
			if right, ok := b.(map[string]any); ok {
				for key := range left {
					if _, exists := right[key]; !exists {
						t.Fatalf("%s.%s: unexpected field", path, key)
					}
					compare(path+"."+key, left[key], right[key])
				}
				for key := range right {
					if _, exists := left[key]; !exists {
						t.Fatalf("%s.%s: missing field", path, key)
					}
				}
				return
			}
		}
		if left, ok := a.([]any); ok {
			if right, ok := b.([]any); ok && len(left) == len(right) {
				for i := range left {
					compare(fmt.Sprintf("%s[%d]", path, i), left[i], right[i])
				}
				return
			}
		}
		t.Fatalf("%s:\ngot  %#v\nwant %#v", path, a, b)
	}
	compare("$", a, b)
}

func TestNativeComputationProductionOracle(t *testing.T) {
	for _, slug := range []string{"", "maintenance", "blocked", "unobserved"} {
		t.Run("runtime-health/"+slug, func(t *testing.T) {
			data := ncFixture()
			got, err := ncRuntimeHealth(data, slug)
			if err != nil {
				t.Fatal(err)
			}
			got["diagnostics"] = ncDiagnostics(got, data)
			want := ncOracle(t, "runtime-health", ncRecord{"data": ncDataJSON(data), "options": ncRecord{"campaign": slug, "diagnose": true}})
			ncAssertJSON(t, got, want)
		})
		t.Run("intelligence/"+slug, func(t *testing.T) {
			data := ncFixture()
			health, err := ncRuntimeHealth(data, slug)
			if err != nil {
				t.Fatal(err)
			}
			contracts, err := ncCampaignContracts(data.campaigns, data.workflows)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ncIntelligence(health, contracts, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := ncOracle(t, "intelligence", ncRecord{"data": ncDataJSON(data), "options": ncRecord{"campaign": slug}})
			ncAssertJSON(t, got, want)
		})
	}
}

func TestNativeComputationFeedbackAndPreviousOracle(t *testing.T) {
	data := ncFixture()
	health, _ := ncRuntimeHealth(data, "")
	contracts, err := ncCampaignContracts(data.campaigns, data.workflows)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ncIntelligence(health, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision := ncRows(prior["decisions"])[0]
	for _, disposition := range []string{"accepted", "rejected", "deferred", "superseded", "recovered", "expired", "unresolved"} {
		t.Run(disposition, func(t *testing.T) {
			feedback := ncRecord{"contractVersion": "1.0.0", "records": []any{
				ncRecord{"decisionId": decision["decisionId"], "inputFingerprint": decision["inputFingerprint"], "disposition": disposition, "observedAt": "2026-10-02T10:00:00Z", "actor": "operator", "actualCost": ncRecord{"2": 1e-7, "1": 1e20, "text": "<&>\u2028"}},
				ncRecord{"decisionId": decision["decisionId"], "inputFingerprint": "sha256:" + strings.Repeat("0", 64), "disposition": "unresolved", "observedAt": "2026-10-02T11:00:00Z"},
				ncRecord{"decisionId": "unmatched", "inputFingerprint": decision["inputFingerprint"], "disposition": "unresolved", "observedAt": "2026-10-02T11:00:00Z"},
			}}
			previous := ncRecord{"command": "computation", "computation": "intelligence", "result": prior}
			got, err := ncIntelligence(health, contracts, previous, feedback)
			if err != nil {
				t.Fatal(err)
			}
			want := ncOracle(t, "intelligence", ncRecord{"data": ncDataJSON(data), "options": ncRecord{"previousResult": previous, "feedback": feedback}})
			ncAssertJSON(t, got, want)
			next, err := ncIntelligence(health, contracts, got, nil)
			if err != nil {
				t.Fatal(err)
			}
			want = ncOracle(t, "intelligence", ncRecord{"data": ncDataJSON(data), "options": ncRecord{"previousResult": got}})
			ncAssertJSON(t, next, want)
		})
	}
}

func TestNativeComputationMissingEvidenceAndBounds(t *testing.T) {
	data := ncFixture()
	data.runs = nil
	result, err := ncRuntimeHealth(data, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, campaign := range ncRows(result["campaignResults"]) {
		if campaign["answer"] != "unknown" || campaign["workerEvaluationState"] != "indeterminate-orchestrator" {
			t.Fatalf("missing evidence reported healthy: %#v", campaign)
		}
	}
	workflow := data.workflows[0]
	runs := []map[string]any{{"id": "unknown", "status": "completed", "conclusion": "success", "startedAt": "invalid"}}
	partition := ncPartition(data.campaigns[0], workflow, runs, nil, nil)
	if partition["answer"] != "unknown" || partition["latestSuccess"] != nil || partition["unknownDateRunCount"] != 1 {
		t.Fatalf("undated success inferred healthy: %#v", partition)
	}
	runs = []map[string]any{}
	for i := 0; i < 55; i++ {
		runs = append(runs, ncRecord{
			"id": fmt.Sprintf("run:%d", i), "githubRunId": i, "attempt": 1, "status": "completed", "conclusion": "failure",
			"startedAt": "2026-10-01T10:00:00Z", "failureKind": fmt.Sprintf("error-%d", i%25),
		})
	}
	partition = ncPartition(data.campaigns[0], workflow, runs, nil, nil)
	if partition["errorGroupCount"] != 25 || len(ncRows(partition["errorGroups"])) != 20 || partition["omittedErrorGroupCount"] != 5 {
		t.Fatalf("error groups not bounded: %#v", partition)
	}
	for _, run := range runs {
		run["failureKind"] = "same"
	}
	partition = ncPartition(data.campaigns[0], workflow, runs, nil, nil)
	group := ncRows(partition["errorGroups"])[0]
	if len(ncRows(group["runReferences"])) != 20 || group["omittedRunReferenceCount"] != 35 {
		t.Fatalf("run references not bounded: %#v", group)
	}
	contracts, _ := ncCampaignContracts(data.campaigns, data.workflows)
	intelligence, err := ncIntelligence(result, contracts, nil, nil)
	if err != nil || intelligence["decisionCount"] != 0 || intelligence["agentInvocations"] != 0 {
		t.Fatalf("missing evidence produced decisions: %#v, %v", intelligence, err)
	}
	if _, err := ncRuntimeHealth(data, "missing"); err == nil {
		t.Fatal("unknown campaign accepted")
	}
}

func TestNativeComputationAdvisorySafety(t *testing.T) {
	data := ncFixture()
	health, _ := ncRuntimeHealth(data, "")
	contracts, _ := ncCampaignContracts(data.campaigns, data.workflows)
	result, err := ncIntelligence(health, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, suppression := range ncRows(result["suppressions"]) {
		rules[ncString(suppression["rule"])] = true
	}
	if !rules["outside-declared-target-scope"] || !rules["recovery-in-progress"] {
		t.Fatalf("unsafe signals not suppressed: %#v", rules)
	}
	for _, decision := range ncRows(result["decisions"]) {
		recommendation := ncObject(decision["recommendation"])
		if recommendation["mode"] != "review" || recommendation["dispatchable"] != false {
			t.Fatalf("computation granted execution authority: %#v", recommendation)
		}
		if ncRows(decision["hardGates"])[0]["effect"] != "dispatch-prohibited" {
			t.Fatal("execution gate missing")
		}
	}
	diagnostics := ncDiagnostics(health, data)
	content, _ := json.Marshal(diagnostics)
	if strings.Contains(string(content), "do not expose") {
		t.Fatal("diagnostics exposed raw data outside contract")
	}
}

func TestNativeComputationInputValidation(t *testing.T) {
	for _, value := range []any{
		[]any{}, ncRecord{}, ncRecord{"contractVersion": "2.0.0", "records": []any{}},
		ncRecord{"contractVersion": "1.0.0", "records": []any{ncRecord{"decisionId": "d", "inputFingerprint": "bad", "disposition": "accepted", "observedAt": "2026-10-01T10:00:00Z"}}},
	} {
		if _, err := ncFeedback(value); err == nil {
			t.Fatalf("invalid feedback accepted: %#v", value)
		}
	}
	valid := ncRecord{"decisionId": "d", "inputFingerprint": "sha256:" + strings.Repeat("0", 64), "disposition": "accepted", "observedAt": "2026-10-01T10:00:00Z"}
	for _, modify := range []func(ncRecord){
		func(row ncRecord) { row["disposition"] = "approve-live" },
		func(row ncRecord) { row["observedAt"] = "invalid" },
		func(row ncRecord) { row["actor"] = true },
		func(row ncRecord) { row["rationale"] = strings.Repeat("😀", 2001) },
	} {
		record := ncCopy(valid)
		modify(record)
		if _, err := ncFeedback(ncRecord{"contractVersion": "1.0.0", "records": []any{record}}); err == nil {
			t.Fatalf("invalid feedback record accepted: %#v", record)
		}
	}
	if _, err := ncFeedback(ncRecord{"contractVersion": "1.0.0", "records": []any{valid, valid}}); err == nil {
		t.Fatal("duplicate feedback accepted")
	}
	for _, previous := range []any{[]any{}, ncRecord{}, ncRecord{"measureId": "portfolio-decisions", "measureVersion": "1.2.0", "decisions": []any{ncRecord{}}}} {
		if _, err := ncIntelligence(ncRecord{}, nil, previous, nil); err == nil {
			t.Fatalf("invalid previous result accepted: %#v", previous)
		}
	}
}

func ncInventoryFixture() ncRecord {
	stamp := "2026-10-01T10:00:00Z"
	return ncRecord{
		"campaigns": ncRecord{"metadata": ncRecord{"as-of": stamp}, "rows": []any{ncRecord{
			"campaign": "maintenance", "campaign-targets": []any{"octo/expected"}, "campaign-monthly-ai-credit-budget": 0,
			"campaign-intelligence-declaration": ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": ncRecord{"intendedOutcome": "Keep dependencies current", "stopConditions": []any{"stop"}}},
		}}},
		"repositories": ncRecord{"metadata": ncRecord{"as-of": stamp}, "rows": []any{ncRecord{"organization": "octo", "repository": "control"}}},
		"workflows": ncRecord{"metadata": ncRecord{"as-of": stamp}, "rows": []any{ncRecord{
			"organization": "octo", "repository": "control", "workflow": ".github/workflows/maintenance.lock.yml",
			"campaign": "maintenance", "workflow-role": "orchestrator", "workflow-active": "true", "max-ai-credits": 42,
		}}},
	}
}

func TestNativeComputationInventoryOracle(t *testing.T) {
	sources := ncInventoryFixture()
	data, err := ncInventory(sources)
	if err != nil {
		t.Fatal(err)
	}
	health, err := ncRuntimeHealth(data, "")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := ncCampaignContracts(data.campaigns, data.workflows)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ncIntelligence(health, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ncAssertJSON(t, result, ncOracle(t, "inventory", sources))
}

func TestNativeComputationContractsOracle(t *testing.T) {
	data := ncFixture()
	fields := ncRecord{}
	for _, field := range ncSemanticFields {
		fields[field] = ncRecord{"declared": field, "values": []any{0, false, "<&>\u2028", `literal \u003c`, "\x00\b\f\n\r\t\"\\", 1e-7, 1e-6, 1e20, 1e21}}
	}
	campaign := data.campaigns[0]
	campaign["observedAt"] = "2026-10-01T10:00:00+01:00"
	campaign["provenance"] = ncRecord{"source": "inventory"}
	campaign["intelligenceDeclaration"] = ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": fields}
	for _, workflow := range data.workflows {
		workflow["observedAt"] = "2026-10-01T10:30:00Z"
		workflow["provenance"] = ncRecord{"source": "inventory"}
	}
	got, err := ncCampaignContracts(data.campaigns, data.workflows)
	if err != nil {
		t.Fatal(err)
	}
	ncAssertJSON(t, got, ncOracle(t, "contracts", ncRecord{"campaigns": data.campaigns, "workflows": data.workflows}))
	if ncObject(got[1]["quality"])["completeness"] != "complete" {
		t.Fatal("fully declared contract not complete")
	}
	for _, declaration := range []any{
		ncRecord{"contractVersion": "2.0.0", "campaign": "maintenance", "fields": fields},
		ncRecord{"contractVersion": "1.0.0", "campaign": "different", "fields": fields},
		ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": ncRecord{}},
		ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": ncRecord{"intendedOutcome": nil}},
		ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": ncRecord{"unexpected": true}},
		ncRecord{"contractVersion": "1.0.0", "campaign": "maintenance", "fields": fields, "unexpected": true},
	} {
		campaign["intelligenceDeclaration"] = declaration
		if _, err := ncCampaignContracts(data.campaigns, data.workflows); err == nil {
			t.Fatalf("invalid declaration accepted: %#v", declaration)
		}
	}
}

func TestNativeComputationQualityOracle(t *testing.T) {
	for i, quality := range []any{
		nil, []any{}, ncRecord{},
		ncRecord{"availability": "available", "coverage": ncRecord{"state": "complete", "numerator": 2, "denominator": 1}},
		ncRecord{"freshness": ncRecord{"state": "fresh", "observedAt": "invalid"}, "coverage": nil, "maturity": true},
		ncRecord{"freshness": ncRecord{"state": "stale", "observedAt": "2026-10-01T10:00:00+01:00"}, "coverage": ncRecord{"state": "zero", "numerator": "0", "denominator": false}, "provenance": ncRecord{"state": "partial", "sources": []any{"one", "one", "", " two "}}},
		ncRecord{"coverage": ncRecord{"state": "partial", "numerator": ncRecord{}, "denominator": 1}, "provenance": ncRecord{"state": "available", "sources": []any{nil, ncRecord{}, []any{"a", "b"}, true}}},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			ncAssertJSON(t, ncQuality(quality), ncOracle(t, "quality", quality))
		})
	}
}

func TestNativeComputationContradictoryOrderingFailsClosed(t *testing.T) {
	data := ncFixture()
	data.runs[0]["runDate"] = "2026-10-01T01:00:00Z"
	data.runs = append(data.runs, ncRecord{
		"id": "run:contradiction", "workflowId": "workflow:orchestrator", "githubRunId": 100,
		"status": "completed", "conclusion": "failure", "startedAt": "2026-10-01T09:00:00Z",
		"runDate": "2026-10-01T12:00:00Z",
	})
	if _, err := ncRuntimeHealth(data, ""); err == nil || !strings.Contains(err.Error(), "newest-first") {
		t.Fatalf("contradictory ordering accepted: %v", err)
	}
}

func TestNativeComputationInventoryFailsClosed(t *testing.T) {
	for _, inventory := range []any{
		nil, []any{}, ncRecord{"campaigns": ncRecord{"rows": []any{ncRecord{"campaign": "missing-date"}}}},
		ncRecord{"campaigns": ncRecord{"metadata": ncRecord{"availability": "unavailable"}}},
		ncRecord{"tools": ncRecord{"metadata": ncRecord{"availability": "unavailable", "error": "incomplete tool evidence"}}},
	} {
		if _, err := ncInventory(inventory); err == nil {
			t.Fatalf("invalid or unavailable inventory accepted: %#v", inventory)
		}
	}
}

func TestNativeComputationIncompleteAndBoundedOracle(t *testing.T) {
	data := ncFixture()
	data.runs = data.runs[:1]
	for i := 0; i < 55; i++ {
		data.runs = append(data.runs, ncRecord{
			"id": fmt.Sprintf("run:%d", i), "githubRunId": i + 10, "attempt": 1, "status": "completed", "conclusion": "failure",
			"startedAt": "2026-10-01T10:01:00Z", "failureKind": fmt.Sprintf("error-%d", i%25),
			"workflowId": "workflow:worker", "targetRepository": "octo/expected",
		})
	}
	for _, stamp := range []any{nil, "", "invalid-a", "invalid-z"} {
		data.runs = append(data.runs, ncRecord{
			"id": fmt.Sprintf("unknown:%v", stamp), "githubRunId": 100, "attempt": 1,
			"status": "completed", "conclusion": "success", "startedAt": stamp,
			"workflowId": "workflow:worker", "targetRepository": "octo/expected",
		})
	}
	for i := 0; i < 25; i++ {
		data.audits = append(data.audits, ncRecord{
			"id": fmt.Sprintf("audit:%02d", i), "runId": "run:54", "summary": "bounded evidence", "timestamp": "2026-10-01T11:00:00Z",
		})
	}
	result, err := ncRuntimeHealth(data, "")
	if err != nil {
		t.Fatal(err)
	}
	result["diagnostics"] = ncDiagnostics(result, data)
	ncAssertJSON(t, result, ncOracle(t, "runtime-health", ncRecord{"data": ncDataJSON(data), "options": ncRecord{"diagnose": true}}))
	contracts, _ := ncCampaignContracts(data.campaigns, data.workflows)
	intelligence, err := ncIntelligence(result, contracts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ncAssertJSON(t, intelligence, ncOracle(t, "intelligence", ncRecord{"data": ncDataJSON(data)}))
}

func TestNativeComputationCLI(t *testing.T) {
	directory, err := os.MkdirTemp(".", ".native-computation-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	database := nativeTestSnapshot(t, nil)
	inventory := filepath.Join(directory, "inventory-sources.json")
	content, _ := json.Marshal(ncInventoryFixture())
	if err := os.WriteFile(inventory, content, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) (ncRecord, error) {
		command := &cobra.Command{Use: name}
		command.SetContext(context.Background())
		for _, flag := range []string{"postgres-url", "inventory", "campaign", "previous", "feedback"} {
			command.Flags().String(flag, "", "")
		}
		command.Flags().Bool("diagnose", false, "")
		if err := command.ParseFlags(append([]string{"--postgres-url", database, "--inventory", inventory}, args...)); err != nil {
			return nil, err
		}
		var output bytes.Buffer
		command.SetOut(&output)
		err := nativeComputation(command)
		var result ncRecord
		if err == nil {
			err = json.Unmarshal(output.Bytes(), &result)
		}
		return result, err
	}
	result, err := run("runtime-health", "--campaign", "maintenance", "--diagnose")
	if err != nil {
		t.Fatal(err)
	}
	if result["command"] != "computation" || result["computation"] != "runtime-health" || ncObject(result["result"])["measureVersion"] != "1.0.0" {
		t.Fatalf("invalid command output contract: %#v", result)
	}
	for _, args := range [][]string{
		{"--diagnose"}, {"--campaign", "missing"},
		{"--inventory", filepath.Join(directory, "missing.json")},
		{"--previous", filepath.Join(directory, "missing.json")},
	} {
		if _, err := run("runtime-health", args...); err == nil {
			t.Fatalf("invalid CLI accepted: %v", args)
		}
	}
	data, _ := ncInventory(ncInventoryFixture())
	runRecord := ncRecord{"id": "run:1", "repositoryId": data.workflows[0]["repositoryId"],
		"workflowId": data.workflows[0]["id"], "githubRunId": "1", "attempt": 1,
		"startedAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		"status":    "completed", "conclusion": "failure"}
	database = nativeTestSnapshot(t, map[string][]map[string]any{
		"repositories": {{"id": data.workflows[0]["repositoryId"], "owner": data.workflows[0]["organization"],
			"name": data.workflows[0]["repository"]}},
		"workflows": {{"id": data.workflows[0]["id"], "repositoryId": data.workflows[0]["repositoryId"],
			"path": data.workflows[0]["path"]}},
		"runs": {runRecord},
	})
	result, err = run("intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if ncObject(result["result"])["decisionCount"] != float64(1) {
		t.Fatalf("canonical snapshot evidence not evaluated: %#v", result)
	}
	previousPath := filepath.Join(directory, "previous.json")
	content, _ = json.Marshal(result)
	if err := os.WriteFile(previousPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = run("intelligence", "--previous", previousPath)
	if err != nil || len(ncArray(ncObject(result["result"])["reusedDecisionIds"])) != 1 {
		t.Fatalf("previous file not honored: %#v, %v", result, err)
	}
	decision := ncRows(ncObject(result["result"])["decisions"])[0]
	feedbackPath := filepath.Join(directory, "feedback.json")
	content, _ = json.Marshal(ncRecord{"contractVersion": "1.0.0", "records": []any{ncRecord{
		"decisionId": decision["decisionId"], "inputFingerprint": decision["inputFingerprint"], "disposition": "accepted", "observedAt": time.Now().UTC().Format(time.RFC3339),
	}}})
	if err := os.WriteFile(feedbackPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = run("intelligence", "--previous", previousPath, "--feedback", feedbackPath)
	if err != nil || ncObject(result["result"])["decisionCount"] != float64(0) || ncObject(ncObject(result["result"])["feedback"])["appliedCount"] != float64(1) {
		t.Fatalf("feedback file not honored: %#v, %v", result, err)
	}
	for _, flag := range []string{"inventory", "previous", "feedback"} {
		path := filepath.Join(directory, "malformed-"+flag+".json")
		if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := run("intelligence", "--"+flag, path); err == nil || !strings.Contains(err.Error(), "Unable to read") {
			t.Fatalf("malformed %s file error not explicit: %v", flag, err)
		}
		if _, err := run("intelligence", "--"+flag, path+".missing"); err == nil {
			t.Fatalf("missing %s file accepted", flag)
		}
	}
}
