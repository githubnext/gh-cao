package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestSignedRepositoryLifecycleQueuesEveryTransition(t *testing.T) {
	endpoint := os.Getenv("REDIS_URL")
	if endpoint == "" {
		t.Skip("REDIS_URL is not set")
	}
	client, err := redisx.New(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	store := redisx.NewStore(client, "repository-webhook-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	enrollment := collect.Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(t.Context(), 42, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	queue := collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	app := &App{services: store.OperationalServices(), webhookSecret: []byte("repository-webhook-test"),
		reconciler: &Collector{admitter: collect.Admitter{Enrollment: enrollment, Queue: queue}}}
	actions := []string{"created", "archived", "unarchived", "renamed", "transferred", "deleted"}
	for i, action := range actions {
		body := fmt.Sprintf(`{"action":%q,"repository":{"id":123,"full_name":"octo/api"},"installation":{"id":42}}`, action)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/github/webhook", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", "repository")
		req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("repository-%d", i))
		mac := hmac.New(sha256.New, app.webhookSecret)
		_, _ = mac.Write([]byte(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		app.githubWebhook(response, req)
		if response.Code != http.StatusAccepted {
			t.Fatalf("repository %s returned %d: %s", action, response.Code, response.Body.String())
		}
		var admitted map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &admitted); err != nil {
			t.Fatal(err)
		}
		if admitted["kind"] != "repository" || admitted["queued"] != true {
			t.Fatalf("repository %s not queued: %+v", action, admitted)
		}
	}
	if err := queue.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := store.StreamRead(t.Context(), "collect:tasks", "collectors", "lifecycle-test", len(actions), 0)
	if err != nil || len(messages) != len(actions) {
		t.Fatalf("expected %d independent lifecycle tasks: %d, %v", len(actions), len(messages), err)
	}
	for i, message := range messages {
		var task collect.Task
		if err := json.Unmarshal([]byte(message.Fields["task"]), &task); err != nil {
			t.Fatal(err)
		}
		if task.RepositoryID != 123 || task.InstallationID != 42 || task.Repository != "octo/api" ||
			task.Reason != "repository."+actions[i] {
			t.Fatalf("unexpected lifecycle task: %+v", task)
		}
	}
}
