package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

func TestHostedCLIQueueOperationsPostgres(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv("CAO_AUDIT_LOG", auditPath)
	store, dsn, namespace := operationalCommandFixture(t)
	options, err := (operational.CLICommandQueueOptions{}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	queue, err := store.OpenCLICommandQueue(t.Context(), "hosted-cli", options)
	if err != nil {
		t.Fatal(err)
	}
	var command operational.CLICommand
	for index := range 3 {
		command, err = queue.Enqueue(t.Context(), operational.CLICommandInvocation{
			ActionID: "run-workflow", Command: "gh workflow run build.yml",
			Actor: "octocat", ActorID: 1234, Repository: "acme/widgets",
		}, "operator-test-"+strconv.Itoa(index), 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	output, err := runHostedCLIQueueTestCommand(t, "list", "--status", "queued", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Commands              []map[string]any `json:"commands"`
		Limit                 int              `json:"limit"`
		HasMore               bool             `json:"hasMore"`
		NextContinuationToken string           `json:"nextContinuationToken"`
	}
	if err := json.Unmarshal([]byte(output), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Commands) != 2 ||
		page.Commands[0]["id"] != float64(command.ID) ||
		page.Commands[0]["actor"] != "octocat" ||
		page.Limit != 2 || !page.HasMore || page.NextContinuationToken == "" {
		t.Fatalf("unexpected queue list: %s", output)
	}
	if strings.Contains(output, "gh workflow run") {
		t.Fatalf("queue summary disclosed command text: %s", output)
	}
	firstPageLastID := page.Commands[1]["id"]
	output, err = runHostedCLIQueueTestCommand(t, "list", "--status", "queued", "--limit", "2", "--continuation-token", page.NextContinuationToken)
	if err != nil {
		t.Fatal(err)
	}
	page.NextContinuationToken = ""
	if err := json.Unmarshal([]byte(output), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Commands) != 1 || page.Commands[0]["id"] != float64(1) ||
		page.Limit != 2 || page.HasMore || page.NextContinuationToken != "" {
		t.Fatalf("unexpected final queue page: %s", output)
	}
	if _, err := runHostedCLIQueueTestCommand(t, "list", "--continuation-token", "not-a-token"); err == nil {
		t.Fatal("invalid continuation token was accepted")
	}
	if firstPageLastID == page.Commands[0]["id"] {
		t.Fatal("continuation token repeated the final item from the previous page")
	}

	output, err = runHostedCLIQueueTestCommand(t, "show", "1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "gh workflow run") || !strings.Contains(output, `"repository": "acme/widgets"`) {
		t.Fatalf("unexpected command summary: %s", output)
	}

	if _, err := runHostedCLIQueueTestCommand(t, "cancel", "1", "--actor", "ops-user"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHostedCLIQueueTestCommand(t, "cancel", "1"); err == nil {
		t.Fatal("cancel without an operator identity succeeded")
	}

	verifyStore, err := postgres.Open(t.Context(), dsn, namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifyStore.Close() }()
	verifyQueue, err := verifyStore.OpenCLICommandQueue(t.Context(), "hosted-cli", options)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := verifyQueue.Detail(t.Context(), 1)
	if err != nil || canceled.Status != operational.CLICommandCanceled {
		t.Fatalf("command after cancel = %q, %v", canceled.Status, err)
	}
	events, err := verifyStore.ListAudit(t.Context(), operational.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	foundRequest, foundCanceled := false, false
	for _, event := range events {
		if event.Actor != "ops-user" {
			continue
		}
		foundRequest = foundRequest || event.Event == "cli_command.operator_cancel_requested"
		foundCanceled = foundCanceled || event.Event == "cli_command.operator_cancelled"
	}
	if !foundRequest || !foundCanceled {
		t.Fatalf("operator cancellation audit events missing: %+v", events)
	}
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatalf("audit mirror was not created: %v", err)
	}
}

func runHostedCLIQueueTestCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{"hosted-cli", "queue"}, args...))
	err := root.Execute()
	return output.String(), err
}

func TestHostedCLIQueueFilter(t *testing.T) {
	token := hostedCLIContinuationToken(42)
	filter, err := hostedCLIQueueFilter([]string{" queued ", "running"}, 100, token)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Limit != 100 || filter.BeforeID != 42 || len(filter.Statuses) != 2 || filter.Statuses[0] != "queued" {
		t.Fatalf("unexpected filter: %+v", filter)
	}
	if filter, err = hostedCLIQueueFilter(nil, 20, ""); err != nil || filter.BeforeID != 0 || len(filter.Statuses) != 0 {
		t.Fatalf("unexpected default filter: %+v, %v", filter, err)
	}
	for name, call := range map[string]func() error{
		"empty status":   func() error { _, err := hostedCLIQueueFilter([]string{"  "}, 20, ""); return err },
		"unknown status": func() error { _, err := hostedCLIQueueFilter([]string{"bogus"}, 20, ""); return err },
		"zero limit":     func() error { _, err := hostedCLIQueueFilter(nil, 0, ""); return err },
		"limit too high": func() error { _, err := hostedCLIQueueFilter(nil, 101, ""); return err },
		"bad token":      func() error { _, err := hostedCLIQueueFilter(nil, 20, "not-a-token"); return err },
	} {
		if call() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
