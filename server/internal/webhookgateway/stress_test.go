package webhookgateway_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational/sqlite"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewayhost"
)

func newStressDriver(t *testing.T, repositories, developers, days int) *stressDriver {
	t.Helper()
	c := simulator.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	api := &simulator.FleetAPI{Clock: c, Workflow: stressManifest(t), WorkflowRepositoryID: simulator.GatewayRepositoryID}
	for range repositories {
		api.Create()
	}
	quota := &simulator.QuotaAPI{Handler: api, Clock: c,
		Limits: simulator.QuotaLimits{RequestsPerHour: 12500, PointsPerMinute: 900}}
	server := httptest.NewServer(quota)
	t.Cleanup(server.Close)
	transport, err := simulator.NewLocalTransport(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	// An ephemeral fixture key satisfies the real discovery client's constructor.
	// No secret is read from the environment, disk, Actions or GitHub.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	discoveryClient, err := githubapp.New(githubapp.Config{
		AppID: 1, BaseURL: server.URL, Transport: transport, RequestTimeout: 5 * time.Second,
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	d := &stressDriver{t: t, clock: c, api: api, quota: quota, url: server.URL,
		client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, prs: make(map[string]*stressPR),
		report: stressReport{InitialRepositories: repositories, Developers: developers, Days: days, PRsPerDeveloperP50: 10}}
	d.broker = newStressBroker(t, c.Now)

	// Pre-enrollment is explicit fixture authority, not discovery-created scope.
	// Future immutable IDs are initially absent from GitHub and fail closed.
	planned := api.Repositories()
	active := repositories
	for range days - 1 {
		created, retired := max(1, active*2/100), max(1, active/100)
		for range created {
			id := int64(len(planned) + 1001)
			planned = append(planned, simulator.FleetRepository{ID: id, Name: fmt.Sprintf("simulator/repo-%05d", id)})
		}
		active += created - retired
	}
	shards := 1
	dir := t.TempDir()
	for index := range shards {
		base := stressBasePolicy(planned, index, shards)
		if err := wg.ValidateDiscoveryPolicy(base.Policy); err != nil {
			t.Fatal("invalid bounded fixture policy", err)
		}
		registry := &stressRegistry{}
		if _, err := registry.BindRegistry(t.Context(), base.Revision); err != nil {
			t.Fatal(err)
		}
		policy := &stressPolicy{base: base, registry: registry, clock: c}
		queue, err := sqlite.OpenWebhookQueueWithClock(t.Context(), filepath.Join(dir, fmt.Sprintf("queue-%d.sqlite", index)),
			base.Policy.Namespace, base.Policy.Queue, c)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := queue.Close(); err != nil {
				t.Error(err)
			}
		})
		shard := &stressShard{policy: policy, queue: queue,
			discovery: &webhookgatewayhost.DiscoveryWorker{
				Policies: stressBase{base}, Registry: registry, GitHub: discoveryClient, Clock: c,
			}}
		shard.ingress = &wg.Ingress{SourceID: wg.CompilerSourceID, Secrets: d, Policies: policy, Queue: queue, Authorizer: shard, Clock: c}
		shard.ingress.Discovery = webhookgatewayhost.DiscoveryHints{Policies: stressBase{base}, Registry: registry}
		shard.worker = &wg.Worker{Policies: policy, Queue: queue, Authorizer: shard, Dispatcher: d, Clock: c,
			Owner: fmt.Sprintf("stress-%d", index), HTTPTimeout: time.Second, LeaseDuration: time.Minute}
		d.shards = append(d.shards, shard)
	}
	// A bounded, rate-limited cold scan precedes PR arrivals.
	for range 60 {
		d.discover()
		c.Advance(time.Minute)
	}
	discovered := 0
	for _, shard := range d.shards {
		policy, err := shard.policy.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		discovered += len(policy.Subscriptions)
	}
	if discovered != repositories {
		t.Fatalf("cold discovery activated %d/%d repositories", discovered, repositories)
	}
	return d
}

func (d *stressDriver) churn() {
	d.t.Helper()
	var active []simulator.FleetRepository
	busy := make(map[int64]bool)
	for _, pr := range d.prs {
		if !pr.reviewed {
			busy[pr.Repository.ID] = true
		}
	}
	for _, repo := range d.api.Repositories() {
		if !repo.Archived && !repo.Deleted {
			active = append(active, repo)
		}
	}
	retired, created := max(1, len(active)/100), max(1, len(active)*2/100)
	count := 0
	for _, repo := range active {
		// Retire only drained targets; deletion cannot truthfully produce a
		// future GitHub review. Pending PRs are never excluded from the SLA.
		if busy[repo.ID] {
			continue
		}
		if repo.ID == d.api.WorkflowRepositoryID {
			continue
		}
		deleted := count%2 == 0
		d.api.Retire(repo.ID, deleted)
		action := "archived"
		if deleted {
			d.report.Deleted++
			action = "deleted"
		} else {
			d.report.Archived++
		}
		d.repositoryWebhook(repo, action)
		count++
		if count == retired {
			break
		}
	}
	if count != retired {
		d.t.Fatal("cannot retire the required 1% without losing an outstanding review")
	}
	for range created {
		repo := d.api.Create()
		d.repositoryWebhook(repo, "created")
		d.report.Created++
	}
}

