package simulator

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxRepositories = 50000
	maxEvents       = 1000000
	maxConcurrency  = 512
)

// Scenario describes deterministic webhook traffic and time-bounded API
// behavior. Durations in API windows are measured from simulator startup.
type Scenario struct {
	Name                     string      `json:"name"`
	Repositories             int         `json:"repositories"`
	EventsPerRepository      int         `json:"events_per_repository"`
	IssueEventsPerRepository int         `json:"issue_events_per_repository,omitempty"`
	Seed                     int64       `json:"seed"`
	Distribution             string      `json:"distribution"`
	OutOfOrder               bool        `json:"out_of_order"`
	DuplicateEvery           int         `json:"duplicate_every"`
	DropEvery                int         `json:"drop_every"`
	DelayEvery               int         `json:"delay_every"`
	Delay                    string      `json:"delay"`
	ReplayCount              int         `json:"replay_count"`
	RemoveRepositories       bool        `json:"remove_repositories"`
	CreateRepositories       bool        `json:"create_repositories,omitempty"`
	ArchiveRepositories      bool        `json:"archive_repositories,omitempty"`
	UnarchiveRepositories    bool        `json:"unarchive_repositories,omitempty"`
	DeleteRepositories       bool        `json:"delete_repositories,omitempty"`
	RenameRepositories       bool        `json:"rename_repositories,omitempty"`
	API                      []APIWindow `json:"api"`
	// RateLimit optionally meters GitHub API traffic with a fixed-window
	// primary rate limit, so load tests can exhaust a deliberately low budget.
	RateLimit         *APIRateLimit `json:"rate_limit,omitempty"`
	History           *History      `json:"history,omitempty"`
	Faults            []APIFault    `json:"faults,omitempty"`
	WebhookRetryLimit int           `json:"webhook_retry_limit,omitempty"`
}

// APIRateLimit is a fixed-window primary rate limit. Window is measured in
// scenario time, so it is compressed by the simulator time scale.
type APIRateLimit struct {
	Limit  int    `json:"limit"`
	Window string `json:"window"`
}

// APIWindow selects a GitHub API behavior for a duration range.
type APIWindow struct {
	From                string  `json:"from"`
	To                  string  `json:"to"`
	Mode                string  `json:"mode"`
	Latency             string  `json:"latency,omitempty"`
	FailureRate         float64 `json:"failure_rate,omitempty"`
	FailEvery           int     `json:"fail_every,omitempty"`
	RateLimitRemaining  int     `json:"rate_limit_remaining,omitempty"`
	RateLimitResetAfter int     `json:"rate_limit_reset_after_seconds,omitempty"`
}

// Delivery is a signed webhook request ready for delivery to CAO.
type Delivery struct {
	ID        string
	Event     string
	Payload   []byte
	Sequence  int
	Delayed   bool
	Bootstrap bool
}

