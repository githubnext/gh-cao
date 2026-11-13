// cao-cli-quota bridges a local CLI queue to the shared GitHub quota service.
// Its stdin/stdout protocol contains only scheduling metadata, never credentials.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

var quotaLog = logger.New("cao:cli-quota")

type request struct {
	ID                int      `json:"id"`
	Operation         string   `json:"operation"`
	ReservationID     string   `json:"reservationId"`
	MaxConcurrency    int      `json:"maxConcurrency"`
	EstimatedCost     int      `json:"estimatedCost"`
	MinimumRemaining  int      `json:"minimumRemaining"`
	Running           int      `json:"running"`
	Resources         []string `json:"resources,omitempty"`
	RateLimited       bool     `json:"rateLimited"`
	RetryAfterSeconds int      `json:"retryAfterSeconds"`
}

type decision struct {
	ID            int    `json:"id"`
	Capacity      int    `json:"capacity"`
	Reason        string `json:"reason"`
	RetryAt       string `json:"retryAt,omitempty"`
	ReservationID string `json:"reservationId,omitempty"`
}

type meter struct {
	Limit     *int  `json:"limit"`
	Remaining *int  `json:"remaining"`
	Reset     int64 `json:"reset"`
}

type snapshot struct {
	identity     string
	observations map[string]githubquota.Observation
}

type reservation struct {
	buckets  []githubquota.Reservation
	finished bool
}

type bridge struct {
	service      *githubquota.Service
	read         func(context.Context, []string) (snapshot, error)
	reservations map[string]*reservation
	now          func() time.Time
	parkedUntil  time.Time
}

func newBridge(service *githubquota.Service, read func(context.Context, []string) (snapshot, error)) *bridge {
	return &bridge{service: service, read: read, reservations: make(map[string]*reservation), now: time.Now}
}

// The synthetic App key is a namespace for a non-App gh credential context.
// Installation=1 is a fixed namespace discriminator, not a claimed installation.
func bucket(identity, resource string) githubquota.BucketID {
	return githubquota.BucketID{App: identity, Installation: 1, Resource: resource}
}

func (b *bridge) unknown(id int) decision {
	return decision{ID: id, Reason: "unknown", RetryAt: b.now().Add(15 * time.Second).UTC().Format(time.RFC3339)}
}

// release returns every bucket held by a reservation; unknown IDs are a no-op.
func (b *bridge) release(ctx context.Context, id string) error {
	held := b.reservations[id]
	if held == nil {
		return nil
	}
	for _, r := range held.buckets {
		if err := b.service.Release(ctx, r); err != nil {
			return err
		}
	}
	delete(b.reservations, id)
	quotaLog.Printf("released reservation: buckets=%d", len(held.buckets))
	return nil
}

// limits applies the documented defaults to unset admission parameters.
func (r request) limits() (concurrency, cost, floor int) {
	concurrency, cost, floor = r.MaxConcurrency, r.EstimatedCost, r.MinimumRemaining
	if concurrency <= 0 {
		concurrency = 4
	}
	if cost <= 0 {
		cost = 50
	}
	if floor <= 0 {
		floor = 100
	}
	return concurrency, cost, floor
}

// parkDuration clamps a retry-after hint to between one minute and one day.
func parkDuration(retryAfterSeconds int) time.Duration {
	return time.Duration(min(max(retryAfterSeconds, 60), 86400)) * time.Second
}

