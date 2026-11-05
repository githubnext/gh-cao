// Package operational defines backend-independent operational state contracts.
// Dashboard entities and queries are deliberately outside this package.
package operational

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var validationLog = logger.New("cao:operational:validation")

type Scope uint8

const (
	ScopeUnsupported Scope = iota
	ScopeProcess
	ScopeDeployment
)

type Persistence uint8

const (
	PersistenceVolatile Persistence = iota
	PersistenceRestart
)

type Capability struct {
	Scope       Scope
	Persistence Persistence
}

type Capabilities struct {
	Cache, RequestLimits, Sessions, Revocations        Capability
	GitHubQuota, Collection, Coordination, Diagnostics Capability
	CLICommands, Audit                                 Capability
}

type Health struct {
	Ready         bool
	CacheDisabled bool
	Capabilities  Capabilities
	CacheBytes    int64
	Entries       int64
}

type MaintenanceError struct {
	UsedBytes, BudgetBytes, Evicted int64
	Cause                           error
}

func (e *MaintenanceError) Error() string { return e.Cause.Error() }
func (e *MaintenanceError) Unwrap() error { return e.Cause }

var (
	ErrCapacity    = errors.New("operational capacity reached")
	ErrUnavailable = errors.New("operational storage unavailable")
	ErrConflict    = errors.New("operational record superseded")
	ErrUnsupported = errors.New("operational capability unsupported")
)

