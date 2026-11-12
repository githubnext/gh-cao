package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

func remoteValueHintKey(repository string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(repository)))
	return "collect:value-remote:" + hex.EncodeToString(digest[:])
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