// LoadScenario decodes a strict JSON scenario and applies safe defaults.
func LoadScenario(data []byte) (Scenario, error) {
	var scenario Scenario
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("decode simulator scenario: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Scenario{}, errors.New("decode simulator scenario: multiple JSON values")
	}
	scenario.defaults()
	if err := scenario.Validate(); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

func (s *Scenario) defaults() {
	if s.EventsPerRepository == 0 {
		s.EventsPerRepository = 1
	}
	if s.Seed == 0 {
		s.Seed = 1
	}
	if s.Distribution == "" {
		s.Distribution = "uniform"
	}
}

// Validate checks scenario bounds before allocating repository or event data.
func (s Scenario) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("simulator scenario name is required")
	}
	if s.Repositories < 1 || s.Repositories > maxRepositories {
		return fmt.Errorf("simulator repositories must be between 1 and %d", maxRepositories)
	}
	if s.EventsPerRepository < 0 || s.EventsPerRepository > maxEvents/s.Repositories {
		return fmt.Errorf("simulator events exceed the %d event limit", maxEvents)
	}
	if s.IssueEventsPerRepository < 0 || s.IssueEventsPerRepository > maxEvents/s.Repositories-s.EventsPerRepository {
		return fmt.Errorf("simulator events exceed the %d event limit", maxEvents)
	}
	switch s.Distribution {
	case "uniform", "hot", "long-tail", "synchronized":
	default:
		return fmt.Errorf("unsupported simulator distribution %q", s.Distribution)
	}
	for name, every := range map[string]int{
		"duplicate_every": s.DuplicateEvery,
		"drop_every":      s.DropEvery,
		"delay_every":     s.DelayEvery,
	} {
		if every < 0 {
			return fmt.Errorf("simulator %s cannot be negative", name)
		}
	}
	if s.DelayEvery > 0 {
		delay, err := time.ParseDuration(s.Delay)
		if err != nil || delay <= 0 {
			return errors.New("simulator delay must be a positive duration when delay_every is set")
		}
	}
	if s.ReplayCount < 0 || s.ReplayCount > s.Repositories*s.EventsPerRepository {
		return errors.New("simulator replay_count exceeds generated workflow events")
	}
	if s.UnarchiveRepositories && !s.ArchiveRepositories {
		return errors.New("simulator unarchive_repositories requires archive_repositories")
	}
	if s.WebhookRetryLimit < 0 || s.WebhookRetryLimit > 30 {
		return errors.New("simulator webhook_retry_limit must be between 0 and 30")
	}
	if s.RateLimit != nil {
		if s.RateLimit.Limit < 1 || s.RateLimit.Limit > 1_000_000 {
			return errors.New("simulator rate_limit.limit must be between 1 and 1000000")
		}
		if s.History != nil {
			if err := s.History.Validate(s.Repositories); err != nil {
				return err
			}
		}
		if len(s.Faults) > 100 {
			return errors.New("simulator cannot have more than 100 proxy faults")
		}
		for _, fault := range s.Faults {
			if err := fault.Validate(); err != nil {
				return err
			}
		}
		window, err := time.ParseDuration(s.RateLimit.Window)
		if err != nil || window < time.Millisecond {
			return errors.New("simulator rate_limit.window must be a duration of at least 1ms")
		}
	}
	if len(s.API) > 100 {
		return errors.New("simulator cannot have more than 100 API windows")
	}
	for i, window := range s.API {
		from, fromErr := time.ParseDuration(window.From)
		to, toErr := time.ParseDuration(window.To)
		if fromErr != nil || toErr != nil || from < 0 || to <= from {
			return fmt.Errorf("simulator API window %d must have valid increasing from/to durations", i)
		}
		if window.FailureRate < 0 || window.FailureRate > 1 || math.IsNaN(window.FailureRate) {
			return fmt.Errorf("simulator API window %d failure_rate must be between 0 and 1", i)
		}
		if window.FailEvery < 0 || window.RateLimitRemaining < 0 || window.RateLimitResetAfter < 0 {
			return fmt.Errorf("simulator API window %d numeric values cannot be negative", i)
		}
		switch window.Mode {
		case "healthy", "latency", "timeout", "connection-failure",
			"rate-limited", "secondary-rate-limit", "internal-error",
			"bad-gateway", "service-unavailable", "unavailable", "intermittent":
		default:
			return fmt.Errorf("simulator API window %d has unsupported mode %q", i, window.Mode)
		}
		if window.Mode == "latency" {
			latency, err := time.ParseDuration(window.Latency)
			if err != nil || latency <= 0 {
				return fmt.Errorf("simulator API window %d latency must be a positive duration", i)
			}
		}
		if i > 0 {
			previousTo, _ := time.ParseDuration(s.API[i-1].To)
			if from < previousTo {
				return errors.New("simulator API windows must be sorted and non-overlapping")
			}
		}
	}
	return nil
}