func nilService(value any) bool {
	if value == nil {
		return true
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func CheckStore(store Store) error {
	if nilService(store) {
		return ErrUnavailable
	}
	return nil
}

type Store interface {
	Backend
	OperationalServices() OperationalServices
}

// Backend owns the lifecycle and guarantees of one operational backend.
type Backend interface {
	Capabilities() Capabilities
	Maintain(context.Context) error
	Close() error
}

type HealthProbe interface {
	Health(context.Context) (Health, error)
	Ping(context.Context) error
}

// OperationalServices exposes independent contracts without requiring each
// service to be backed by a different connection or storage instance.
type OperationalServices struct {
	Backend            Backend
	Cache              Cache
	RequestLimiter     RequestLimiter
	Sessions           SessionStore
	SessionInvalidator SessionInvalidator
	Revocations        RevocationQueue
	Leases             LeaseStore
	State              StateStore
	Deliveries         DeliveryDeduplicator
	Queue              TaskQueue
	Admission          DeliveryAdmissionStore
	Collection         CollectionMetadata
	GitHubQuota        GitHubQuotaStore
	RateLimits         RateLimitStateStore
	Health             HealthProbe
	IngestionMetrics   IngestionMetrics
	CLICommands        CLICommandQueues
	Audit              AuditLog
}

type Requirements struct {
	SingleProcess bool
	AllowVolatile bool
	OAuth         bool
	Collection    bool
	CLICommands   bool
	Audit         bool
}

type serviceFeature struct {
	name     string
	cap      Capability
	present  bool
	required bool
}

// featureRejectionStage identifies which of validateFeatures' checks failed
// for one service feature, so a misconfigured operational backend is
// diagnosable without logging the feature's advertised capability or the
// service value itself.
type featureRejectionStage string

const (
	featureRejectionStageNone                featureRejectionStage = "none"
	featureRejectionStageInvalidGuarantees   featureRejectionStage = "invalid-guarantees"
	featureRejectionStageAdvertisedWithout   featureRejectionStage = "advertised-without-service"
	featureRejectionStageUnsupported         featureRejectionStage = "unsupported"
	featureRejectionStageSingleProcess       featureRejectionStage = "single-process-required"
	featureRejectionStageVolatileAcknowledge featureRejectionStage = "volatile-acknowledgement-required"
)

// classifyFeatureRejection applies validateFeatures' precondition order to
// one service feature and reports which stage, if any, rejects it. It is a
// pure function extracted from validateFeatures so each rejection stage is
// independently testable against a constructed serviceFeature and
// Requirements, without building an OperationalServices value.
func classifyFeatureRejection(f serviceFeature, r Requirements) featureRejectionStage {
	switch {
	case f.cap.Scope > ScopeDeployment || f.cap.Persistence > PersistenceRestart ||
		(f.cap.Scope == ScopeUnsupported && f.cap.Persistence != PersistenceVolatile):
		return featureRejectionStageInvalidGuarantees
	case f.cap.Scope != ScopeUnsupported && !f.present:
		return featureRejectionStageAdvertisedWithout
	case f.name == "cli commands" && f.cap.Scope != ScopeUnsupported && f.cap.Persistence != PersistenceRestart:
		return featureRejectionStageInvalidGuarantees
	case !f.required:
		return featureRejectionStageNone
	case f.cap.Scope == ScopeUnsupported || !f.present:
		return featureRejectionStageUnsupported
	case f.cap.Scope == ScopeProcess && !r.SingleProcess:
		return featureRejectionStageSingleProcess
	case f.cap.Persistence == PersistenceVolatile && !r.AllowVolatile &&
		(f.name == "sessions" || f.name == "revocations" || f.name == "collection"):
		return featureRejectionStageVolatileAcknowledge
	default:
		return featureRejectionStageNone
	}
}

func validateFeatures(features []serviceFeature, r Requirements) error {
	for _, f := range features {
		switch classifyFeatureRejection(f, r) {
		case featureRejectionStageInvalidGuarantees:
			validationLog.Printf("operational feature rejected feature=%s stage=%s", f.name, featureRejectionStageInvalidGuarantees)
			return fmt.Errorf("%s has invalid operational guarantees", f.name)
		case featureRejectionStageAdvertisedWithout:
			validationLog.Printf("operational feature rejected feature=%s stage=%s", f.name, featureRejectionStageAdvertisedWithout)
			return fmt.Errorf("%s advertised without a service: %w", f.name, ErrUnsupported)
		case featureRejectionStageUnsupported:
			validationLog.Printf("operational feature rejected feature=%s stage=%s", f.name, featureRejectionStageUnsupported)
			return fmt.Errorf("%s: %w", f.name, ErrUnsupported)
		case featureRejectionStageSingleProcess:
			validationLog.Printf("operational feature rejected feature=%s stage=%s", f.name, featureRejectionStageSingleProcess)
			return fmt.Errorf("%s requires one owning process", f.name)
		case featureRejectionStageVolatileAcknowledge:
			validationLog.Printf("operational feature rejected feature=%s stage=%s", f.name, featureRejectionStageVolatileAcknowledge)
			return fmt.Errorf("%s requires explicit volatile-state acknowledgement", f.name)
		case featureRejectionStageNone:
			continue
		}
	}
	return nil
}

// ValidateOperationalServices checks every advertised service independently,
// including the components of optional collection and OAuth profiles.
func ValidateOperationalServices(c Capabilities, s OperationalServices, r Requirements) error {
	if nilService(s.Backend) {
		validationLog.Printf("operational feature rejected feature=backend stage=%s", featureRejectionStageUnsupported)
		return fmt.Errorf("backend: %w", ErrUnsupported)
	}
	return validateFeatures([]serviceFeature{
		{"cache", c.Cache, !nilService(s.Cache), true},
		{"request limits", c.RequestLimits, !nilService(s.RequestLimiter), true},
		{"sessions", c.Sessions, !nilService(s.Sessions), r.OAuth},
		{"session invalidator", c.Sessions, !nilService(s.SessionInvalidator), r.OAuth},
		{"revocations", c.Revocations, !nilService(s.Revocations), r.OAuth},
		{"leases", c.Coordination, !nilService(s.Leases), true},
		{"state", c.Coordination, !nilService(s.State), true},
		{"deliveries", c.Coordination, !nilService(s.Deliveries), true},
		{"queue", c.Collection, !nilService(s.Queue), r.Collection},
		{"admission", c.Collection, !nilService(s.Admission), r.Collection},
		{"collection", c.Collection, !nilService(s.Collection), r.Collection},
		{"github quota", c.GitHubQuota, !nilService(s.GitHubQuota), true},
		{"rate limits", c.GitHubQuota, !nilService(s.RateLimits), true},
		{"health", c.Diagnostics, !nilService(s.Health), true},
		{"ingestion metrics", c.Diagnostics, !nilService(s.IngestionMetrics), true},
		{"cli commands", c.CLICommands, !nilService(s.CLICommands), r.CLICommands},
		{"audit", c.Audit, !nilService(s.Audit), r.Audit},
	}, r)
}

type QueryCacheStats struct {
	MemoryBytes, Entries, Expired, Evicted int64
}

type MemoryStats struct {
	UsedBytes, BudgetBytes, Evicted int64
}

const (
	QueryCacheTTL             = 5 * time.Minute
	QueryCacheMaxEntries      = 1024
	GitHubQuotaUsageInterval  = 15 * time.Minute
	GitHubQuotaUsageRetention = 24 * time.Hour
)

type Cache interface {
	CachedQueryResult(context.Context, string, int64, int64) ([]byte, QueryCacheStats, error)
	CacheQueryResult(context.Context, string, []byte, int64, int64) (bool, QueryCacheStats, error)
	CachedMarketplaceRegistry(context.Context, string, string) ([]byte, error)
	CacheMarketplaceRegistry(context.Context, string, string, []byte, time.Duration) error
	CachedRepositoryMemoryCampaign(context.Context, string) ([]byte, error)
	CacheRepositoryMemoryCampaign(context.Context, string, []byte, time.Duration) error
	CachedRepositoryMemoryFile(context.Context, string, string, string) ([]byte, error)
	CacheRepositoryMemoryFile(context.Context, string, string, string, []byte, time.Duration) error
}

type RateLimitResult struct {
	Allowed                bool
	Remaining              int64
	RetryAfter, ResetAfter time.Duration
}

type RequestLimiter interface {
	TakeRateLimitToken(context.Context, string, int, time.Duration) (RateLimitResult, error)
	TakeRateLimitTokens(context.Context, string, int, time.Duration, int) (RateLimitResult, error)
}

// Session records are opaque encrypted values.
type SessionStore interface {
	SessionRecord(context.Context, string) (string, error)
	// The final argument is the cleanup namespace. Expiry removes authority,
	// not the encrypted record; PendingRevocation stages expired sessions.
	PutSession(context.Context, string, string, time.Duration, string) error
	CompareSession(context.Context, string, string, string, time.Duration, string) (bool, error)
	DeleteSession(context.Context, string) error
}

// Invalidation atomically removes session authority and stages revocation.
type SessionInvalidator interface {
	InvalidateSession(context.Context, string, string, string) (string, error)
}

type RevocationQueue interface {
	QueueRevocation(context.Context, string, string, string) error
	PendingRevocation(context.Context, string) (Revocation, error)
	CompleteRevocation(context.Context, string, string, string) error
}

type Revocation struct {
	ID, Value string
}

type IssueUpdate struct {
	Repository, ID, Delivery string
	InstallationID           int64
	State, StateReason       string
	ClosedAt, ObservedAt     string
}

type LeaseStore interface {
	TryLock(context.Context, string, string, time.Duration) (bool, error)
	RenewLock(context.Context, string, string, time.Duration) (bool, error)
	Unlock(context.Context, string, string) error
	LockHeld(context.Context, string) (bool, error)
}

type StateStore interface {
	SetOperationalState(context.Context, string, []byte) error
	OperationalState(context.Context, string) ([]byte, error)
}

type DeliveryDeduplicator interface {
	RememberDelivery(context.Context, string, time.Duration) (bool, error)
	ForgetDelivery(context.Context, string) error
	ReserveDelivery(context.Context, string, time.Duration) (DeliveryReservation, error)
	ReleaseDeliveryReservation(context.Context, string) error
}

type IngestionMetrics interface {
	IncrementIngestionCounter(context.Context, string) error
	RecordIngestionHealthEvent(context.Context, string, string, time.Time) error
	IngestionHealth(context.Context) (map[string]int64, map[string]string, error)
	Loads(context.Context, []string, time.Duration) (map[string]float64, error)
}

type DeliveryReservation int

const (
	DeliveryAlreadyCommitted DeliveryReservation = iota
	DeliveryReserved
	DeliveryInProgress
)

type DeliveryAdmission int

const (
	DeliveryDuplicate DeliveryAdmission = iota
	DeliveryCoalesced
	DeliveryEnqueued
)

// TaskFields is the bounded collection envelope, not a Redis field map.
type TaskFields struct {
	Repository string
	Task       string
	Key        string
	Reason     string
	RecordedAt string
}

type TaskMessage struct {
	ID      string
	Fields  TaskFields
	Receipt string
}

type EnqueueRequest struct {
	Queue, Delayed, Debounce string
	DebounceTTL              time.Duration
	Capacity                 int64
	Fields                   TaskFields
}

type DeliveryRequest struct {
	EnqueueRequest
	Delivery    string
	DeliveryTTL time.Duration
}

type QueueRead struct {
	Queue, Group, Consumer string
	Count                  int
	Block                  time.Duration
}

type Replacement struct {
	Source, Group, ID, Destination, Delayed string
	Due                                     time.Time
	Capacity                                int64
	Fields                                  TaskFields
}

type QueueStats struct {
	Length, Backlog, Pending int64
	OldestPendingAge         time.Duration
}

type TaskQueue interface {
	EnsureQueue(context.Context, string, string) error
	EnqueueTask(context.Context, EnqueueRequest) (bool, error)
	EnqueueUniqueTask(context.Context, string, string, int64, TaskFields) (bool, error)
	ReadTasks(context.Context, QueueRead) ([]TaskMessage, error)
	ClaimTasks(context.Context, QueueRead, time.Duration) ([]TaskMessage, error)
	ReplaceTask(context.Context, Replacement) error
	CompleteTask(context.Context, string, string, string) error
	PromoteTasks(context.Context, string, string, time.Time, int) (int64, error)
	DelayedDepth(context.Context, string) (int64, error)
	QueueStats(context.Context, string, string) (QueueStats, error)
}

var ErrTaskLeaseLost = errors.New("task lease was completed or reclaimed")

// FencedTaskQueue issues a fresh opaque receipt on every delivery and reclaim.
// Completion and replacement atomically reject a missing or superseded receipt.
// Backends without this contract must not emulate it with a separate lease read.
type FencedTaskQueue interface {
	TaskQueue
	CompleteTaskFenced(context.Context, string, string, string, string) error
	ReplaceTaskFenced(context.Context, Replacement, string) error
}

type DeliveryAdmissionStore interface {
	AdmitDelivery(context.Context, DeliveryRequest) (DeliveryAdmission, error)
}

// CollectionMetadata owns logical enrollment indexes and checkpoint attributes.
// Names are namespace-local domain identities, never physical storage keys.
type CollectionMetadata interface {
	AttributeReader
	AddMembers(context.Context, string, ...string) (int64, error)
	RemoveMembers(context.Context, string, ...string) error
	HasMember(context.Context, string, string) (bool, error)
	MemberCount(context.Context, string) (int64, error)
	ScanMembers(context.Context, string, string, int) ([]string, string, error)
	WriteAttribute(context.Context, string, string, string) error
	DeleteAttribute(context.Context, string, string) error
	Clear(context.Context, string) error
	TransferOwners(context.Context, string, string, string, int64, []string) (int, error)
}

type AttributeReader interface {
	ReadAttribute(context.Context, string, string) (string, error)
}

type GitHubQuotaState struct {
	Known                            bool
	Limit, Remaining, Reserved       int64
	ResetAt, ObservedAt, ParkedUntil time.Time
	ParkReason                       string
	Now                              time.Time
}

type GitHubQuotaObservation struct {
	Limit, Remaining    int64
	ResetAt, ObservedAt time.Time
}

type GitHubQuotaObserveOutcome int

const (
	GitHubQuotaObservationStale GitHubQuotaObserveOutcome = iota
	GitHubQuotaObservationReplaced
	GitHubQuotaObservationReconciled
)

type GitHubQuotaAdmission int

const (
	GitHubQuotaAdmitted GitHubQuotaAdmission = iota
	GitHubQuotaParked
	GitHubQuotaExhausted
	GitHubQuotaUnknown
	GitHubQuotaDuplicateReservation
)

type GitHubQuotaUsageSample struct {
	Bucket                string
	Slot                  time.Time
	Limit, Used, Reserved int64
}

type GitHubQuotaStore interface {
	GitHubQuotaSnapshot(context.Context, string) (GitHubQuotaState, error)
	ObserveGitHubQuota(context.Context, string, GitHubQuotaObservation, string) (GitHubQuotaObserveOutcome, bool, GitHubQuotaState, error)
	ReserveGitHubQuota(context.Context, string, string, int64, int64, time.Duration) (GitHubQuotaAdmission, time.Time, GitHubQuotaState, error)
	ReleaseGitHubQuota(context.Context, string, string) (bool, GitHubQuotaState, error)
	ParkGitHubQuota(context.Context, string, time.Time, string) (bool, GitHubQuotaState, error)
	UnparkGitHubQuota(context.Context, string) (GitHubQuotaState, error)
	RecordGitHubQuotaUsage(context.Context, string, time.Time, int64, int64, int64) (bool, error)
	GitHubQuotaUsage(context.Context) ([]GitHubQuotaUsageSample, time.Time, error)
}

type RateLimitStateStore interface {
	ReserveRateLimit(context.Context, string, string, int, int, int64) (int, error)
	ObserveRateLimit(context.Context, string, string, int, int64) error
	ParkRateLimit(context.Context, string, string, int64) error
}
