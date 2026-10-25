package redisx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var storeLog = logger.New("cao:redis:store")

const ingestionHealthKey = "state:ingestion-health"

var ingestionCounterNames = map[string]struct{}{
	"webhookReceived":        {},
	"webhookDuplicate":       {},
	"webhookAdmissionFailed": {},
	"taskQueued":             {},
	"taskCoalesced":          {},
	"collectionSucceeded":    {},
	"collectionFailed":       {},
	"collectionRetried":      {},
	"collectionDeadLettered": {},
	"adxExported":            {},
	"adxFailure":             {},
	"adxGap":                 {},
}

var ingestionLoadNames = map[string]string{
	"webhookReceived":     "webhook",
	"collectionSucceeded": "collection",
	"collectionFailed":    "failure",
}

type Store struct {
	Client                CommandClient
	namespace             string
	processIsolated       bool
	maxMemoryBytes        atomic.Int64
	queryMaintenanceBytes atomic.Int64
	cacheInitMu           sync.Mutex
	cacheCapability       atomic.Uint32
}

type CommandClient interface {
	Do(context.Context, ...string) (any, error)
	DoMany(context.Context, [][]string) ([]any, error)
}

func NewStore(client CommandClient, namespaces ...string) *Store {
	var namespace string
	switch len(namespaces) {
	case 0:
		defaultNamespace, err := DefaultNamespace(".")
		if err != nil {
			panic(err)
		}
		namespace = defaultNamespace
	case 1:
		namespace = namespaces[0]
	default:
		panic("NewStore accepts at most one Redis namespace")
	}
	normalized, err := NormalizeNamespace(namespace)
	if err != nil {
		panic(err)
	}
	return &Store{Client: client, namespace: normalized}
}

func NewProcessIsolatedStore(client CommandClient, namespace string) (*Store, error) {
	normalized, err := NormalizeNamespace(namespace)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, errors.New("generate process-isolated Redis namespace")
	}
	sum := sha256.Sum256([]byte(normalized))
	isolated, err := NormalizeNamespace(
		"process-" + hex.EncodeToString(sum[:6]) + "-" + hex.EncodeToString(nonce[:]),
	)
	if err != nil {
		return nil, err
	}
	store := NewStore(client, isolated)
	store.processIsolated = true
	return store, nil
}

func (s *Store) ProcessIsolated() bool {
	return s != nil && s.processIsolated
}

func (s *Store) Ping(ctx context.Context) error {
	value, err := s.Client.Do(ctx, "PING")
	if err != nil {
		return err
	}
	if fmt.Sprint(value) != "PONG" {
		return errors.New("unexpected Redis PING response")
	}
	return nil
}

func (s *Store) TryLock(ctx context.Context, name, token string, ttl time.Duration) (bool, error) {
	value, err := s.Client.Do(ctx, "SET", s.Key("lock:"+name), token, "NX", "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	if err != nil {
		return false, err
	}
	return value != nil && fmt.Sprint(value) == "OK", nil
}