func (d *stressDriver) runDay(day int) {
	d.t.Helper()
	var active []simulator.FleetRepository
	for _, repo := range d.api.Repositories() {
		if !repo.Archived && !repo.Deleted {
			active = append(active, repo)
		}
	}
	arrivals := make([][]*stressPR, 24*60)
	for _, fixture := range simulator.FleetDay(d.clock.Now(), day, d.report.Developers, active) {
		pr := &stressPR{FleetPullRequest: fixture}
		minute := int(pr.ReadyAt.Sub(d.clock.Now()) / time.Minute)
		arrivals[minute] = append(arrivals[minute], pr)
		d.prs[pr.ID] = pr
	}
	for minute := range 24 * 60 {
		d.limited = false
		d.discover()
		// Exhaust the shared dispatch installation near the end of an hour:
		// production response classification must park jobs until reset.
		if minute == 55 {
			d.quota.ExhaustPrimary(2)
			status, _, _ := d.request(d.t.Context(), http.MethodGet, "/repositories/1001", 2, nil)
			if status != http.StatusForbidden {
				d.t.Fatal("primary quota exhaustion was not enforced")
			}
		}
		for _, pr := range arrivals[minute] {
			d.report.Ready++
			if status := d.webhook(pr, "opened"); status != http.StatusNoContent {
				d.t.Fatalf("draft PR should not enqueue a vulnerability review: %d", status)
			}
			d.waiting = append(d.waiting, pr)
		}
		d.retryAdmissions()
		d.completeReviews()
		d.drain()
		d.clock.Advance(time.Minute)
	}
}

func runFleetStress(t *testing.T, repositories, developers, days int) {
	t.Helper()
	started := time.Now()
	d := newStressDriver(t, repositories, developers, days)
	// Write an incomplete report even if an invariant aborts the scenario.
	t.Cleanup(func() {
		d.report.Missing = d.report.Ready - d.report.Reviewed
		d.report.API, d.report.Quota = d.api.Stats(), d.quota.Stats()
		d.report.TokenBroker = d.broker.report
		d.report.WallSeconds = time.Since(started).Seconds()
		if dir := os.Getenv("CAO_FLEET_STRESS_REPORT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0750); err != nil { // #nosec G703 -- report destination is explicitly selected by the local test operator, never webhook input.
				t.Error(err)
				return
			}
			data, err := json.MarshalIndent(d.report, "", "  ")
			if err != nil {
				t.Error(err)
			} else if err := os.WriteFile(filepath.Join(dir, "fleet-stress.json"), append(data, '\n'), 0600); err != nil { // #nosec G703 -- fixed report name under the operator-selected local test artifact directory.
				t.Error(err)
			}
		}
		t.Logf("fleet stress: ready=%d reviewed=%d missing=%d late=%d max=%.0fs primary=%d secondary=%d wall=%.2fs",
			d.report.Ready, d.report.Reviewed, d.report.Missing, d.report.Late, d.report.ReviewMaxSeconds,
			d.report.Quota.Primary, d.report.Quota.Secondary, d.report.WallSeconds)
	})
	for day := range days {
		if day > 0 {
			d.churn()
		}
		d.runDay(day)
		t.Logf("simulated day %d: ready=%d reviewed=%d", day+1, d.report.Ready, d.report.Reviewed)
	}
	for range 60 {
		d.limited = false
		d.discover()
		d.retryAdmissions()
		d.completeReviews()
		d.drain()
		d.clock.Advance(time.Minute)
	}
	slices.Sort(d.latencies)
	if len(d.latencies) > 0 {
		d.report.ReviewP50Seconds = d.latencies[len(d.latencies)/2]
		d.report.ReviewP99Seconds = d.latencies[(len(d.latencies)-1)*99/100]
		d.report.ReviewMaxSeconds = d.latencies[len(d.latencies)-1]
	}
	if d.report.Ready != developers*10*days || d.report.Reviewed != d.report.Ready ||
		d.report.Late != 0 || d.report.DuplicateReviews != 0 || d.report.ReviewMaxSeconds > 3600 {
		t.Fatalf("one-hour review SLA failed: ready=%d reviewed=%d late=%d max=%.0fs",
			d.report.Ready, d.report.Reviewed, d.report.Late, d.report.ReviewMaxSeconds)
	}
	if d.quota.Stats().Primary == 0 || d.report.Created == 0 || d.report.Deleted == 0 ||
		d.report.RepositoryWebhooks != d.report.Created+d.report.Deleted+d.report.Archived {
		t.Fatal("scenario did not exercise primary throttling and daily repository churn")
	}
	if developers >= 5000 && (d.quota.Stats().Secondary == 0 || d.report.Archived == 0 || d.report.Replays == 0) {
		t.Fatal("full scenario did not exercise secondary throttling, archive and replay")
	}
	broker := d.broker.report
	if broker.Jobs == 0 || broker.Jobs != broker.Exchanges || broker.Jobs != broker.Invalidations ||
		broker.Jobs != broker.Revocations || len(d.broker.tokens) != 0 || len(d.broker.jobs) != 0 {
		t.Fatalf("broker lifecycle incomplete: %+v", broker)
	}
	if developers >= 5000 && broker.Throttled == 0 {
		t.Fatal("full scenario did not exercise broker throttling")
	}
}

func TestGitHubSimulatorFleetSmoke(t *testing.T) {
	runFleetStress(t, 10, 10, 2)
}

func TestGitHubSimulatorFleetStress(t *testing.T) {
	if os.Getenv("CAO_FLEET_STRESS") != "1" {
		t.Skip("set CAO_FLEET_STRESS=1 for the 1000-repository, 5000-developer simulation")
	}
	runFleetStress(t, 1000, 5000, 3)
}
