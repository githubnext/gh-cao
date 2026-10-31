package webhookgateway_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

const stressSigningFixture = "public-simulator-signature-fixture-not-a-credential"

type stressPR struct {
	simulator.FleetPullRequest
	admitted, dispatched, reviewed bool
	due                            time.Time
	execution                      *stressExecution
}

type stressReport struct {
	InitialRepositories int                  `json:"initialRepositories"`
	Developers          int                  `json:"developers"`
	Days                int                  `json:"days"`
	PRsPerDeveloperP50  int                  `json:"prsPerDeveloperPerDayP50"`
	Created             int                  `json:"createdRepositories"`
	Archived            int                  `json:"archivedRepositories"`
	Deleted             int                  `json:"deletedRepositories"`
	Ready               int                  `json:"readyPRs"`
	Reviewed            int                  `json:"reviewedPRs"`
	Missing             int                  `json:"missingReviews"`
	Late                int                  `json:"lateReviews"`
	DuplicateReviews    int                  `json:"duplicateReviews"`
	Webhooks            int                  `json:"webhooks"`
	RepositoryWebhooks  int                  `json:"repositoryWebhooks"`
	Replays             int                  `json:"replays"`
	DiscoveryFailures   int                  `json:"discoveryDeferrals"`
	MaxQueue            int64                `json:"maxQueueDepth"`
	ReviewP50Seconds    float64              `json:"reviewP50Seconds"`
	ReviewP99Seconds    float64              `json:"reviewP99Seconds"`
	ReviewMaxSeconds    float64              `json:"reviewMaxSeconds"`
	WallSeconds         float64              `json:"wallSeconds"`
	API                 simulator.FleetStats `json:"github"`
	Quota               simulator.QuotaStats `json:"quota"`
	TokenBroker         stressBrokerReport   `json:"tokenBroker"`
}

type stressDriver struct {
	t         *testing.T
	clock     *simulator.ManualClock
	api       *simulator.FleetAPI
	quota     *simulator.QuotaAPI
	url       string
	client    *http.Client
	shards    []*stressShard
	prs       map[string]*stressPR
	pending   []*stressPR
	latencies []float64
	report    stressReport
	limited   bool
	tick      int
	waiting   []*stressPR
	broker    *stressBroker
}

func (*stressDriver) WebhookSecrets(context.Context, string) ([]byte, []byte, error) {
	return []byte(stressSigningFixture), nil, nil
}

