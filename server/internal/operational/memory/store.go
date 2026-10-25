// Package memory provides bounded, volatile operational state for one owning
// process. It does not emulate Redis or persist data across Store instances.
package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// ErrInvalid identifies invalid configuration or service input.
var ErrInvalid = errors.New("invalid memory operational input")

// Config bounds every variable-cardinality state family. Zero limits select
// defaults (nested defaults are clamped to their enclosing budget); negative
// limits are invalid. Byte budgets charge payloads, retained
// names, and a conservative 128-byte per-record accounting overhead, not the Go
// allocator's exact heap usage. Cache and protected budgets are independent.
// Clock is called under the store mutex and must not call back into the Store.
type Config struct {
	Clock                func() time.Time
	MaxCacheEntries      int
	MaxCacheBytes        int64
	MaxCacheValueBytes   int64
	MaxProtectedEntries  int
	MaxProtectedBytes    int64
	MaxMetadataEntries   int
	MaxMetadataBytes     int64
	MaxSessions          int
	MaxRevocations       int
	MaxRateLimitSubjects int
	MaxQueuedTasks       int
	MaxTaskBytes         int64
	MaxQueueNames        int
	MaxLeases            int
	MaxLocks             int
	MaxStateEntries      int
	MaxQuotaBuckets      int
	MaxQuotaReservations int
	MaxQuotaUsageSamples int
	MaxRecordBytes       int
	MaxNameBytes         int
}

// DefaultConfig returns the bounds used by New(Config{}).
func DefaultConfig() Config {
	return Config{
		Clock: time.Now, MaxCacheEntries: 1024, MaxCacheBytes: 32 << 20,
		MaxCacheValueBytes: 4 << 20, MaxProtectedEntries: 200000,
		MaxProtectedBytes: 128 << 20, MaxMetadataEntries: 100000,
		MaxMetadataBytes: 32 << 20, MaxSessions: 10000, MaxRevocations: 10000,
		MaxRateLimitSubjects: 10000, MaxQueuedTasks: 10000, MaxTaskBytes: 32 << 20,
		MaxQueueNames: 128, MaxLeases: 20000, MaxLocks: 10000,
		MaxStateEntries: 1024, MaxQuotaBuckets: 1024,
		MaxQuotaReservations: 10000, MaxQuotaUsageSamples: 100000,
		MaxRecordBytes: 1 << 20, MaxNameBytes: 1024,
	}
}

type recordKey struct{ kind, name, field string }

func metadata(k recordKey) bool {
	switch k.kind {
	case "queue", "group", "delayed", "member", "attribute",
		"debounce", "delivery", "delivery-reservation", "identity":
		return true
	}
	return false
}

func charge(parts ...string) int64 {
	n := int64(128)
	for _, p := range parts {
		n += int64(len(p))
	}
	return n
}

type expiringValue struct {
	value   string
	expires time.Time
}

type sessionValue struct {
	expiringValue
	namespace string
}

type revocationKey struct{ namespace, id string }
type revocationValue struct {
	value string
	order uint64
}

type Store struct {
	mu     sync.Mutex
	config Config
	closed bool
	wake   chan struct{}
	seq    uint64

	records                       map[recordKey]int64
	protectedBytes, metadataBytes int64
	metadataEntries               int
	cache                         map[string]cacheEntry
	cacheBytes                    int64
	sessions                      map[string]sessionValue
	revocations                   map[revocationKey]revocationValue
	revocationCursor              map[string]uint64
	buckets                       map[string]tokenBucket
	locks                         map[string]expiringValue
	states                        map[string][]byte
	queues                        map[string]*queue
	delayed                       map[string][]scheduled
	taskCount, leaseCount         int
	taskBytes                     int64
	markers                       map[recordKey]time.Time
	identities                    map[recordKey]struct{}
	members                       map[string]map[string]struct{}
	attributes                    map[string]map[string]string
	quotas                        map[string]*quotaBucket
	quotaReservations             int
	usage                         map[usageKey]operational.GitHubQuotaUsageSample
	counters                      [len(counterNames)]int64
	events                        [4]string
	healthRevision                uint64
	loads                         [3]load
}

var _ operational.Store = (*Store)(nil)

