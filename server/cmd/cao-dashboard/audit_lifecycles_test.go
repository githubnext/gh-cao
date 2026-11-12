package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/auditlifecycle"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestAuditLifecyclesPostgresReadOnly(t *testing.T) {
	store, _, _ := operationalCommandFixture(t)
	defer func() { _ = store.Close() }()
	attrs := map[string]string{"rebuild_id": strings.Repeat("a", 64), "state": "running"}
	if _, err := store.AppendAudit(t.Context(), operational.AuditEvent{
		Event: "admin.rebuild.admitted", Outcome: "success", Attributes: attrs,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.VerifyAudit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A writable verify/mirror path would create this file. The lifecycle
	// command must neither append audit rows nor open the JSON mirror.
	mirror := filepath.Join(t.TempDir(), "must-not-exist.jsonl")
	t.Setenv("CAO_AUDIT_LOG", mirror)
	output, err := runAudit(t, "lifecycles")
	var result auditlifecycle.Result
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.Status != "ok" || result.Pending != 1 {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Fatalf("read-only scan opened mirror: %v", err)
	}
	after, err := store.VerifyAudit(t.Context())
	if err != nil || before != after {
		t.Fatalf("scan changed history: before=%+v after=%+v err=%v", before, after, err)
	}
	attrs["state"] = "canceled"
	if _, err := store.AppendAudit(t.Context(), operational.AuditEvent{
		Event: "admin.rebuild.completed", Outcome: "failure", Attributes: attrs,
	}); err != nil {
		t.Fatal(err)
	}
	output, err = runAudit(t, "lifecycles")
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.Completed != 1 {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = runAudit(t, "lifecycles", "--max-events", "1")
	if err == nil || json.Unmarshal([]byte(output), &result) != nil || result.Status != "incomplete" || result.MissingTerminal != 0 {
		t.Fatalf("truncated output=%q err=%v", output, err)
	}
	output, err = runAudit(t, "lifecycles", "--after", "1")
	if err == nil || json.Unmarshal([]byte(output), &result) != nil || result.HistoryComplete {
		t.Fatalf("suffix output=%q err=%v", output, err)
	}
}

func TestAuditLifecyclesRejectsInvalidOptionsAndMissingStore(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", filepath.Join(t.TempDir(), "missing-policy.json"))
	for _, flags := range [][]string{
		{"--grace", "1m"}, {"--grace", "169h"}, {"--max-events", "0"},
		{"--max-events", "100001"}, {"--timeout", "0s"}, {"--timeout", "31m"}, {"--after", "-1"}, {},
	} {
		args := append([]string{"lifecycles"}, flags...)
		if _, err := runAudit(t, args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestValidateLifecycleTimeout(t *testing.T) {
	for _, d := range []time.Duration{time.Second, 2 * time.Minute, 30 * time.Minute} {
		if err := validateLifecycleTimeout(d); err != nil {
			t.Fatalf("%v rejected: %v", d, err)
		}
	}
	for _, d := range []time.Duration{0, time.Second - 1, 30*time.Minute + 1, -time.Second} {
		if err := validateLifecycleTimeout(d); err == nil {
			t.Fatalf("%v accepted", d)
		}
	}
}

func TestLifecycleOutcome(t *testing.T) {
	scanErr := errors.New("scan failed")
	if err := lifecycleOutcome(auditlifecycle.Result{Status: "ok"}, nil); err != nil {
		t.Fatalf("ok result: %v", err)
	}
	if err := lifecycleOutcome(auditlifecycle.Result{Status: "ok"}, scanErr); !errors.Is(err, scanErr) {
		t.Fatalf("scan error not preferred: %v", err)
	}
	err := lifecycleOutcome(auditlifecycle.Result{Status: "alert", MissingTerminal: 1}, nil)
	if err == nil || !strings.Contains(err.Error(), "operator attention") {
		t.Fatalf("non-ok status: %v", err)
	}
}