func (d *stressDriver) request(ctx context.Context, method, path string, installation int, body []byte) (int, http.Header, []byte) {
	d.t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, d.url+path, bytes.NewReader(body))
	if err != nil {
		d.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer simulator-installation-"+strconv.Itoa(installation))
	request.Header.Set("Content-Type", "application/json")
	response, err := d.client.Do(request)
	if err != nil {
		d.t.Fatal("local simulator HTTP", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		d.t.Fatal(err)
	}
	if response.StatusCode == 403 || response.StatusCode == 429 {
		d.limited = true
	}
	return response.StatusCode, response.Header, data
}

func (d *stressDriver) Dispatch(ctx context.Context, s wg.Subscription, payload json.RawMessage) wg.DispatchResponse {
	return d.DispatchCompiler(ctx, s, payload, wg.Revision{})
}

func (d *stressDriver) DispatchCompiler(ctx context.Context, s wg.Subscription, payload json.RawMessage, _ wg.Revision) wg.DispatchResponse {
	d.t.Helper()
	if err := wg.ValidateBatchDispatchPayload(s, payload, wg.DefaultLimits()); err != nil {
		d.t.Fatal("invalid production dispatch payload", err)
	}
	var request struct {
		Payload struct {
			Context struct {
				Webhook struct {
					Deliveries []struct {
						ID string `json:"delivery_id"`
					} `json:"deliveries"`
				} `json:"webhook"`
			} `json:"aw_context"`
		} `json:"client_payload"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		d.t.Fatal(err)
	}
	deliveries := request.Payload.Context.Webhook.Deliveries
	if len(deliveries) == 0 {
		d.t.Fatal("dispatch contains no PRs")
	}
	first := d.prs[deliveries[0].ID]
	if first == nil {
		d.t.Fatal("dispatch contains an unknown PR")
	}
	var destination string
	for _, repo := range d.api.Repositories() {
		if repo.ID == s.Destination.RepositoryID {
			destination = repo.Name
		}
	}
	if destination == "" {
		d.t.Fatal("unknown campaign control repository")
	}
	status, headers, body := d.request(ctx, http.MethodPost, "/repos/"+destination+"/dispatches", 2, payload)
	if status == 204 {
		execution := d.broker.execution(len(deliveries))
		for _, delivery := range deliveries {
			pr := d.prs[delivery.ID]
			if pr == nil || pr.Repository.ID != s.Source.RepositoryID || pr.dispatched {
				d.t.Fatal("missing, misrouted or duplicate PR dispatch")
			}
			pr.dispatched, pr.due = true, d.clock.Now().Add(2*time.Minute)
			pr.execution = execution
			d.pending = append(d.pending, pr)
		}
	}
	return wg.DispatchResponse{StatusCode: status, TransportOutcome: "response", Body: body,
		RetryAfter: headers.Get("Retry-After"), RateLimitRemaining: headers.Get("X-RateLimit-Remaining"),
		RateLimitReset: headers.Get("X-RateLimit-Reset")}
}

func (d *stressDriver) webhook(pr *stressPR, action string) int {
	d.t.Helper()
	body, err := json.Marshal(map[string]any{
		"action": action, "installation": map[string]int{"id": 1},
		"organization": map[string]any{"id": simulator.GatewayOwnerID, "login": "simulator"},
		"repository":   map[string]any{"id": pr.Repository.ID, "full_name": pr.Repository.Name, "private": true},
		"pull_request": map[string]any{"number": pr.Number, "draft": action == "opened", "state": "open",
			"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repository.Name, pr.Number),
			"user":     map[string]string{"login": fmt.Sprintf("developer-%d", pr.Developer)}},
	})
	if err != nil {
		d.t.Fatal(err)
	}
	id := pr.ID
	if action != "ready_for_review" {
		id += "-" + action
	}
	return d.deliver(body, pr.Repository.ID, id, "pull_request")
}

func (d *stressDriver) repositoryWebhook(repo simulator.FleetRepository, action string) {
	d.t.Helper()
	body, err := json.Marshal(map[string]any{
		"action": action, "installation": map[string]int{"id": 1},
		"organization": map[string]any{"id": simulator.GatewayOwnerID, "login": "simulator"},
		"repository":   map[string]any{"id": repo.ID, "full_name": repo.Name, "private": true},
	})
	if err != nil {
		d.t.Fatal(err)
	}
	if status := d.deliver(body, repo.ID, fmt.Sprintf("repository-%d-%s", repo.ID, action), "repository"); status != http.StatusAccepted {
		d.t.Fatalf("repository lifecycle webhook: status=%d", status)
	}
	d.report.RepositoryWebhooks++
}

func (d *stressDriver) deliver(body []byte, repository int64, id, event string) int {
	d.t.Helper()
	request := httptest.NewRequestWithContext(d.t.Context(), http.MethodPost, "/webhooks/compiler", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", event)
	request.Header.Set("X-GitHub-Delivery", id)
	signature := hmac.New(sha256.New, []byte(stressSigningFixture))
	_, _ = signature.Write(body)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	recorder := httptest.NewRecorder()
	d.shards[int(repository-1001)%len(d.shards)].ingress.ServeHTTP(recorder, request)
	d.report.Webhooks++
	return recorder.Code
}

func (d *stressDriver) retryAdmissions() {
	next := d.waiting[:0]
	for _, pr := range d.waiting {
		d.admit(pr)
		if !pr.admitted {
			next = append(next, pr)
		} else if pr.Number%100 == 0 {
			if status := d.webhook(pr, "ready_for_review"); status != http.StatusAccepted {
				d.t.Fatal("identical replay did not retain durable admission")
			}
			d.report.Replays++
		}
	}
	d.waiting = next
}

func (d *stressDriver) admit(pr *stressPR) {
	d.t.Helper()
	status := d.webhook(pr, "ready_for_review")
	switch status {
	case http.StatusAccepted:
		pr.admitted = true
	case http.StatusNoContent, http.StatusServiceUnavailable:
		// No subscription yet / unavailable admission: retain the original
		// ready time and retry the identical signed delivery, never reset SLA.
	default:
		d.t.Fatalf("PR admission status=%d at %s", status, d.clock.Now())
	}
}

func (d *stressDriver) discover() {
	// Six production ten-second wakeups per simulated minute, each retaining
	// the production eight-repository scan ceiling.
	for range 6 {
		shard := d.shards[d.tick%len(d.shards)]
		d.tick++
		if err := shard.discovery.RunOnce(d.t.Context()); err != nil {
			d.report.DiscoveryFailures++
		}
	}
}

func (d *stressDriver) completeReviews() {
	remaining := d.pending[:0]
	for _, pr := range d.pending {
		if d.clock.Now().Before(pr.due) || d.limited {
			remaining = append(remaining, pr)
			continue
		}
		if !d.broker.exchange(pr.execution) {
			remaining = append(remaining, pr)
			continue
		}
		status, _, _ := d.request(d.t.Context(), http.MethodPost,
			fmt.Sprintf("/repos/%s/pulls/%d/reviews", pr.Repository.Name, pr.Number), 2,
			[]byte(`{"event":"COMMENT","body":"Synthetic vulnerability review; no inference."}`))
		if status == 403 || status == 429 {
			remaining = append(remaining, pr)
			continue
		}
		if status != http.StatusCreated {
			d.t.Fatalf("synthetic review failed: status=%d", status)
		}
		if pr.reviewed {
			d.report.DuplicateReviews++
			d.t.Fatal("duplicate completed review")
		}
		pr.reviewed = true
		d.broker.complete(pr.execution)
		d.report.Reviewed++
		latency := d.clock.Now().Sub(pr.ReadyAt).Seconds()
		d.latencies = append(d.latencies, latency)
		if latency > time.Hour.Seconds() {
			d.report.Late++
		}
	}
	d.pending = remaining
}

func (d *stressDriver) drain() {
	for offset := range len(d.shards) {
		shard := d.shards[(d.tick+offset)%len(d.shards)]
		if depth, err := shard.queue.(wg.QueueDepthProvider).QueueDepth(d.t.Context()); err != nil {
			d.t.Fatal(err)
		} else {
			d.report.MaxQueue = max(d.report.MaxQueue, depth)
		}
		for !d.limited {
			worked, err := shard.worker.RunOnce(d.t.Context())
			if err != nil {
				d.t.Fatal("production webhook worker", err)
			}
			if !worked {
				break
			}
		}
	}
}
