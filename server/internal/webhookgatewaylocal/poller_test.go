package webhookgatewaylocal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/testutil"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

type pollingCase struct {
	ID, Scenario string
	ExpectedJobs int64
}

type readRecord struct {
	method, path, condition string
}

type pollRig struct {
	queue           pollingQueue
	store           *postgres.Store
	poller          *Poller
	policy          wg.PolicySnapshot
	dsn, namespace  string
	records         chan readRecord
	badIdentity     atomic.Bool
	rejectAdmission atomic.Bool
	payloadVersion  atomic.Int32
	lateEvent       atomic.Bool
	nullFeed        atomic.Bool
}

type pollingQueue interface {
	wg.Queue
	wg.QueueDepthProvider
	ReceiptReader
	Counts(context.Context) (map[wg.JobState]int64, error)
}

func newPollRig(t *testing.T, total int) *pollRig {
	t.Helper()
	rig := &pollRig{records: make(chan readRecord, 100)}
	rig.store, rig.dsn, rig.namespace = testutil.OperationalPostgres(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.records <- readRecord{r.Method, r.URL.RequestURI(), r.Header.Get("If-None-Match")}
		if r.Method != http.MethodGet {
			t.Error("local client attempted a GitHub write")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/repos/octo/repo":
			id := 42
			if rig.badIdentity.Load() {
				id = 99
			}
			if _, err := fmt.Fprintf(w, `{"id":%d,"full_name":"octo/repo","owner":{"id":7,"type":"Organization"}}`, id); err != nil {
				t.Error(err)
			}
		case "/repos/octo/repo/events":
			w.Header().Set("ETag", `"feed"`)
			w.Header().Set("X-Poll-Interval", "120")
			if r.Header.Get("If-None-Match") == `"feed"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if rig.nullFeed.Load() {
				if _, err := fmt.Fprint(w, "null"); err != nil {
					t.Error(err)
				}
				return
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			events := make([]map[string]any, 0, 100)
			count := total
			if rig.lateEvent.Load() {
				count++
			}
			for i := (page - 1) * 100; i < min(page*100, count); i++ {
				id := strconv.Itoa(i + 1)
				if i == total {
					id = "0"
				}
				events = append(events, map[string]any{"id": id, "type": "IssuesEvent",
					"repo": map[string]any{"id": 42}, "payload": map[string]any{
						"action": "opened", "private_body": "never-persist-feed", "updated": rig.payloadVersion.Load()}})
			}
			if err := json.NewEncoder(w).Encode(events); err != nil {
				t.Error(err)
			}
		default:
			t.Error("client escaped the explicit repository scope")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "octo/repo", "test-only-token")
	if err != nil {
		t.Fatal(err)
	}
	repository, err := client.Repository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	simulation, err := NewSimulation(repository, []string{"issues"})
	if err != nil {
		t.Fatal(err)
	}
	rig.policy, err = simulation.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := rig.store.OpenWebhookQueue(t.Context(), rig.policy.Policy.Namespace, rig.policy.Policy.Queue)
	if err != nil {
		t.Fatal(err)
	}
	rig.queue = queue.(pollingQueue)
	t.Cleanup(func() { _ = rig.store.Close() })
	rig.poller = &Poller{Client: client, Repository: repository, Simulation: simulation, Interval: time.Minute}
	rig.wire()
	return rig
}

func (r *pollRig) wire() {
	r.poller.Receipts = r.queue
	ingress := &wg.Ingress{SourceID: "local", Secrets: r.poller.Simulation, Policies: r.poller.Simulation,
		Queue: r.queue, Authorizer: r.poller.Simulation}
	r.poller.Handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if r.rejectAdmission.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ingress.ServeHTTP(w, request)
	})
}

func TestSpecPolling(t *testing.T) {
	data, err := os.ReadFile("../../../specs/fixtures/local-gateway-polling.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Version int
		Cases   []pollingCase
	}
	if err := json.Unmarshal(data, &suite); err != nil || suite.Version != 1 || len(suite.Cases) != 5 {
		t.Fatal("invalid polling specification vectors", err)
	}
	spec, err := os.ReadFile("../../../specs/rfc-034-local-webhook-gateway.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if seen[c.ID] || !strings.Contains(string(spec), "**"+c.ID+"**") {
			t.Fatal("duplicate or unspecified polling requirement", c.ID)
		}
		seen[c.ID] = true
		t.Run(c.ID, func(t *testing.T) {
			total := int(max(1, c.ExpectedJobs))
			if c.Scenario == "etag-rollback" {
				total = 1
			}
			rig := newPollRig(t, total)
			switch c.Scenario {
			case "identity":
				rig.badIdentity.Store(true)
				if _, err := rig.poller.Poll(t.Context()); err == nil {
					t.Fatal("repository name reuse accepted")
				}
			case "pagination", "signed-ingress":
				result, err := rig.poller.Poll(t.Context())
				if err != nil || result.Delivered != int(c.ExpectedJobs) {
					t.Fatal("signed polling admission failed", result, err)
				}
			case "etag-rollback":
				rig.rejectAdmission.Store(true)
				if _, err := rig.poller.Poll(t.Context()); err == nil || rig.poller.etag != "" {
					t.Fatal("failed delivery advanced polling progress", err)
				}
				if _, err := rig.poller.Poll(t.Context()); err != nil || rig.poller.etag != `"feed"` {
					t.Fatal("failed delivery was skipped", err)
				}
				if err := rig.store.Close(); err != nil {
					t.Fatal(err)
				}
				rig.store, err = postgres.Open(t.Context(), rig.dsn, rig.namespace, postgres.Config{})
				if err != nil {
					t.Fatal(err)
				}
				queue, err := rig.store.OpenWebhookQueue(t.Context(), rig.policy.Policy.Namespace, rig.policy.Policy.Queue)
				if err != nil {
					t.Fatal(err)
				}
				rig.queue = queue.(pollingQueue)
				rig.poller.Simulation, err = NewSimulation(rig.poller.Repository, []string{"issues"})
				if err != nil {
					t.Fatal(err)
				}
				rig.poller.etag = ""
				rig.payloadVersion.Store(1)
				rig.wire()
				result, err := rig.poller.Poll(t.Context())
				if err != nil || result.Replayed != 1 || result.Delivered != 0 {
					t.Fatal("changed feed payload was not recognized after restart", result, err)
				}
				rig.poller.etag = ""
				rig.lateEvent.Store(true)
				result, err = rig.poller.Poll(t.Context())
				if err != nil || result.Replayed != 1 || result.Delivered != 1 {
					t.Fatal("late event was skipped by replay handling", result, err)
				}
			case "conditional-cancel":
				if _, err := rig.poller.Poll(t.Context()); err != nil {
					t.Fatal(err)
				}
				result, err := rig.poller.Poll(t.Context())
				if err != nil || !result.Unchanged || result.Delivered != 0 || rig.poller.minimum != 120*time.Second {
					t.Fatal("conditional polling or minimum interval failed", result, err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := rig.poller.Run(ctx); !errors.Is(err, context.Canceled) {
					t.Fatal("poll loop ignored cancellation", err)
				}
			default:
				t.Fatal("unknown normative polling scenario", c.Scenario)
			}
			worker := &wg.Worker{Policies: rig.poller.Simulation, Queue: rig.queue, Authorizer: rig.poller.Simulation,
				Dispatcher: rig.poller.Simulation, Owner: "test", HTTPTimeout: time.Second, LeaseDuration: time.Minute}
			for range c.ExpectedJobs + 1 {
				worked, err := worker.RunOnce(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !worked {
					break
				}
			}
			counts, err := rig.queue.Counts(t.Context())
			if err != nil || counts[wg.Succeeded] != c.ExpectedJobs {
				t.Fatal("worker outcomes differ from specification", counts, err)
			}
			if depth, err := rig.queue.QueueDepth(t.Context()); err != nil || depth != 0 {
				t.Fatal("worker left unprocessed jobs", depth, err)
			}
			pages := 0
			for len(rig.records) > 0 {
				record := <-rig.records
				if record.method != http.MethodGet || !strings.HasPrefix(record.path, "/repos/octo/repo") {
					t.Fatal("non-read-only or out-of-scope request", record)
				}
				if strings.Contains(record.path, "/events?") {
					pages++
				}
			}
			if c.Scenario == "pagination" && pages != 3 {
				t.Fatal("incorrect pagination bound", pages)
			}
		})
	}
	for i := 1; i <= 5; i++ {
		if !seen["LG-P0"+strconv.Itoa(i)] {
			t.Fatal("missing normative polling requirement", i)
		}
	}
}

func TestPollingFailsClosedWithoutReadableReceipts(t *testing.T) {
	for _, unavailable := range []string{"missing", "closed"} {
		t.Run(unavailable, func(t *testing.T) {
			rig := newPollRig(t, 1)
			if unavailable == "missing" {
				rig.poller.Receipts = nil
			} else if err := rig.store.Close(); err != nil {
				t.Fatal(err)
			}
			rig.poller.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("delivery proceeded without readable receipts")
			})
			if result, err := rig.poller.Poll(t.Context()); err == nil || result.Delivered != 0 || rig.poller.etag != "" {
				t.Fatal("unavailable receipts advanced polling progress", result, err)
			}
		})
	}
}

func TestPollingRejectsNullFeedWithoutAdvancingETag(t *testing.T) {
	rig := newPollRig(t, 1)
	rig.poller.etag = `"previous"`
	rig.nullFeed.Store(true)
	result, err := rig.poller.Poll(t.Context())
	if err == nil || result.Delivered != 0 || rig.poller.etag != `"previous"` {
		t.Fatal("null feed was accepted or advanced polling progress", result, err)
	}
	if depth, err := rig.queue.QueueDepth(t.Context()); err != nil || depth != 0 {
		t.Fatal("invalid feed created work", depth, err)
	}
}

func TestPollingAcceptsEmptyFeed(t *testing.T) {
	rig := newPollRig(t, 0)
	result, err := rig.poller.Poll(t.Context())
	if err != nil || result.Seen != 0 || result.Unchanged || rig.poller.etag != `"feed"` {
		t.Fatal("empty feed was not accepted", result, err)
	}
}

func TestLocalIngressStillVerifiesSignaturesAndDropsRawData(t *testing.T) {
	rig := newPollRig(t, 1)
	request, err := simulator.SignedRequest(t.Context(), "http://127.0.0.1/webhooks/local", "wrong-key",
		simulator.Delivery{ID: "invalid-signature", Event: "issues", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	rig.poller.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("signature verification was bypassed")
	}
	if _, err := rig.poller.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", rig.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var leaked bool
	if err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM cao_operational_webhook_jobs
		WHERE namespace=$1 AND (position('never-persist-feed' in convert_from(document,'UTF8'))>0
		OR position('test-only-token' in convert_from(document,'UTF8'))>0))`, rig.namespace).Scan(&leaked); err != nil || leaked {
		t.Fatal("raw feed or credential persisted", err)
	}
}

func TestLocalClientRejectsRedirectsAndUnsafeLocators(t *testing.T) {
	for _, input := range []struct{ base, repo string }{
		{"http://example.com", "octo/repo"}, {"http://localhost:8080", "octo/repo"},
		{"https://example.com?credential=unsafe", "octo/repo"}, {"https://user:pass@example.com", "octo/repo"},
		{"https://api.github.com", "../repo"}, {"https://api.github.com", "octo/repo/../another"},
	} {
		if _, err := NewClient(input.base, input.repo, ""); err == nil {
			t.Fatal("unsafe API locator accepted")
		}
	}
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "/escaped")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "octo/repo", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Repository(t.Context()); err == nil || requests.Load() != 1 {
		t.Fatal("API redirect followed", err, requests.Load())
	}
	repository := Repository{ID: 42}
	repository.Owner.ID, repository.Owner.Type = 7, "Organization"
	if _, err := NewSimulation(repository, []string{"projects_v2_item"}); err == nil {
		t.Fatal("unsupported Projects webhook simulation claimed")
	}
}
