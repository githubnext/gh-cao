package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

type remoteValueHint struct {
	Dirty      uint64 `json:"dirty"`
	Processed  uint64 `json:"processed"`
	AttemptDay string `json:"attemptDay,omitempty"`
	RetryAt    string `json:"retryAt,omitempty"`
}

func remoteValueHintKey(repository string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(repository)))
	return "collect:value-remote:" + hex.EncodeToString(digest[:])
}

func (p NativeValueService) readRemoteValueHint(ctx context.Context, repository string) (remoteValueHint, error) {
	content, err := p.State.OperationalState(ctx, remoteValueHintKey(repository))
	if err != nil || len(content) == 0 {
		return remoteValueHint{}, err
	}
	var hint remoteValueHint
	if err := json.Unmarshal(content, &hint); err != nil {
		return remoteValueHint{}, err
	}
	if hint.Processed > hint.Dirty {
		return remoteValueHint{}, errors.New("remote Value hint has an invalid processed generation")
	}
	if hint.AttemptDay != "" {
		if _, err := time.Parse("2006-01-02", hint.AttemptDay); err != nil {
			return remoteValueHint{}, errors.New("remote Value attempt day is invalid")
		}
	}
	if hint.RetryAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, hint.RetryAt); err != nil {
			return remoteValueHint{}, errors.New("remote Value retry clock is invalid")
		}
	}
	return hint, nil
}

func (p NativeValueService) writeRemoteValueHint(ctx context.Context, repository string, hint remoteValueHint) error {
	content, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	return p.State.SetOperationalState(ctx, remoteValueHintKey(repository), content)
}

// MarkRemoteValueDirty runs under the collection worker's repository lease.
// The webhook records an invalidation, never an accepted Value outcome.
func (p NativeValueService) MarkRemoteValueDirty(ctx context.Context, repository string) error {
	if p.State == nil || p.CatalogRoot == "" || p.PolicyPath == "" {
		return nil
	}
	repositories, err := p.RemoteValueRepositories(ctx)
	if err != nil {
		return err
	}
	registered := false
	for _, candidate := range repositories {
		if strings.EqualFold(candidate, repository) {
			registered = true
			break
		}
	}
	if !registered {
		return nil
	}
	hint, err := p.readRemoteValueHint(ctx, repository)
	if err != nil {
		return err
	}
	if hint.Dirty == ^uint64(0) {
		return errors.New("remote Value hint generation exhausted")
	}
	hint.Dirty++
	return p.writeRemoteValueHint(ctx, repository, hint)
}

func (w Worker) withRemoteValueHint(ctx context.Context, repository string, update func(*remoteValueHint) error) error {
	token, err := operationToken()
	if err != nil {
		return err
	}
	if err := w.Queue.LockRepository(ctx, repository, token, 30*time.Second); err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = w.Queue.UnlockRepository(release, repository, token)
	}()
	hint, err := w.Values.readRemoteValueHint(ctx, repository)
	if err != nil {
		return err
	}
	if err := update(&hint); err != nil {
		return err
	}
	return w.Values.writeRemoteValueHint(ctx, repository, hint)
}

func (w Worker) collectRemoteValue(ctx context.Context, repository string, now time.Time) error {
	if w.Values.Enrollment.Authority != nil {
		return w.Values.collectNativeRemoteValue(ctx, repository, now)
	}
	hint, err := w.Values.readRemoteValueHint(ctx, repository)
	if err != nil {
		return err
	}
	day := now.UTC().Format("2006-01-02")
	if (hint.AttemptDay == day && hint.Dirty == hint.Processed) ||
		(hint.RetryAt != "" && now.Before(remoteValueRetry(hint.RetryAt))) {
		return nil
	}
	token, err := operationToken()
	if err != nil {
		return err
	}
	lease := "collect:value-remote-run:" + strings.TrimPrefix(remoteValueHintKey(repository), "collect:value-remote:")
	acquired, err := w.Values.Leases.TryLock(ctx, lease, token, 12*time.Minute)
	if err != nil || !acquired {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = w.Values.Leases.Unlock(release, lease, token)
	}()
	hint, err = w.Values.readRemoteValueHint(ctx, repository)
	if err != nil || (hint.AttemptDay == day && hint.Dirty == hint.Processed) {
		return err
	}
	startedDirty := hint.Dirty
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	collectErr := w.Values.CollectRemoteValues(runCtx, repository)
	updateErr := w.withRemoteValueHint(ctx, repository, func(current *remoteValueHint) error {
		if collectErr != nil {
			current.RetryAt = now.Add(time.Hour).UTC().Format(time.RFC3339Nano)
		} else {
			current.Processed = max(current.Processed, startedDirty)
			current.AttemptDay = day
			current.RetryAt = ""
		}
		return nil
	})
	return errors.Join(collectErr, updateErr)
}

func remoteValueRetry(value string) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, value)
	return at
}

func (p NativeValueService) remoteValueLoop(ctx context.Context) {
	ticker := time.NewTicker(p.minInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if p.State == nil || p.Leases == nil || p.PolicyPath == "" {
			continue
		}
		targets, err := p.RemoteValueTargets(ctx)
		if err != nil {
			workerLog.Printf("remote Value eligibility unavailable; acquisition deferred")
			continue
		}
		const parallelCollections = 8
		targets = roundRobinRemoteValueTargets(targets)
		installationSlots := map[int64]chan struct{}{}
		for _, target := range targets {
			if installationSlots[target.Installation] == nil {
				installationSlots[target.Installation] = make(chan struct{}, 1)
			}
		}
		pending := make(chan remoteValueTarget)
		var workers sync.WaitGroup
		for range min(len(targets), parallelCollections) {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for target := range pending {
					if ctx.Err() != nil {
						return
					}
					slot := installationSlots[target.Installation]
					select {
					case slot <- struct{}{}:
					case <-ctx.Done():
						return
					}
					err := p.collectNativeRemoteValue(ctx, target.Repository, time.Now().UTC())
					<-slot
					if err != nil &&
						!errors.Is(err, ErrRepositoryBusy) && ctx.Err() == nil {
						workerLog.Printf("remote Value acquisition deferred or failed; accepted evidence retained")
					}
				}
			}()
		}
	dispatch:
		for _, target := range targets {
			select {
			case <-ctx.Done():
				break dispatch
			case pending <- target:
			}
		}
		close(pending)
		workers.Wait()
	}
}

func roundRobinRemoteValueTargets(targets []remoteValueTarget) []remoteValueTarget {
	byInstallation := map[int64][]remoteValueTarget{}
	installations := make([]int64, 0)
	for _, target := range targets {
		if len(byInstallation[target.Installation]) == 0 {
			installations = append(installations, target.Installation)
		}
		byInstallation[target.Installation] = append(byInstallation[target.Installation], target)
	}
	slices.Sort(installations)
	result := make([]remoteValueTarget, 0, len(targets))
	for len(result) < len(targets) {
		for _, installation := range installations {
			current := byInstallation[installation]
			if len(current) == 0 {
				continue
			}
			result = append(result, current[0])
			byInstallation[installation] = current[1:]
		}
	}
	return result
}
