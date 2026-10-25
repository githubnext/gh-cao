package githubapp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-github/v66/github"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

func TestReadOutcomeMakesOneQuotaReportedObjectRead(t *testing.T) {
	for _, pullRequest := range []bool{false, true} {
		t.Run(strconv.FormatBool(pullRequest), func(t *testing.T) {
			category, webCategory := "issues", "issues"
			if pullRequest {
				category, webCategory = "pulls", "pull"
			}
			raw := fmt.Sprintf("https://github.com/octo/api/%s/12", webCategory)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/api/"+category+"/12" {
					t.Errorf("unexpected object request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4999")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
				w.Header().Set("X-RateLimit-Resource", "core")
				_, _ = fmt.Fprintf(w, `{"number":12,"html_url":%q,"state":"closed","state_reason":"completed",
					"merged":%t,"updated_at":"2026-10-20T00:00:00Z","closed_by":{"type":"User"}}`, raw, pullRequest)
			}))
			defer upstream.Close()
			installation := github.NewClient(upstream.Client())
			base, err := url.Parse(upstream.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			installation.BaseURL = base
			client := &Client{installations: map[int64]*installationEntry{7: {client: installation}}}
			target, err := mining.ParseTarget(raw)
			if err != nil {
				t.Fatal(err)
			}
			outcome, quota, err := client.ReadOutcome(t.Context(), 7, target)
			if err != nil || calls != 1 || !quota.HasObservation || quota.Observation.Remaining != 4999 {
				t.Fatalf("outcome=%+v quota=%+v calls=%d err=%v", outcome, quota, calls, err)
			}
			if status, _ := outcome.Evaluation(); status != "accepted" {
				t.Fatalf("observed disposition lost: %+v", outcome)
			}
			target.URL = "https://unrelated.example/octo/api/issues/12"
			if _, _, err := client.ReadOutcome(t.Context(), 7, target); err == nil || calls != 1 {
				t.Fatal("foreign-host target reached the API")
			}
		})
	}
}