func (b *bridge) handle(ctx context.Context, req request) decision {
	if req.Operation == "release" {
		if err := b.release(ctx, req.ReservationID); err != nil {
			return b.unknown(req.ID)
		}
		return decision{ID: req.ID, Reason: "available"}
	}
	if req.Operation != "admit" && req.Operation != "complete" && req.Operation != "status" {
		return b.unknown(req.ID)
	}
	resources, err := requiredResources(req.Resources)
	if err != nil {
		return b.unknown(req.ID)
	}
	if req.Operation == "complete" {
		if held := b.reservations[req.ReservationID]; held != nil {
			held.finished = true
			resources = nil
			for _, r := range held.buckets {
				resources = append(resources, r.Bucket.Resource)
			}
		}
		if req.RateLimited {
			if err := b.parkAfterRateLimit(ctx, req.RetryAfterSeconds); err != nil {
				return b.unknown(req.ID)
			}
		}
	}
	if b.parkedUntil.After(b.now()) {
		return decision{ID: req.ID, Reason: "parked", RetryAt: b.parkedUntil.UTC().Format(time.RFC3339)}
	}
	current, err := b.refresh(ctx, resources)
	if err != nil {
		return b.unknown(req.ID)
	}
	concurrency, cost, floor := req.limits()
	active := b.activeCount()
	room := b.headroom(ctx, current.identity, resources, concurrency, cost, floor)
	switch room.status {
	case githubquota.StatusUnknown:
		return b.unknown(req.ID)
	case githubquota.StatusParked:
		return decision{ID: req.ID, Reason: "parked", RetryAt: room.parkedUntil.UTC().Format(time.RFC3339)}
	}
	available, retry := room.available, room.resetAt
	capacity, reason := admissionOutcome(req.Operation, concurrency, active, req.Running, available)
	switch reason {
	case "exhausted":
		return decision{ID: req.ID, Reason: reason, RetryAt: retry.UTC().Format(time.RFC3339)}
	case "concurrency":
		return decision{ID: req.ID, Capacity: capacity, Reason: reason}
	}
	result := decision{ID: req.ID, Capacity: capacity, Reason: reason}
	if req.Operation != "admit" {
		return result
	}
	held, err := b.reserveAll(ctx, current.identity, resources, cost, floor)
	if err != nil {
		return b.unknown(req.ID)
	}
	result.ReservationID = held[0].ID
	b.reservations[result.ReservationID] = &reservation{buckets: held}
	quotaLog.Printf("admitted reservation: buckets=%d active=%d capacity=%d", len(held), active, result.Capacity)
	return result
}

// quotaRoom is the quota-limited admission room across a set of resources.
// status is StatusUnknown or StatusParked when any bucket stops admission and
// StatusAvailable otherwise; available may still be zero when exhausted.
type quotaRoom struct {
	status      githubquota.Status
	available   int
	resetAt     time.Time
	parkedUntil time.Time
}

// headroom folds the state of every resource bucket into the number of
// additional commands the quota can admit, capped at concurrency. An
// unreadable or unknown bucket pauses admission, and a parked bucket reports
// its resume time.
func (b *bridge) headroom(ctx context.Context, identity string, resources []string, concurrency, cost, floor int) quotaRoom {
	room := quotaRoom{status: githubquota.StatusAvailable, available: concurrency}
	for _, resource := range resources {
		state, err := b.service.State(ctx, bucket(identity, resource))
		if err != nil || state.Status == githubquota.StatusUnknown {
			quotaLog.Printf("quota headroom paused: status=%s resources=%d", githubquota.StatusUnknown, len(resources))
			return quotaRoom{status: githubquota.StatusUnknown}
		}
		if state.Status == githubquota.StatusParked {
			quotaLog.Printf("quota headroom paused: status=%s resources=%d", githubquota.StatusParked, len(resources))
			return quotaRoom{status: githubquota.StatusParked, parkedUntil: state.ParkedUntil}
		}
		room.available = min(room.available, max(0, (state.Available-floor)/cost))
		room.resetAt = maxTime(room.resetAt, state.ResetAt)
	}
	return room
}

// admissionOutcome classifies an admission attempt from the quota-limited
// headroom: "exhausted" when no quota remains, "concurrency" when an admit
// request finds the active or externally running count at capacity, and
// "available" otherwise.
func admissionOutcome(operation string, concurrency, active, running, available int) (capacity int, reason string) {
	if available <= 0 {
		quotaLog.Printf("admission exhausted: concurrency=%d", concurrency)
		return 0, "exhausted"
	}
	capacity = min(concurrency, active+available)
	if operation == "admit" && max(active, running) >= capacity {
		quotaLog.Printf("admission at concurrency bound: active=%d running=%d capacity=%d", active, running, capacity)
		return capacity, "concurrency"
	}
	return capacity, "available"
}

