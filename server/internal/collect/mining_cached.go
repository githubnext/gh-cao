package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

// Cached gh-aw run envelopes can already carry the recorded context. Recover
// that evidence locally rather than hydrating historical artifacts from GitHub.
func (l Lake) mineCachedInfo(ctx context.Context, repository string) error {
	paths, err := l.repositoryShards(ctx, l.ShardDirectory(), repository)
	if err != nil {
		return err
	}
	var memberships []mining.Membership
	for _, path := range paths {
		if err := scanCachedMemberships(ctx, path, repository, &memberships, nil); err != nil {
			return err
		}
	}
	if len(memberships) == 0 {
		return nil
	}
	return l.recordMining(ctx, repository, memberships, nil)
}

func scalarText(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return string(number)
	}
	return ""
}

func scanCachedMemberships(ctx context.Context, path, repository string, memberships *[]mining.Membership, identities map[string]bool) error {
	file, err := os.Open(path) // #nosec G304 -- ownership-verified lake shard.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var envelope struct {
			Kind string `json:"kind"`
			Run  struct {
				ID      json.RawMessage `json:"run_id"`
				Attempt json.RawMessage `json:"run_attempt"`
				Context *mining.Context `json:"context"`
				Info    struct {
					Context *mining.Context `json:"context"`
				} `json:"aw_info"`
				Hops []struct {
					RunID            json.RawMessage `json:"run_id"`
					Repository       string          `json:"repository"`
					HopID            string          `json:"hop_id"`
					ParentHopID      string          `json:"parent_hop_id"`
					CallerRunID      string          `json:"caller_run_id"`
					CallerRepository string          `json:"caller_repository"`
					Evidence         []string        `json:"evidence"`
				} `json:"episode_hops"`
			} `json:"run"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return err
		}
		if envelope.Kind != "run" {
			continue
		}
		attempt, attemptErr := strconv.Atoi(scalarText(envelope.Run.Attempt))
		runID := scalarText(envelope.Run.ID)
		if identities != nil && attemptErr == nil && attempt > 0 {
			identities[fmt.Sprintf("%s:%d", runID, attempt)] = true
		}
		contexts := []*mining.Context{envelope.Run.Context, envelope.Run.Info.Context}
		var recorded *mining.Context
		for _, candidate := range contexts {
			if candidate == nil || strings.TrimSpace(candidate.EpisodeID) == "" {
				continue
			}
			if recorded != nil && *recorded != *candidate {
				return errors.New("conflicting cached episode contexts")
			}
			recorded = candidate
		}
		if recorded == nil {
			continue
		}
		if attemptErr != nil {
			return errors.New("cached episode attempt is unavailable")
		}
		recordedContexts := []mining.Context{*recorded}
		for _, hop := range envelope.Run.Hops {
			if !slices.Contains(hop.Evidence, "context.episode_id") || hop.HopID == "" {
				continue
			}
			if scalarText(hop.RunID) != runID ||
				(hop.Repository != "" && !strings.EqualFold(hop.Repository, repository)) {
				return errors.New("cached episode hop identity mismatch")
			}
			recordedContexts = append(recordedContexts, mining.Context{
				EpisodeID: recorded.EpisodeID, HopID: hop.HopID, ParentHopID: hop.ParentHopID,
				RunID: hop.CallerRunID, Repo: hop.CallerRepository,
				RootRepo: recorded.RootRepo, RootRunID: recorded.RootRunID, RootWorkflowID: recorded.RootWorkflowID,
			})
		}
		for _, context := range recordedContexts {
			membership := mining.Membership{Repository: repository, RunID: runID,
				Attempt: attempt, Context: context, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
			if err := membership.Validate(); err != nil {
				return fmt.Errorf("invalid cached episode: %w", err)
			}
			*memberships = append(*memberships, membership)
			if len(*memberships) > 20000 {
				return errors.New("cached episode evidence exceeds bounded packet capacity")
			}
		}
	}
	return scanner.Err()
}
