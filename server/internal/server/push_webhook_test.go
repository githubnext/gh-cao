package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestSignedPushQueuesIndependentValueAndInventoryRefreshes(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enrollment := collect.Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(t.Context(), 42, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	queue := collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	app := &App{services: store.OperationalServices(), webhookSecret: []byte("push-webhook-test"),
		reconciler: &Collector{admitter: collect.Admitter{Enrollment: enrollment, Queue: queue}}}
	for i, test := range []struct {
		ref, path         string
		wantKind          string
		queued, duplicate bool
	}{
		{"refs/heads/topic", ".github/workflows/build.lock.yml", "ignore", false, false},
		{"refs/heads/trunk", ".github/workflows/build.md", "value-refresh", true, false},
		{"refs/heads/trunk", ".github/workflows/build.yml", "value-refresh", false, false},
		{"refs/heads/trunk", ".github/workflows/build.lock.yml", "workflow-refresh", true, false},
		{"refs/heads/trunk", ".github/workflows/build.lock.yml", "workflow-refresh", false, false},
		{"refs/heads/trunk", ".github/workflows/build.lock.yml", "workflow-refresh", false, true},
	} {
		body := fmt.Sprintf(`{"ref":%q,"repository":{"full_name":"octo/api","default_branch":"trunk"},"installation":{"id":42},"commits":[{"modified":[%q]}]}`, test.ref, test.path)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/github/webhook", strings.NewReader(body))
		req.Header.Set("X-GitHub-Event", "push")
		delivery := i
		if test.duplicate {
			delivery = i - 1
		}
		req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("push-%d", delivery))
		mac := hmac.New(sha256.New, app.webhookSecret)
		_, _ = mac.Write([]byte(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		app.githubWebhook(response, req)
		if response.Code != http.StatusAccepted {
			t.Fatalf("push %d returned %d: %s", i, response.Code, response.Body.String())
		}
		var admitted struct {
			Kind      string `json:"kind"`
			Queued    bool   `json:"queued"`
			Duplicate bool   `json:"duplicate"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &admitted); err != nil {
			t.Fatal(err)
		}
		if admitted.Kind != test.wantKind || admitted.Queued != test.queued || admitted.Duplicate != test.duplicate {
			t.Fatalf("push %d: %+v", i, admitted)
		}
	}
	if depth, err := queue.Depth(t.Context()); err != nil || depth != 2 {
		t.Fatalf("expected separate grouped Value and inventory refreshes: %d, %v", depth, err)
	}
}
