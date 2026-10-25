package collect

import (
	"context"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

const (
	discoveryLastKey = "collect:repository-discovery-last"
	discoveryLease   = "collect:repository-discovery"
)

type InstallationEnumerator interface {
	ListInstallations(context.Context) ([]githubapp.Installation, error)
}

// RepositoryDiscovery repairs missed scope webhooks without enumerating runs.
// Workers perform quota-governed repository enumeration through ordinary tasks.
type RepositoryDiscovery struct {
	Client     InstallationEnumerator
	Enrollment Enrollment
	Queue      Queue
	State      operational.StateStore
	Leases     operational.LeaseStore
	Interval   time.Duration
}

type DiscoveryResult struct {
	Installations     int `json:"installations"`
	RefreshesQueued   int `json:"refreshesQueued"`
	WithdrawalsQueued int `json:"withdrawalsQueued"`
}

func (d RepositoryDiscovery) interval() time.Duration {
	if d.Interval <= 0 {
		return time.Hour
	}
	return d.Interval
}

// Refresh queues a fresh inventory pass, including removal of installations
// absent from a complete successful App enumeration. It never replays history.
func (d RepositoryDiscovery) Refresh(ctx context.Context) (DiscoveryResult, error) {
	return d.refresh(ctx, true)
}

func (d RepositoryDiscovery) refresh(ctx context.Context, force bool) (result DiscoveryResult, err error) {
	if d.Client == nil || d.State == nil || d.Leases == nil {
		return result, errors.New("repository discovery requires App enumeration and operational state")
	}
	token, err := operationToken()
	if err != nil {
		return result, err
	}
	acquired, err := d.Leases.TryLock(ctx, discoveryLease, token, 5*time.Minute)
	if err != nil {
		return result, err
	}
	if !acquired {
		if force {
			return result, errors.New("repository discovery is already running")
		}
		return result, nil
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if unlockErr := d.Leases.Unlock(release, discoveryLease, token); err == nil {
			err = unlockErr
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if !force {
		last, err := d.State.OperationalState(ctx, discoveryLastKey)
		if err != nil {
			return result, err
		}
		if len(last) > 0 {
			at, err := time.Parse(time.RFC3339Nano, string(last))
			if err != nil {
				return result, errors.New("stored repository discovery timestamp is invalid")
			}
			if time.Since(at) < d.interval() {
				return result, nil
			}
		}
	}
	ctx, finish := startRefreshTelemetry(ctx, "worker", "repository-list")
	defer func() {
		outcome := "completed"
		if err != nil {
			outcome = "failed"
		}
		finish(outcome, err)
	}()
	listed, err := d.Client.ListInstallations(ctx)
	if err != nil {
		return result, err
	}
	if len(listed) > 100_000 {
		return result, errors.New("repository discovery exceeds the installation bound")
	}
	current := make(map[int64]bool, len(listed))
	for _, installation := range listed {
		if installation.ID <= 0 || current[installation.ID] {
			return result, errors.New("app enumeration returned an invalid or duplicate installation")
		}
		current[installation.ID] = true
	}
	previous, err := d.Enrollment.Installations(ctx)
	if err != nil {
		return result, err
	}
	admitter := Admitter{Enrollment: d.Enrollment, Queue: d.Queue}
	result.Installations = len(listed)
	for _, installation := range listed {
		admission, err := admitter.queueScope(ctx, Intent{Kind: IntentEnroll,
			InstallationID: installation.ID, Reason: "repository-list-refresh"}, "", 0)
		if err != nil {
			return result, err
		}
		if admission.Enqueued {
			result.RefreshesQueued++
		}
	}
	for _, installation := range previous {
		if current[installation] {
			continue
		}
		admission, err := admitter.queueScope(ctx, Intent{Kind: IntentRemoveInstallation,
			InstallationID: installation, Reason: "repository-list-refresh"}, "", 0)
		if err != nil {
			return result, err
		}
		if admission.Enqueued {
			result.WithdrawalsQueued++
		}
	}
	if err := d.State.SetOperationalState(ctx, discoveryLastKey, []byte(time.Now().UTC().Format(time.RFC3339Nano))); err != nil {
		return result, err
	}
	eventsLog.Printf("repository list refresh admitted installations=%d refreshes=%d withdrawals=%d",
		result.Installations, result.RefreshesQueued, result.WithdrawalsQueued)
	return result, nil
}

func (d RepositoryDiscovery) Run(ctx context.Context) {
	ticker := time.NewTicker(min(d.interval(), time.Minute))
	defer ticker.Stop()
	for {
		if _, err := d.refresh(ctx, false); err != nil && ctx.Err() == nil {
			eventsLog.Printf("repository list refresh failed")
			if d.Queue.Metrics != nil {
				if metricErr := d.Queue.Metrics.RecordIngestionHealthEvent(ctx, "failure", "repository-discovery", time.Now().UTC()); metricErr != nil {
					eventsLog.Printf("repository list refresh failure metric failed")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
