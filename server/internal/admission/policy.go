package admission

import (
	"context"
	"io"
	"os"
	"slices"
	"time"
)

// Snapshot is atomically published by protected, trusted usage ingestion.
// It must never be built from admission request fields or browser projections.
type Snapshot struct {
	Version      int          `json:"version"`
	Repositories []Enrollment `json:"repositories"`
}

type Enrollment struct {
	Repository   string   `json:"repository"`
	RepositoryID string   `json:"repository_id"`
	OwnerID      string   `json:"repository_owner_id"`
	Enabled      bool     `json:"enabled"`
	Subjects     []string `json:"subjects"`
	Workflows    []Policy `json:"workflows"`
	Usage        Usage    `json:"usage"`
}

type Policy struct {
	WorkflowRef string     `json:"workflow_ref"`
	WorkflowSHA string     `json:"workflow_sha"`
	Events      []string   `json:"events"`
	ActorIDs    []string   `json:"actor_ids"`
	Allowances  Allowances `json:"allowances"`
}

type Usage struct {
	Unit        string    `json:"unit"`
	ObservedAt  time.Time `json:"observed_at"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Used        *int64    `json:"used"`
	Limit       int64     `json:"limit"`
}

type PolicySource interface {
	Evaluate(context.Context, Identity) (Allowances, error)
}

type FilePolicy struct{ Path string }

func (f FilePolicy) load() (Snapshot, error) {
	file, err := os.Open(f.Path)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return Snapshot{}, ErrUnavailable
	}
	var snapshot Snapshot
	if strictJSON(data, &snapshot) != nil {
		admissionLog.Printf("policy snapshot rejected: reason=invalid_json")
		return Snapshot{}, ErrUnavailable
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// validateSnapshot enforces the trusted snapshot invariants: version, bounds,
// unique enrollments, well-formed identifiers, and valid workflow policies.
func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != 1 || len(snapshot.Repositories) > 1000 {
		admissionLog.Printf("policy snapshot rejected: reason=version_or_size repositories=%d", len(snapshot.Repositories))
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for _, entry := range snapshot.Repositories {
		if !repository.MatchString(entry.Repository) || !decimal.MatchString(entry.RepositoryID) ||
			!decimal.MatchString(entry.OwnerID) || seen[entry.RepositoryID] ||
			len(entry.Subjects) == 0 || len(entry.Workflows) > 100 ||
			entry.Usage.Unit != "ai-credits" || entry.Usage.Used == nil ||
			*entry.Usage.Used < 0 || entry.Usage.Limit <= 0 {
			admissionLog.Printf("policy snapshot rejected: reason=invalid_enrollment")
			return ErrUnavailable
		}
		seen[entry.RepositoryID] = true
		workflows := map[string]bool{}
		for _, policy := range entry.Workflows {
			key := policy.WorkflowRef + ":" + policy.WorkflowSHA
			if !clean(policy.WorkflowRef, 1024) || !revision.MatchString(policy.WorkflowSHA) ||
				len(policy.Events) == 0 || len(policy.ActorIDs) == 0 ||
				!policy.Allowances.valid() || workflows[key] {
				admissionLog.Printf("policy snapshot rejected: reason=invalid_workflow")
				return ErrUnavailable
			}
			workflows[key] = true
			for _, actor := range policy.ActorIDs {
				if !decimal.MatchString(actor) {
					admissionLog.Printf("policy snapshot rejected: reason=invalid_actor")
					return ErrUnavailable
				}
			}
		}
	}
	return nil
}

func (f FilePolicy) Validate() error {
	_, err := f.load()
	return err
}

// ValidateCurrent checks local readiness without fabricating an invocation or
// updating accounting. Disabled enrollments are deliberately not made ready.
func (f FilePolicy) ValidateCurrent(ctx context.Context) error {
	snapshot, err := f.load()
	if err != nil {
		return err
	}
	enabled := 0
	for _, entry := range snapshot.Repositories {
		if !entry.Enabled {
			continue
		}
		for _, policy := range entry.Workflows {
			identity := Identity{
				Repository: entry.Repository, RepositoryID: entry.RepositoryID, OwnerID: entry.OwnerID,
				Subject: entry.Subjects[0], WorkflowRef: policy.WorkflowRef, WorkflowSHA: policy.WorkflowSHA,
				Event: policy.Events[0], ActorID: policy.ActorIDs[0],
			}
			if _, err := evaluateSnapshot(ctx, snapshot, identity); err != nil {
				return err
			}
			enabled++
		}
	}
	if enabled == 0 {
		return ErrDenied
	}
	return nil
}

func (f FilePolicy) Evaluate(ctx context.Context, identity Identity) (Allowances, error) {
	if ctx.Err() != nil {
		return Allowances{}, ErrUnavailable
	}
	snapshot, err := f.load()
	if err != nil {
		return Allowances{}, err
	}
	return evaluateSnapshot(ctx, snapshot, identity)
}

func evaluateSnapshot(ctx context.Context, snapshot Snapshot, identity Identity) (Allowances, error) {
	usage, allowances, err := enrolledPolicy(ctx, snapshot, identity)
	if err != nil {
		return Allowances{}, err
	}
	if err := checkUsage(usage, time.Now()); err != nil {
		return Allowances{}, err
	}
	return allowances, nil
}

// checkUsage requires a fresh, coherent usage observation inside a window of at
// most 24 hours (ErrUnavailable) and denies exhausted budgets (ErrDenied).
func checkUsage(usage Usage, now time.Time) error {
	if usage.ObservedAt.IsZero() || usage.ObservedAt.After(now.Add(30*time.Second)) ||
		now.Sub(usage.ObservedAt) > 2*time.Minute ||
		usage.WindowStart.After(now) || !usage.WindowEnd.After(now) ||
		usage.WindowStart.IsZero() || usage.WindowEnd.Sub(usage.WindowStart) > 24*time.Hour ||
		usage.ObservedAt.Before(usage.WindowStart) || !usage.ObservedAt.Before(usage.WindowEnd) {
		admissionLog.Printf("usage observation rejected: reason=stale_or_incoherent")
		return ErrUnavailable
	}
	if *usage.Used >= usage.Limit {
		admissionLog.Printf("usage budget exhausted")
		return ErrDenied
	}
	return nil
}

func (f FilePolicy) AuthorizeConclusion(ctx context.Context, identity Identity) error {
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	snapshot, err := f.load()
	if err != nil {
		return err
	}
	_, _, err = enrolledPolicy(ctx, snapshot, identity)
	return err
}

func enrolledPolicy(ctx context.Context, snapshot Snapshot, identity Identity) (Usage, Allowances, error) {
	if ctx.Err() != nil {
		return Usage{}, Allowances{}, ErrUnavailable
	}
	for _, entry := range snapshot.Repositories {
		if entry.RepositoryID != identity.RepositoryID || entry.OwnerID != identity.OwnerID ||
			entry.Repository != identity.Repository {
			continue
		}
		if !entry.Enabled || !slices.Contains(entry.Subjects, identity.Subject) {
			return Usage{}, Allowances{}, ErrDenied
		}
		for _, policy := range entry.Workflows {
			if policy.WorkflowRef != identity.WorkflowRef || policy.WorkflowSHA != identity.WorkflowSHA ||
				!slices.Contains(policy.Events, identity.Event) || !slices.Contains(policy.ActorIDs, identity.ActorID) {
				continue
			}
			return entry.Usage, policy.Allowances, nil
		}
		return Usage{}, Allowances{}, ErrDenied
	}
	return Usage{}, Allowances{}, ErrDenied
}