func (s *Store) Unlock(ctx context.Context, name, token string) error {
	script := `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
	_, err := s.Client.Do(ctx, "EVAL", script, "1", s.Key("lock:"+name), token)
	return err
}

func (s *Store) LockHeld(ctx context.Context, name string) (bool, error) {
	value, err := s.Client.Do(ctx, "GET", s.Key("lock:"+name))
	if err != nil {
		return false, err
	}
	return value != nil, nil
}

// ReserveRateLimit atomically checks and decrements one serialized rate-limit
// state. It returns 1 for a parked installation and 2 for exhausted headroom.
func (s *Store) ReserveRateLimit(
	ctx context.Context, key, field string, floor, cost int, now int64,
) (int, error) {
	script := `
local value = redis.call("HGET", KEYS[1], ARGV[1]) or ""
local remaining, reset, parked = string.match(value, "^(-?%d+)|(-?%d+)|(-?%d+)$")
if not remaining then return 3 end
remaining = tonumber(remaining) or 0
reset = tonumber(reset) or 0
parked = tonumber(parked) or 0
local now = tonumber(ARGV[4])
if parked > now then return 1 end
if reset > 0 and reset < now then
  return 3
end
local floor = tonumber(ARGV[2])
if remaining <= floor then return 2 end
remaining = math.max(remaining - tonumber(ARGV[3]), 0)
redis.call("HSET", KEYS[1], ARGV[1], remaining .. "|" .. reset .. "|" .. parked)
return 0`
	value, err := s.Client.Do(
		ctx, "EVAL", script, "1", s.Key(key), field,
		strconv.Itoa(floor), strconv.Itoa(cost), strconv.FormatInt(now, 10))
	if err != nil {
		return 0, err
	}
	result, err := strconv.Atoi(fmt.Sprint(value))
	if err != nil {
		return 0, errors.New("invalid rate-limit reservation response")
	}
	return result, nil
}

// ObserveRateLimit atomically records authoritative response headroom without
// increasing a same-window value that a concurrent reservation already lowered.
func (s *Store) ObserveRateLimit(
	ctx context.Context, key, field string, remaining int, reset int64,
) error {
	script := `
local value = redis.call("HGET", KEYS[1], ARGV[1]) or ""
local current, current_reset, parked = string.match(value, "^(-?%d+)|(-?%d+)|(-?%d+)$")
current = tonumber(current)
current_reset = tonumber(current_reset)
parked = tonumber(parked) or 0
local observed = tonumber(ARGV[2])
local observed_reset = tonumber(ARGV[3])
if current and current_reset == observed_reset then
  observed = math.min(current, observed)
end
redis.call("HSET", KEYS[1], ARGV[1], observed .. "|" .. observed_reset .. "|" .. parked)
return 0`
	_, err := s.Client.Do(
		ctx, "EVAL", script, "1", s.Key(key), field,
		strconv.Itoa(remaining), strconv.FormatInt(reset, 10))
	return err
}

// ParkRateLimit atomically extends an installation's backoff without changing
// its current headroom.
func (s *Store) ParkRateLimit(
	ctx context.Context, key, field string, parkedTo int64,
) error {
	script := `
local value = redis.call("HGET", KEYS[1], ARGV[1]) or ""
local remaining, reset, parked = string.match(value, "^(-?%d+)|(-?%d+)|(-?%d+)$")
remaining = tonumber(remaining) or 0
reset = tonumber(reset) or 0
parked = math.max(tonumber(parked) or 0, tonumber(ARGV[2]))
redis.call("HSET", KEYS[1], ARGV[1], remaining .. "|" .. reset .. "|" .. parked)
return 0`
	_, err := s.Client.Do(
		ctx, "EVAL", script, "1", s.Key(key), field, strconv.FormatInt(parkedTo, 10))
	return err
}

func (s *Store) RememberDelivery(ctx context.Context, delivery string, ttl time.Duration) (bool, error) {
	value, err := s.Client.Do(ctx, "SET", s.deliveryKey(delivery), "1", "NX", "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	if err != nil {
		return false, err
	}
	return value != nil && fmt.Sprint(value) == "OK", nil
}

func (s *Store) ForgetDelivery(ctx context.Context, delivery string) error {
	_, err := s.Client.Do(ctx, "DEL", s.deliveryKey(delivery))
	return err
}

type DeliveryReservation = operational.DeliveryReservation

const (
	DeliveryAlreadyCommitted = operational.DeliveryAlreadyCommitted
	DeliveryReserved         = operational.DeliveryReserved
	DeliveryInProgress       = operational.DeliveryInProgress
)

// ReserveDelivery serializes non-queue admission without consuming the
// durable delivery marker before its state changes succeed.
func (s *Store) ReserveDelivery(ctx context.Context, delivery string, ttl time.Duration) (DeliveryReservation, error) {
	script := `
if redis.call("EXISTS", KEYS[1]) == 1 then return 0 end
if redis.call("SET", KEYS[2], "1", "NX", "PX", ARGV[1]) then return 1 end
return 2`
	value, err := s.Client.Do(
		ctx, "EVAL", script, "2", s.deliveryKey(delivery), s.deliveryReservationKey(delivery),
		strconv.FormatInt(ttl.Milliseconds(), 10),
	)
	if err != nil {
		return DeliveryAlreadyCommitted, err
	}
	switch toInt64(value) {
	case 0:
		return DeliveryAlreadyCommitted, nil
	case 1:
		return DeliveryReserved, nil
	case 2:
		return DeliveryInProgress, nil
	default:
		return DeliveryAlreadyCommitted, errors.New("unexpected delivery reservation response")
	}
}

func (s *Store) ReleaseDeliveryReservation(ctx context.Context, delivery string) error {
	_, err := s.Client.Do(ctx, "DEL", s.deliveryReservationKey(delivery))
	return err
}

func (s *Store) deliveryReservationKey(delivery string) string {
	sum := sha256.Sum256([]byte(delivery))
	return s.Key("github-delivery-reservation:" + hex.EncodeToString(sum[:]))
}

func (s *Store) SetOperationalState(ctx context.Context, name string, value []byte) error {
	_, err := s.Client.Do(ctx, "SET", s.Key("state:"+name), string(value))
	return err
}

func (s *Store) OperationalState(ctx context.Context, name string) ([]byte, error) {
	value, err := s.Client.Do(ctx, "GET", s.Key("state:"+name))
	if err != nil || value == nil {
		return nil, err
	}
	return []byte(fmt.Sprint(value)), nil
}

// IncrementIngestionCounter records one bounded, low-cardinality ingestion
// event. Counter names are restricted to the known health contract.
func (s *Store) IncrementIngestionCounter(ctx context.Context, name string) error {
	if _, ok := ingestionCounterNames[name]; !ok {
		return errors.New("unknown ingestion counter")
	}
	if metric, ok := ingestionLoadNames[name]; ok {
		script := `redis.call("HINCRBY", KEYS[1], ARGV[4], 1); redis.call("HINCRBY", KEYS[1], "healthRevision", 1); ` + loadStep
		_, err := s.Client.Do(ctx, "EVAL", script, "2", s.Key(ingestionHealthKey),
			s.Key("load:"+metric), "1", "60", "480", name)
		return err
	}
	script := `local count = redis.call("HINCRBY", KEYS[1], ARGV[1], 1); redis.call("HINCRBY", KEYS[1], "healthRevision", 1); return count`
	_, err := s.Client.Do(ctx, "EVAL", script, "1", s.Key(ingestionHealthKey), name)
	return err
}

// RecordIngestionHealthEvent stores only fixed event codes and timestamps, not
// error messages, request data, or credentials.
func (s *Store) RecordIngestionHealthEvent(ctx context.Context, event, code string, at time.Time) error {
	var field string
	switch event {
	case "failure":
		if code != "admission" && code != "collection" && code != "redis" {
			return errors.New("unknown ingestion failure code")
		}
		field = "lastFailure"
	case "success":
		if code != "" {
			return errors.New("success events must not include a code")
		}
		field = "lastSuccess"
	case "webhook":
		if code != "" {
			return errors.New("webhook events must not include a code")
		}
		field = "lastWebhook"
	default:
		return errors.New("unknown ingestion health event")
	}
	timestamp := at.UTC().Format(time.RFC3339Nano)
	script := `redis.call("HINCRBY", KEYS[1], "healthRevision", 1); redis.call("HSET", KEYS[1], ARGV[1], ARGV[2]); return 1`
	if event == "failure" {
		script = `redis.call("HINCRBY", KEYS[1], "healthRevision", 1); redis.call("HSET", KEYS[1], ARGV[1], ARGV[2], ARGV[3], ARGV[4]); return 1`
		_, err := s.Client.Do(ctx, "EVAL", script, "1", s.Key(ingestionHealthKey),
			field+"At", timestamp, field+"Code", code)
		return err
	}
	_, err := s.Client.Do(ctx, "EVAL", script, "1", s.Key(ingestionHealthKey), field+"At", timestamp)
	return err
}

// parseIngestionHealthFields classifies one flat HGETALL field/value sequence
// into bounded ingestion counters and fixed health events, applying the same
// precedence IngestionHealth previously checked inline: a recognized counter
// name, then a recognized event field, otherwise the field is ignored. It is
// a pure function extracted from IngestionHealth so every branch — a valid
// counter, a negative or non-numeric counter value, a recognized event, and
// an unrecognized field name — is testable without a Redis client. An error
// is returned as soon as a recognized counter has an invalid value, mirroring
// IngestionHealth's previous fail-fast behavior.
func parseIngestionHealthFields(fields []string) (counters map[string]int64, events map[string]string, err error) {
	counters = make(map[string]int64, len(ingestionCounterNames))
	events = make(map[string]string, 5)
	for index := 0; index+1 < len(fields); index += 2 {
		name, value := fields[index], fields[index+1]
		if _, ok := ingestionCounterNames[name]; ok {
			count, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || count < 0 {
				return nil, nil, errors.New("invalid ingestion counter")
			}
			counters[name] = count
			continue
		}
		switch name {
		case "lastFailureAt", "lastFailureCode", "lastSuccessAt", "lastWebhookAt", "healthRevision":
			events[name] = value
		}
	}
	return counters, events, nil
}

// IngestionHealth returns the bounded health fields persisted by the
// collection pipeline.
func (s *Store) IngestionHealth(ctx context.Context) (map[string]int64, map[string]string, error) {
	value, err := s.Client.Do(ctx, "HGETALL", s.Key(ingestionHealthKey))
	if err != nil {
		return nil, nil, err
	}
	fields, err := Strings(value)
	if err != nil {
		return nil, nil, err
	}
	counters, events, err := parseIngestionHealthFields(fields)
	if err != nil {
		return nil, nil, err
	}
	storeLog.Printf("ingestion health read counters=%d events=%d", len(counters), len(events))
	return counters, events, nil
}

func repositoryMemoryCacheKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CachedRepositoryMemoryCampaign(ctx context.Context, campaign string) ([]byte, error) {
	return s.cachedValue(ctx, s.Key(
		"repository-memory:campaign:"+repositoryMemoryCacheKey(campaign)))
}

func (s *Store) CacheRepositoryMemoryCampaign(
	ctx context.Context, campaign string, content []byte, ttl time.Duration,
) error {
	return s.cacheValue(ctx, s.Key("repository-memory:campaign:"+repositoryMemoryCacheKey(campaign)),
		content, ttl.Milliseconds())
}

func (s *Store) CachedRepositoryMemoryFile(
	ctx context.Context, campaign, commit, path string,
) ([]byte, error) {
	return s.cachedValue(ctx, s.Key(
		"repository-memory:cached-file:"+repositoryMemoryCacheKey(campaign, commit, path)))
}

func (s *Store) CacheRepositoryMemoryFile(
	ctx context.Context, campaign, commit, path string, content []byte, ttl time.Duration,
) error {
	return s.cacheValue(ctx, s.Key("repository-memory:cached-file:"+repositoryMemoryCacheKey(campaign, commit, path)),
		content, ttl.Milliseconds())
}

func marketplaceCacheKey(registryID, generation string) string {
	return repositoryMemoryCacheKey(registryID, generation)
}

// CachedMarketplaceRegistry returns one registry's cached, already-normalized
// package list for the given data revision, or nil if no entry is cached.
// The cache key isolates both registry identity and revision.
func (s *Store) CachedMarketplaceRegistry(ctx context.Context, registryID, generation string) ([]byte, error) {
	return s.cachedValue(ctx, s.Key("marketplace:registry:"+marketplaceCacheKey(registryID, generation)))
}

// CacheMarketplaceRegistry stores one registry's normalized package list for
// ttl, isolated by registryID and revision. content must already be safe to
// serve to clients: callers must never cache raw secrets or access tokens.
func (s *Store) CacheMarketplaceRegistry(
	ctx context.Context, registryID, generation string, content []byte, ttl time.Duration,
) error {
	if ttl <= 0 {
		return nil
	}
	return s.cacheValue(ctx, s.Key("marketplace:registry:"+marketplaceCacheKey(registryID, generation)),
		content, ttl.Milliseconds())
}

func (s *Store) Key(suffix string) string {
	return s.namespace + ":" + suffix
}