// Generate creates installation, workflow_run.completed, and optional issues webhook events
// using only the scenario seed, so repeated runs produce identical traffic.
func (s Scenario) Generate() ([]Delivery, error) {
	s.defaults()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	// #nosec G404 -- deterministic weak randomness is required for repeatable scenarios.
	rng := rand.New(rand.NewSource(s.Seed))
	repositories := make([]string, s.Repositories)
	for i := range repositories {
		repositories[i] = fmt.Sprintf("simulator/repo-%05d", i+1)
	}

	deliveries := make([]Delivery, 0, 1+(s.Repositories+99)/100+s.Repositories*(s.EventsPerRepository+s.IssueEventsPerRepository)+1)
	appendDelivery := func(event string, payload any, bootstrap bool) error {
		content, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		deliveries = append(deliveries, Delivery{
			ID:        deliveryID(s.Seed, len(deliveries)),
			Event:     event,
			Payload:   content,
			Sequence:  len(deliveries),
			Bootstrap: bootstrap,
		})
		return nil
	}
	if err := appendDelivery("installation", map[string]any{
		"action":       "created",
		"installation": map[string]any{"id": 1, "account": map[string]string{"login": "simulator"}},
		"sender":       map[string]string{"login": "simulator"},
	}, true); err != nil {
		return nil, err
	}
	for start := 0; start < len(repositories); start += 100 {
		end := min(start+100, len(repositories))
		items := make([]map[string]string, 0, end-start)
		for _, name := range repositories[start:end] {
			items = append(items, map[string]string{"full_name": name})
		}
		if err := appendDelivery("installation_repositories", map[string]any{
			"action": "added", "installation": map[string]any{"id": 1},
			"repositories_added": items, "repositories_removed": []any{},
		}, true); err != nil {
			return nil, err
		}
	}
	if s.CreateRepositories {
		if err := appendDelivery("repository", map[string]any{
			"action": "created", "repository": map[string]any{"id": 1, "full_name": repositories[0]},
			"installation": map[string]int{"id": 1},
		}, true); err != nil {
			return nil, err
		}
	}

	workflowEvents := make([]Delivery, 0, s.Repositories*s.EventsPerRepository)
	for i := 0; i < s.Repositories*s.EventsPerRepository; i++ {
		repositoryIndex := selectRepository(rng, s.Distribution, s.Repositories)
		name := repositories[repositoryIndex]
		createdAt := time.Unix(1_800_000_000+int64(i), 0).UTC()
		if s.Distribution == "synchronized" {
			createdAt = time.Unix(1_800_000_000, 0).UTC()
		}
		payload, err := json.Marshal(map[string]any{
			"action": "completed",
			"workflow_run": map[string]any{
				"id": i + 1, "name": "simulated workflow", "status": "completed",
				"conclusion": "success", "created_at": createdAt.Format(time.RFC3339),
				"updated_at": createdAt.Format(time.RFC3339), "head_branch": "main",
				"repository": map[string]any{"full_name": name},
			},
			"repository":   map[string]string{"full_name": name},
			"installation": map[string]int{"id": 1},
		})
		if err != nil {
			return nil, err
		}
		workflowEvents = append(workflowEvents, Delivery{
			ID:       deliveryID(s.Seed, len(deliveries)+i),
			Event:    "workflow_run",
			Payload:  payload,
			Sequence: i,
			Delayed:  s.DelayEvery > 0 && (i+1)%s.DelayEvery == 0,
		})
	}
	if s.OutOfOrder {
		rng.Shuffle(len(workflowEvents), func(i, j int) {
			workflowEvents[i], workflowEvents[j] = workflowEvents[j], workflowEvents[i]
		})
	}
	deliveries = append(deliveries, workflowEvents...)
	issueActions := []string{"opened", "closed", "reopened", "edited"}
	for _, name := range repositories {
		for i := 0; i < s.IssueEventsPerRepository; i++ {
			action := issueActions[i%len(issueActions)]
			at := time.Unix(1_800_000_000+int64(i), 0).UTC().Format(time.RFC3339)
			issue := map[string]any{
				"number": 1, "state": "open", "state_reason": nil,
				"closed_at": nil, "updated_at": at,
				"html_url": fmt.Sprintf("https://github.com/%s/issues/1", name),
			}
			if action == "closed" {
				issue["state"] = "closed"
				issue["state_reason"] = "completed"
				issue["closed_at"] = at
			}
			if err := appendDelivery("issues", map[string]any{
				"action": action, "issue": issue,
				"repository":   map[string]string{"full_name": name},
				"installation": map[string]int{"id": 1},
			}, false); err != nil {
				return nil, err
			}
		}
	}
	if s.RemoveRepositories {
		if err := appendDelivery("installation_repositories", map[string]any{
			"action": "removed", "installation": map[string]any{"id": 1},
			"repositories_added": []any{}, "repositories_removed": []map[string]string{
				{"full_name": repositories[0]},
			},
		}, false); err != nil {
			return nil, err
		}
	}
	for _, event := range []struct {
		enabled bool
		action  string
	}{{s.ArchiveRepositories, "archived"}, {s.UnarchiveRepositories, "unarchived"},
		{s.RenameRepositories, "renamed"}, {s.DeleteRepositories, "deleted"}} {
		if !event.enabled {
			continue
		}
		name := repositories[0]
		if s.RenameRepositories && (event.action == "renamed" || event.action == "deleted") {
			name += "-renamed"
		}
		payload := map[string]any{
			"action": event.action, "repository": map[string]any{"id": 1, "full_name": name},
			"installation": map[string]int{"id": 1},
		}
		if event.action == "renamed" {
			payload["changes"] = map[string]any{"repository": map[string]any{
				"name": map[string]string{"from": "repo-00001"}}}
		}
		if err := appendDelivery("repository", payload, false); err != nil {
			return nil, err
		}
	}
	return deliveries, nil
}