func New(c Config) (*Store, error) {
	d := DefaultConfig()
	defaultCacheValue := c.MaxCacheValueBytes == 0
	defaultMetadataBytes := c.MaxMetadataBytes == 0
	defaultMetadataEntries := c.MaxMetadataEntries == 0
	for _, p := range []struct{ dst, def *int }{
		{&c.MaxCacheEntries, &d.MaxCacheEntries}, {&c.MaxProtectedEntries, &d.MaxProtectedEntries},
		{&c.MaxMetadataEntries, &d.MaxMetadataEntries}, {&c.MaxSessions, &d.MaxSessions},
		{&c.MaxRevocations, &d.MaxRevocations}, {&c.MaxRateLimitSubjects, &d.MaxRateLimitSubjects},
		{&c.MaxQueuedTasks, &d.MaxQueuedTasks}, {&c.MaxQueueNames, &d.MaxQueueNames},
		{&c.MaxLeases, &d.MaxLeases}, {&c.MaxLocks, &d.MaxLocks},
		{&c.MaxStateEntries, &d.MaxStateEntries}, {&c.MaxQuotaBuckets, &d.MaxQuotaBuckets},
		{&c.MaxQuotaReservations, &d.MaxQuotaReservations}, {&c.MaxQuotaUsageSamples, &d.MaxQuotaUsageSamples},
		{&c.MaxRecordBytes, &d.MaxRecordBytes}, {&c.MaxNameBytes, &d.MaxNameBytes},
	} {
		if *p.dst < 0 {
			return nil, invalid("negative configuration limit")
		}
		if *p.dst == 0 {
			*p.dst = *p.def
		}
	}
	for _, p := range []struct{ dst, def *int64 }{
		{&c.MaxCacheBytes, &d.MaxCacheBytes}, {&c.MaxCacheValueBytes, &d.MaxCacheValueBytes},
		{&c.MaxProtectedBytes, &d.MaxProtectedBytes}, {&c.MaxMetadataBytes, &d.MaxMetadataBytes},
		{&c.MaxTaskBytes, &d.MaxTaskBytes},
	} {
		if *p.dst < 0 {
			return nil, invalid("negative configuration byte budget")
		}
		if *p.dst == 0 {
			*p.dst = *p.def
		}
	}
	if defaultCacheValue {
		c.MaxCacheValueBytes = min(c.MaxCacheValueBytes, c.MaxCacheBytes)
	}
	if defaultMetadataBytes {
		c.MaxMetadataBytes = min(c.MaxMetadataBytes, c.MaxProtectedBytes)
	}
	if defaultMetadataEntries {
		c.MaxMetadataEntries = min(c.MaxMetadataEntries, c.MaxProtectedEntries)
	}
	if c.MaxCacheValueBytes > c.MaxCacheBytes || c.MaxMetadataBytes > c.MaxProtectedBytes ||
		c.MaxMetadataEntries > c.MaxProtectedEntries {
		return nil, invalid("component budget exceeds its enclosing budget")
	}
	if c.Clock == nil {
		c.Clock = d.Clock
	}
	return &Store{
		config: c, wake: make(chan struct{}), records: make(map[recordKey]int64),
		cache: make(map[string]cacheEntry), sessions: make(map[string]sessionValue),
		revocations: make(map[revocationKey]revocationValue), revocationCursor: make(map[string]uint64),
		buckets: make(map[string]tokenBucket), locks: make(map[string]expiringValue),
		states: make(map[string][]byte), queues: make(map[string]*queue),
		delayed: make(map[string][]scheduled), markers: make(map[recordKey]time.Time),
		identities: make(map[recordKey]struct{}), members: make(map[string]map[string]struct{}),
		attributes: make(map[string]map[string]string), quotas: make(map[string]*quotaBucket),
		usage: make(map[usageKey]operational.GitHubQuotaUsageSample),
	}, nil
}

func invalid(message string) error { return fmt.Errorf("%s: %w", message, ErrInvalid) }
func capacity() error              { return operational.ErrCapacity }