// parkAfterRateLimit extends the bridge-wide park and parks every held bucket:
// command output does not identify which resource incurred a secondary limit.
func (b *bridge) parkAfterRateLimit(ctx context.Context, retryAfterSeconds int) error {
	wait := parkDuration(retryAfterSeconds)
	b.parkedUntil = maxTime(b.parkedUntil, b.now().Add(wait))
	quotaLog.Printf("parking quota after rate limit: wait_seconds=%d reservations=%d", int(wait.Seconds()), len(b.reservations))
	for _, held := range b.reservations {
		for _, r := range held.buckets {
			if err := b.service.Park(ctx, r.Bucket, b.parkedUntil, githubquota.ParkReasonSecondaryRateLimit); err != nil {
				return err
			}
		}
	}
	return nil
}

// refresh reads a fresh snapshot, requires a usable observation for every
// resource, records them, and reconciles finished reservations.
func (b *bridge) refresh(ctx context.Context, resources []string) (snapshot, error) {
	current, err := b.read(ctx, resources)
	if err != nil {
		return snapshot{}, err
	}
	for _, resource := range resources {
		observation, ok := current.observations[resource]
		if !ok || observation.ResetAt.Before(b.now()) || observation.Limit <= 0 {
			return snapshot{}, errors.New("GitHub quota observation is unusable")
		}
		if err := b.service.Observe(ctx, bucket(current.identity, resource), observation); err != nil {
			return snapshot{}, err
		}
	}
	if err := b.reconcile(ctx, current); err != nil {
		return snapshot{}, err
	}
	return current, nil
}

// activeCount returns the number of reservations whose command has not finished.
func (b *bridge) activeCount() int {
	active := 0
	for _, held := range b.reservations {
		if !held.finished {
			active++
		}
	}
	return active
}

// reserveAll reserves every resource or none: a failure releases the buckets
// already reserved.
func (b *bridge) reserveAll(ctx context.Context, identity string, resources []string, cost, floor int) ([]githubquota.Reservation, error) {
	var held []githubquota.Reservation
	for _, resource := range resources {
		r, err := b.service.Reserve(ctx, bucket(identity, resource), githubquota.ReservationRequest{
			EstimatedCost: cost, MinimumRemain: floor, TTL: 20 * time.Minute,
		})
		if err != nil {
			for _, previous := range held {
				_ = b.service.Release(ctx, previous)
			}
			return nil, err
		}
		held = append(held, r)
	}
	return held, nil
}

