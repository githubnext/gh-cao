package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
)

const miningBackfillKey = "collect:outcome-backfill"

type OutcomeClient interface {
	ReadOutcome(context.Context, int64, mining.Target) (mining.Outcome, githubquota.ResponseQuota, error)
	WorkflowRunQuotaRefresher
}

type miningBackfillState struct {
	After       string    `json:"after"`
	AttemptedAt time.Time `json:"attemptedAt"`
	Requests    int       `json:"requests"`
	Observed    int       `json:"observed"`
	Failures    int       `json:"failures"`
	Status      string    `json:"status"`
}

// CollectOutcomeEvidence repairs a bounded page of retained safe outputs. The
// namespace lease and persisted cadence prevent replicas multiplying the cap.
// All REST calls, including quota initialization, share the ten-request budget.
func (p Projector) CollectOutcomeEvidence(ctx context.Context) (err error) {
	if p.OutcomeClient == nil {
		return nil
	}
	if p.Data == nil || p.Quota == nil || p.State == nil || p.Leases == nil || p.Enrollment.Metadata == nil {
		return errors.New("outcome repair requires canonical data, quota, and current enrollment")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	token, err := operationToken()
	if err != nil {
		return err
	}
	acquired, err := p.Leases.TryLock(ctx, miningBackfillKey, token, 2*time.Minute)
	if err != nil || !acquired {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = p.Leases.Unlock(release, miningBackfillKey, token)
	}()
	var state miningBackfillState
	content, err := p.State.OperationalState(ctx, miningBackfillKey)
	if err != nil {
		return err
	}
	if len(content) > 0 {
		if err := json.Unmarshal(content, &state); err != nil {
			return fmt.Errorf("decode outcome repair checkpoint: %w", err)
		}
		if time.Since(state.AttemptedAt) < p.minInterval() {
			return nil
		}
	}
	state = miningBackfillState{After: state.After, AttemptedAt: time.Now().UTC(), Status: "partial"}
	persist := func(ctx context.Context) error {
		content, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return p.State.SetOperationalState(ctx, miningBackfillKey, content)
	}
	// Reserve the cadence before any request. A crash cannot reset the budget
	// by immediately starting another pass.
	if err := persist(ctx); err != nil {
		return err
	}
	defer func() {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		err = errors.Join(err, persist(saveCtx))
	}()
	targets, err := p.Data.MiningTargets(ctx, state.After, 10)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		state.After, state.Status = "", "selected-output-pass-complete"
		return nil
	}
	for _, raw := range targets {
		if state.Requests >= 10 {
			break
		}
		target, err := mining.ParseTarget(raw)
		if err != nil {
			state.Failures++
			state.After = raw
			continue
		}
		installation, err := p.Enrollment.InstallationFor(ctx, target.Repository)
		if err != nil {
			return err
		}
		if installation <= 0 {
			state.After = raw
			continue
		}
		refresher := &miningQuotaRefresher{client: p.OutcomeClient, requests: &state.Requests}
		reservation, err := reserveCollectionQuota(ctx, p.Quota, p.QuotaApp, installation, refresher)
		if err != nil {
			// A parked or unknown bucket must not advance this target's cursor.
			return fmt.Errorf("reserve outcome repair quota: %w", err)
		}
		if state.Requests >= 10 {
			return p.Quota.Release(ctx, reservation)
		}
		state.Requests++
		outcome, response, readErr := p.OutcomeClient.ReadOutcome(ctx, installation, target)
		if err := reconcileCollectionQuota(ctx, p.Quota, reservation.Bucket, reservation, response); err != nil {
			return err
		}
		if readErr != nil {
			state.Failures++
			state.After = raw
			continue
		}
		// Serialize with withdrawal so a completed erasure cannot be followed
		// by a late API response resurrecting private evidence.
		recorded := false
		if err := p.Enrollment.withMutationLock(ctx, func(ctx context.Context) error {
			current, err := p.Enrollment.InstallationFor(ctx, target.Repository)
			if err != nil || current != installation {
				return err
			}
			if err := p.Lake.recordMining(ctx, target.Repository, nil, &outcome); err != nil {
				return err
			}
			if err := p.RequestProjection(ctx); err != nil {
				return err
			}
			recorded = true
			return nil
		}); err != nil {
			return err
		}
		if recorded {
			state.Observed++
		}
		state.After = raw
	}
	if state.Failures > 0 {
		return errors.New("outcome repair has unavailable object evidence")
	}
	return nil
}

type miningQuotaRefresher struct {
	client   OutcomeClient
	requests *int
}

func (r *miningQuotaRefresher) QuotaRateLimit(ctx context.Context, installation int64) (githubquota.ResponseQuota, error) {
	if *r.requests >= 10 {
		return githubquota.ResponseQuota{}, errors.New("outcome repair API request budget exhausted")
	}
	*r.requests++
	return r.client.QuotaRateLimit(ctx, installation)
}