// enter checks context both before waiting and before touching state.
func (s *Store) enter(ctx context.Context) error {
	if ctx == nil {
		return invalid("nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.closed {
		s.mu.Unlock()
		return operational.ErrUnavailable
	}
	return nil
}

func (s *Store) name(values ...string) error {
	for _, v := range values {
		if strings.TrimSpace(v) == "" || len(v) > s.config.MaxNameBytes || strings.ContainsRune(v, 0) {
			return invalid("empty, oversized, or NUL-containing name")
		}
	}
	return nil
}

func (s *Store) namespace(v string) error {
	if len(v) > s.config.MaxNameBytes || strings.ContainsRune(v, 0) {
		return invalid("invalid opaque namespace")
	}
	return nil
}

func (s *Store) value(v string) error {
	if len(v) > s.config.MaxRecordBytes {
		return invalid("record exceeds payload limit")
	}
	return nil
}

func validTTL(ttl time.Duration) error {
	if ttl < time.Millisecond {
		return invalid("TTL must be at least one millisecond")
	}
	return nil
}

// reserve preflights and commits all accounting changes together; callers
// mutate domain state only after it succeeds. A zero charge deletes a record.
func (s *Store) reserve(changes map[recordKey]int64) error {
	bytes, mb, entries, me := s.protectedBytes, s.metadataBytes, len(s.records), s.metadataEntries
	for k, size := range changes {
		old, exists := s.records[k]
		bytes += size - old
		if !exists && size != 0 {
			entries++
		} else if exists && size == 0 {
			entries--
		}
		if metadata(k) {
			mb += size - old
			if !exists && size != 0 {
				me++
			} else if exists && size == 0 {
				me--
			}
		}
	}
	if bytes > s.config.MaxProtectedBytes || entries > s.config.MaxProtectedEntries ||
		mb > s.config.MaxMetadataBytes || me > s.config.MaxMetadataEntries {
		return capacity()
	}
	for k, size := range changes {
		if size == 0 {
			delete(s.records, k)
		} else {
			owned := recordKey{strings.Clone(k.kind), strings.Clone(k.name), strings.Clone(k.field)}
			s.records[owned] = size
		}
	}
	s.protectedBytes, s.metadataBytes, s.metadataEntries = bytes, mb, me
	return nil
}

func (s *Store) drop(k recordKey) {
	n, ok := s.records[k]
	if !ok {
		return
	}
	delete(s.records, k)
	s.protectedBytes -= n
	if metadata(k) {
		s.metadataBytes -= n
		s.metadataEntries--
	}
}

func (s *Store) Capabilities() operational.Capabilities {
	c := operational.Capability{Scope: operational.ScopeProcess, Persistence: operational.PersistenceVolatile}
	return operational.Capabilities{Cache: c, RequestLimits: c, Sessions: c, Revocations: c,
		GitHubQuota: c, Collection: c, Coordination: c, Diagnostics: c}
}

func (s *Store) OperationalServices() operational.OperationalServices {
	return operational.OperationalServices{
		Backend: s, Cache: s, RequestLimiter: s, Sessions: s,
		SessionInvalidator: s, Revocations: s, Leases: s, State: s,
		Deliveries: s, Queue: s, Admission: s, Collection: s,
		GitHubQuota: s, RateLimits: s, Health: s, IngestionMetrics: s,
	}
}

func (s *Store) Health(ctx context.Context) (operational.Health, error) {
	if err := s.enter(ctx); err != nil {
		return operational.Health{Capabilities: s.Capabilities()}, err
	}
	defer s.mu.Unlock()
	s.prune(s.config.Clock())
	return operational.Health{Ready: true, Capabilities: s.Capabilities(),
		CacheBytes: s.cacheBytes, Entries: int64(len(s.records) + len(s.cache))}, nil
}

func (s *Store) Maintain(ctx context.Context) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.prune(s.config.Clock())
	return nil
}

func (s *Store) prune(now time.Time) {
	s.expireCache(now)
	for k, v := range s.locks {
		if !v.expires.After(now) {
			delete(s.locks, k)
			s.drop(recordKey{"lock", k, ""})
		}
	}
	for k, v := range s.buckets {
		if !v.expires.After(now) {
			delete(s.buckets, k)
			s.drop(recordKey{"bucket", k, ""})
		}
	}
	for k, until := range s.markers {
		if !until.After(now) {
			delete(s.markers, k)
			s.drop(k)
		}
	}
	s.pruneQuotas(now)
	for k := range s.usage {
		if !k.slot.Add(operational.GitHubQuotaUsageInterval + operational.GitHubQuotaUsageRetention).After(now) {
			delete(s.usage, k)
			s.drop(recordKey{"usage", k.bucket, k.slot.Format(time.RFC3339)})
		}
	}
}

func (s *Store) notify() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// Close is idempotent and releases all retained references, including encrypted
// credentials. Go strings cannot promise cryptographic zeroization.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.wake)
	s.records, s.cache, s.sessions, s.revocations, s.revocationCursor = nil, nil, nil, nil, nil
	s.buckets, s.locks, s.states, s.queues, s.delayed = nil, nil, nil, nil, nil
	s.markers, s.identities, s.members, s.attributes, s.quotas, s.usage = nil, nil, nil, nil, nil, nil
	s.counters, s.events, s.loads = [len(counterNames)]int64{}, [4]string{}, [3]load{}
	s.protectedBytes, s.metadataBytes, s.cacheBytes, s.taskBytes = 0, 0, 0, 0
	s.metadataEntries, s.taskCount, s.leaseCount, s.quotaReservations = 0, 0, 0, 0
	s.config.Clock = nil
	return nil
}