// reconcile settles finished reservations against a fresh observation. Never
// infer command cost: only a fresh GitHub observation reconciles completed
// work, and a failed refresh leaves its reservation held.
func (b *bridge) reconcile(ctx context.Context, current snapshot) error {
	settled := 0
	for id, held := range b.reservations {
		done := true
		for _, r := range held.buckets {
			if held.finished && r.Bucket.App == current.identity {
				observation, ok := current.observations[r.Bucket.Resource]
				if !ok {
					done = false
					continue
				}
				if err := b.service.Commit(ctx, r, observation); err != nil {
					return err
				}
			} else if r.ExpiresAt.After(b.now()) {
				done = false
			}
		}
		if done {
			delete(b.reservations, id)
			settled++
		}
	}
	if settled > 0 {
		quotaLog.Printf("reconciled reservations: settled=%d remaining=%d", settled, len(b.reservations))
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

func requiredResources(resources []string) ([]string, error) {
	if len(resources) == 0 {
		return []string{"core", "graphql"}, nil
	}
	seen := make(map[string]bool)
	for _, resource := range resources {
		if (resource != "core" && resource != "graphql") || seen[resource] {
			return nil, errors.New("invalid GitHub quota resource set")
		}
		seen[resource] = true
	}
	return resources, nil
}

// parseUserID decodes the `gh api user --jq .id` output into a positive
// numeric credential identity.
func parseUserID(output []byte) (int64, error) {
	var userID int64
	if err := json.Unmarshal(bytes.TrimSpace(output), &userID); err != nil || userID <= 0 {
		quotaLog.Printf("rejected credential identity: reason=invalid_user_id")
		return 0, errors.New("GitHub credential identity is invalid")
	}
	return userID, nil
}

func ghSnapshot(ctx context.Context, run commandRunner, executable, host string, required ...string) (snapshot, error) {
	resources, err := requiredResources(required)
	if err != nil {
		return snapshot{}, err
	}
	user, err := run(ctx, executable, "api", "--hostname", host, "user", "--jq", ".id")
	if err != nil {
		return snapshot{}, errors.New("GitHub credential identity is unavailable")
	}
	userID, err := parseUserID(user)
	if err != nil {
		return snapshot{}, err
	}
	data, err := run(ctx, executable, "api", "--hostname", host, "rate_limit")
	if err != nil {
		return snapshot{}, errors.New("GitHub quota observation is unavailable")
	}
	return snapshotFromRateLimit(host, userID, data, resources)
}

// snapshotFromRateLimit derives the credential identity and validates every
// required meter in a `gh api rate_limit` payload.
func snapshotFromRateLimit(host string, userID int64, data []byte, resources []string) (snapshot, error) {
	var payload struct {
		Resources map[string]meter `json:"resources"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		quotaLog.Printf("rejected quota snapshot: reason=invalid_payload")
		return snapshot{}, errors.New("GitHub quota observation is invalid")
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d", strings.ToLower(host), userID))
	result := snapshot{identity: "gh-" + hex.EncodeToString(sum[:24]), observations: make(map[string]githubquota.Observation)}
	for _, resource := range resources {
		m, ok := payload.Resources[resource]
		if !ok || m.Limit == nil || m.Remaining == nil || *m.Limit <= 0 || *m.Remaining < 0 || *m.Remaining > *m.Limit || m.Reset <= 0 {
			quotaLog.Printf("rejected quota snapshot: reason=incomplete_meter resources=%d", len(resources))
			return snapshot{}, errors.New("GitHub quota observation is incomplete")
		}
		result.observations[resource] = githubquota.Observation{
			Limit: *m.Limit, Remaining: *m.Remaining, ResetAt: time.Unix(m.Reset, 0).UTC(),
		}
	}
	return result, nil
}

func serve(input io.Reader, output io.Writer, b *bridge) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		if err := encoder.Encode(b.handleLine(scanner.Bytes())); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// handleLine answers one JSONL protocol line. A malformed line yields an
// "unknown" decision with ID 0 instead of terminating the stream.
func (b *bridge) handleLine(line []byte) decision {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		quotaLog.Printf("rejected request line: reason=invalid_json bytes=%d", len(line))
		return b.unknown(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return b.handle(ctx, req)
}

func run() error {
	store, err := memory.New(memory.Config{})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	service, err := githubquota.New(store, githubquota.Options{})
	if err != nil {
		return err
	}
	host := os.Getenv("GH_HOST")
	if host == "" {
		host = "github.com"
	}
	executable := os.Getenv("CAO_CLI_QUOTA_GH_EXECUTABLE")
	if executable == "" {
		executable = "gh"
	}
	execute := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		// Output and errors are deliberately never forwarded to logs or JSONL.
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Executable is local operator configuration; arguments are fixed API reads.
		cmd.Dir = os.Getenv("CAO_CLI_QUOTA_WORKING_DIRECTORY")
		return cmd.Output()
	}
	b := newBridge(service, func(ctx context.Context, resources []string) (snapshot, error) {
		return ghSnapshot(ctx, execute, executable, host, resources...)
	})
	return serve(os.Stdin, os.Stdout, b)
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}
