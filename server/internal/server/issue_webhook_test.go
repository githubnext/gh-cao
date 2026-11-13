package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func TestSignedIssueWebhookQueuesDeduplicatedRepositoryRefresh(t *testing.T) {

	store := newTestOperationalStore(t)
	enrollment := collect.Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(t.Context(), 42, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	queue := collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	app := &App{services: store.OperationalServices(), webhookSecret: []byte("test-issue-webhook-secret"),
		reconciler: &Collector{admitter: collect.Admitter{
			Enrollment: enrollment, Queue: queue,
		}}}
	payload := `{"action":"closed","repository":{"full_name":"octo/api"},"installation":{"id":42},
		"issue":{"number":12,"state":"closed","state_reason":"completed",
		"closed_at":"2026-01-02T03:04:06Z","updated_at":"2026-01-02T03:04:06Z",
		"html_url":"https://github.com/octo/api/issues/12"}}`
	send := func(body, delivery string, signed bool) map[string]any {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/github/webhook", strings.NewReader(body))
		request.Header.Set("X-GitHub-Event", "issues")
		request.Header.Set("X-GitHub-Delivery", delivery)
		if signed {
			mac := hmac.New(sha256.New, app.webhookSecret)
			_, _ = mac.Write([]byte(body))
			request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		response := httptest.NewRecorder()
		app.githubWebhook(response, request)
		if !signed {
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unsigned webhook returned %d", response.Code)
			}
			return nil
		}
		if response.Code != http.StatusAccepted {
			t.Fatalf("webhook returned %d: %s", response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	depth := func() int64 {
		t.Helper()
		length, err := testQueueLength(t.Context(), store, "collect:tasks")
		if err != nil {
			t.Fatal(err)
		}
		return length
	}
	send(payload, "unsigned", false)
	if got := depth(); got != 0 {
		t.Fatalf("unsigned delivery queued %d tasks", got)
	}
	unowned := strings.Replace(payload, `"id":42`, `"id":43`, 1)
	if result := send(unowned, "wrong-installation", true); result["reason"] != "not-enrolled" || result["queued"] == true {
		t.Fatalf("delivery from wrong installation was admitted: %+v", result)
	}
	outOfScope := strings.ReplaceAll(payload, "octo/api", "octo/unowned")
	if result := send(outOfScope, "outside-enrollment", true); result["reason"] != "not-enrolled" || result["queued"] == true {
		t.Fatalf("unenrolled repository was admitted: %+v", result)
	}
	pullRequest := strings.Replace(payload, `"number":12`, `"number":12,"pull_request":{"url":"https://api.github.com/repos/octo/api/pulls/12"}`, 1)
	if result := send(pullRequest, "pull-request", true); result["kind"] != "ignore" || result["queued"] != false {
		t.Fatalf("pull request was admitted as an issue: %+v", result)
	}
	if got := depth(); got != 0 {
		t.Fatalf("out-of-scope deliveries queued %d tasks", got)
	}
	if result := send(payload, "delivery-1", true); result["kind"] != "issue-status" ||
		result["queued"] != true || result["duplicate"] != false {
		t.Fatalf("signed issue did not enqueue repository refresh: %+v", result)
	}
	if result := send(payload, "delivery-1", true); result["duplicate"] != true || result["queued"] != false {
		t.Fatalf("duplicate delivery queued another refresh: %+v", result)
	}
	reopened := strings.ReplaceAll(payload, `"action":"closed"`, `"action":"reopened"`)
	reopened = strings.ReplaceAll(reopened, `"state":"closed"`, `"state":"open"`)
	if result := send(reopened, "delivery-same-second", true); result["kind"] != "issue-status" ||
		result["queued"] != false || result["duplicate"] != false {
		t.Fatalf("same-second status did not coalesce pending refresh: %+v", result)
	}
	if got := depth(); got != 1 {
		t.Fatalf("issue deliveries queued %d tasks, want one", got)
	}

	repository := "simulator/repo-00001"
	if err := enrollment.AddRepositories(t.Context(), 1, []string{repository}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := (simulator.Scenario{
		Name: "issue-status-source", Repositories: 1, IssueEventsPerRepository: 4, Seed: 7,
	}).Generate()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, delivery := range deliveries {
		if delivery.Event != "issues" {
			continue
		}
		result := send(string(delivery.Payload), delivery.ID, true)
		if result["kind"] != "issue-status" || result["duplicate"] != false ||
			result["queued"] != (seen == 0) {
			t.Fatalf("simulated issue delivery %d did not coalesce: %+v", seen, result)
		}
		seen++
	}
	if seen != 4 {
		t.Fatalf("simulator generated %d issue deliveries, want four", seen)
	}
	if got := depth(); got != 2 {
		t.Fatalf("issue events queued %d repository refreshes, want two", got)
	}
	if err := queue.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadTasks(t.Context(), operational.QueueRead{Queue: "collect:tasks", Group: "collectors", Consumer: "issue-webhook-test", Count: 2, Block: 0})
	if err != nil || len(messages) != 2 {
		t.Fatalf("expected two durable repository refreshes: %+v, %v", messages, err)
	}
	wantInstallations := map[string]int64{"octo/api": 42, repository: 1}
	for _, message := range messages {
		var task collect.Task
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil {
			t.Fatal(err)
		}
		if task.InstallationID != wantInstallations[task.Repository] || task.InstallationID == 0 ||
			task.Reason != "issue-status" || task.Erase {
			t.Fatalf("unexpected repository refresh: %+v", task)
		}
		delete(wantInstallations, task.Repository)
	}
	if len(wantInstallations) != 0 {
		t.Fatalf("missing repository refreshes: %+v", wantInstallations)
	}
}