func selectRepository(rng *rand.Rand, distribution string, count int) int {
	switch distribution {
	case "hot":
		hotCount := max(1, count/100)
		if rng.Float64() < 0.8 {
			return rng.Intn(hotCount)
		}
		return rng.Intn(count)
	case "long-tail":
		return min(count-1, int(math.Pow(rng.Float64(), 4)*float64(count)))
	default:
		return rng.Intn(count)
	}
}

func deliveryID(seed int64, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", seed, index)))
	raw := hex.EncodeToString(sum[:16])
	return raw[:8] + "-" + raw[8:12] + "-4" + raw[13:16] + "-a" + raw[17:20] + "-" + raw[20:]
}

// RunResult summarizes webhook requests without including payloads or secrets.
type RunResult struct {
	Attempts   int `json:"attempts"`
	Accepted   int `json:"accepted"`
	Failed     int `json:"failed"`
	Dropped    int `json:"dropped"`
	Duplicates int `json:"duplicates"`
	Replayed   int `json:"replayed"`
	Retries    int `json:"retries"`
}

type webhookRetryKey struct{}
type webhookRetryConfig struct {
	limit int
	total *atomic.Int64
}

// Deliver sends the scenario's signed events to the real CAO webhook endpoint.
// It uses bounded concurrency and reuses delivery IDs for duplicates/replays.
func (s Scenario) Deliver(ctx context.Context, client *http.Client, endpoint, secret string, concurrency int) (result RunResult, err error) {
	var retries atomic.Int64
	ctx = context.WithValue(ctx, webhookRetryKey{}, webhookRetryConfig{limit: s.WebhookRetryLimit, total: &retries})
	defer func() { result.Retries = int(retries.Load()) }()
	if client == nil {
		client = http.DefaultClient
	}
	transport, err := NewLocalTransport(endpoint)
	if err != nil {
		return RunResult{}, err
	}
	defer transport.CloseIdleConnections()
	localClient := *client
	localClient.Transport = transport
	client = &localClient
	if strings.TrimSpace(secret) == "" {
		return RunResult{}, errors.New("webhook signing secret is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return RunResult{}, errors.New("simulator webhook endpoint must be an HTTP or HTTPS URL")
	}
	deliveries, err := s.Generate()
	if err != nil {
		return RunResult{}, err
	}
	if concurrency <= 0 {
		concurrency = 64
	}
	if concurrency > maxConcurrency {
		return RunResult{}, fmt.Errorf("simulator concurrency cannot exceed %d", maxConcurrency)
	}

	bootstrap := make([]Delivery, 0)
	delayed := make([]Delivery, 0)
	primary := make([]Delivery, 0, len(deliveries))
	final := make([]Delivery, 0)
	workflowEvents := make([]Delivery, 0, s.Repositories*s.EventsPerRepository)
	workflowCount := 0
	for _, delivery := range deliveries {
		if delivery.Bootstrap {
			bootstrap = append(bootstrap, delivery)
			continue
		}
		if delivery.Event == "workflow_run" {
			workflowCount++
			workflowEvents = append(workflowEvents, delivery)
			if s.DropEvery > 0 && workflowCount%s.DropEvery == 0 {
				result.Dropped++
				continue
			}
			if delivery.Delayed {
				delayed = append(delayed, delivery)
				continue
			}
		} else {
			final = append(final, delivery)
			continue
		}
		primary = append(primary, delivery)
		if delivery.Event == "workflow_run" && s.DuplicateEvery > 0 && workflowCount%s.DuplicateEvery == 0 {
			primary = append(primary, delivery)
			result.Duplicates++
		}
	}
	result.Attempts = len(bootstrap)
	accepted, failed, err := sendDeliveries(ctx, client, endpoint, secret, bootstrap, 1)
	result.Accepted += accepted
	result.Failed += failed
	if err != nil {
		return result, err
	}
	if s.DuplicateEvery > 0 {
		for i := range delayed {
			if (delayed[i].Sequence+1)%s.DuplicateEvery == 0 {
				delayed = append(delayed, delayed[i])
				result.Duplicates++
			}
		}
	}
	result.Attempts += len(primary)
	accepted, failed, err = sendDeliveries(ctx, client, endpoint, secret, primary, concurrency)
	result.Accepted += accepted
	result.Failed += failed
	if err != nil {
		return result, err
	}
	if len(delayed) > 0 {
		if delay, _ := time.ParseDuration(s.Delay); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return result, ctx.Err()
			case <-timer.C:
			}
		}
		result.Attempts += len(delayed)
		accepted, failed, err = sendDeliveries(ctx, client, endpoint, secret, delayed, concurrency)
		result.Accepted += accepted
		result.Failed += failed
		if err != nil {
			return result, err
		}
	}
	if s.ReplayCount > 0 {
		sort.Slice(workflowEvents, func(i, j int) bool {
			return workflowEvents[i].Sequence < workflowEvents[j].Sequence
		})
		replay := make([]Delivery, 0, s.ReplayCount)
		start := max(0, len(workflowEvents)-s.ReplayCount)
		for i := len(workflowEvents) - 1; i >= start; i-- {
			replay = append(replay, workflowEvents[i])
		}
		result.Attempts += len(replay)
		result.Replayed = len(replay)
		result.Duplicates += len(replay)
		accepted, failed, err = sendDeliveries(ctx, client, endpoint, secret, replay, concurrency)
		result.Accepted += accepted
		result.Failed += failed
		if err != nil {
			return result, err
		}
	}
	if len(final) > 0 {
		result.Attempts += len(final)
		accepted, failed, err = sendDeliveries(ctx, client, endpoint, secret, final, 1)
		result.Accepted += accepted
		result.Failed += failed
		if err != nil {
			return result, err
		}
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("simulator webhook delivery had %d failed requests", result.Failed)
	}
	return result, nil
}

