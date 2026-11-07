package collectnative

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestNativeDiscoveryUsesScopedWorkflowPagesAndInclusiveWindow(t *testing.T) {
	after, before := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "100" {
			t.Error("discovery escaped its credential/page boundary")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4900")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		switch r.URL.Path {
		case "/repos/octo/api/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[{"id":41,"path":".github/workflows/test.lock.yml"},{"id":42,"path":".github/workflows/ci.yml"}]}`))
		case "/repos/octo/api/actions/workflows/41/runs":
			if r.URL.Query().Get("created") != "2026-10-01T00:00:00Z..2026-10-08T00:00:00Z" {
				t.Error("historical API request lost its immutable inclusive bounds")
			}
			var runs []map[string]any
			for index, at := range []time.Time{after, before} {
				runs = append(runs, map[string]any{"id": 303 + index, "run_attempt": 3 + index,
					"workflow_id": 41, "path": ".github/workflows/test.lock.yml", "created_at": at.Format(time.RFC3339),
					"repository": map[string]any{"full_name": "octo/api"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 2, "workflow_runs": runs})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	scope := EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	t.Setenv("PATH", "/nonexistent")
	workflows, next, err := provider.DiscoveryWorkflows(t.Context(), scope, 1)
	if err != nil || next != 0 || !reflect.DeepEqual(workflows, []int64{41}) {
		t.Fatalf("agentic workflow discovery: %v %d %v", workflows, next, err)
	}
	runs, next, err := provider.DiscoveryRuns(t.Context(), scope, 41, after, before, 1)
	if err != nil || next != 0 || len(runs) != 2 || !runs[0].CreatedAt.Equal(after) || !runs[1].CreatedAt.Equal(before) ||
		runs[0].Attempt != 3 || runs[1].Attempt != 4 {
		t.Fatalf("native inclusive exact-attempt discovery: %v %d %v", runs, next, err)
	}
}

func TestNativeDiscoveryRejectsCappedMissingAndForeignEvidence(t *testing.T) {
	for _, test := range []struct {
		name, response string
		want           error
	}{
		{"capped", `{"total_count":1001,"workflow_runs":[]}`, ErrCapacity},
		{"missing-total", `{"workflow_runs":[]}`, nil},
		{"missing-population", `{"total_count":0}`, nil},
		{"partial-page", `{"total_count":2,"workflow_runs":[]}`, nil},
		{"foreign-repository", `{"total_count":1,"workflow_runs":[{"id":303,"run_attempt":1,"workflow_id":41,"path":".github/workflows/test.lock.yml","created_at":"2026-10-01T00:00:00Z","repository":{"full_name":"other/repository"}}]}`, ErrIdentity},
		{"outside-window", `{"total_count":1,"workflow_runs":[{"id":303,"run_attempt":1,"workflow_id":41,"path":".github/workflows/test.lock.yml","created_at":"2026-09-01T00:00:00Z","repository":{"full_name":"octo/api"}}]}`, ErrIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
			after, before := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
			_, _, err := provider.DiscoveryRuns(t.Context(), EnrollmentScope{"octo/api", 7, 1}, 41, after, before, 1)
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("invalid history became successful evidence: %v", err)
			}
		})
	}
}