func sendDeliveries(ctx context.Context, client *http.Client, endpoint, secret string, deliveries []Delivery, concurrency int) (accepted, failed int, err error) {
	jobs := make(chan Delivery)
	results := make(chan bool, len(deliveries))
	workers := min(concurrency, max(1, len(deliveries)))
	for range workers {
		go func() {
			for delivery := range jobs {
				results <- sendDelivery(ctx, client, endpoint, secret, delivery)
			}
		}()
	}
	for _, delivery := range deliveries {
		select {
		case <-ctx.Done():
			close(jobs)
			return accepted, failed, ctx.Err()
		case jobs <- delivery:
		}
	}
	close(jobs)
	for range deliveries {
		select {
		case <-ctx.Done():
			return accepted, failed, ctx.Err()
		case ok := <-results:
			if ok {
				accepted++
			} else {
				failed++
			}
		}
	}
	return accepted, failed, nil
}

func sendDelivery(ctx context.Context, client *http.Client, endpoint, secret string, delivery Delivery) bool {
	config, _ := ctx.Value(webhookRetryKey{}).(webhookRetryConfig)
	for attempt := 0; attempt <= config.limit; attempt++ {
		status, retryAfter := sendDeliveryOnce(ctx, client, endpoint, secret, delivery)
		if status >= 200 && status < 300 {
			return true
		}
		if attempt == config.limit || (status != 0 && status != http.StatusTooManyRequests &&
			status != http.StatusBadGateway && status != http.StatusServiceUnavailable && status != http.StatusGatewayTimeout) {
			return false
		}
		config.total.Add(1)
		delay := time.Duration(min(attempt+1, 10)) * 100 * time.Millisecond
		if retryAfter > 0 {
			delay = retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return false
}

// SignedRequest constructs the same HMAC-authenticated request used by network
// simulation and by the local gateway's in-process HTTP ingress.
func SignedRequest(ctx context.Context, endpoint, secret string, delivery Delivery) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(delivery.Payload))
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(delivery.Payload)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", delivery.Event)
	request.Header.Set("X-GitHub-Delivery", delivery.ID)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return request, nil
}

func sendDeliveryOnce(ctx context.Context, client *http.Client, endpoint, secret string, delivery Delivery) (int, time.Duration) {
	request, err := SignedRequest(ctx, endpoint, secret, delivery)
	if err != nil {
		return 0, 0
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, 0
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	delay := time.Duration(0)
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
		delay = time.Duration(seconds) * time.Second
	} else if retryAt, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		delay = time.Until(retryAt)
	}
	return response.StatusCode, delay
}
