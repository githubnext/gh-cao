package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const miningPacketLimit = 16 << 20

type miningPacket struct {
	Repository string              `json:"repository"`
	Episodes   []mining.Membership `json:"episodes"`
	Outcomes   []mining.Outcome    `json:"outcomes"`
}

func (l Lake) miningDirectory() string { return filepath.Join(l.Directory, "server-mining") }

func (l Lake) miningPath(repository string) string {
	return filepath.Join(l.miningDirectory(), l.ShardPrefix(repository)+"evidence.json")
}

func readMiningPacket(path string) (miningPacket, []byte, error) {
	file, err := os.Open(path) // #nosec G304 -- configured lake path, not an upstream path.
	if err != nil {
		return miningPacket{}, nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, miningPacketLimit+1))
	if err != nil {
		return miningPacket{}, nil, err
	}
	if len(content) > miningPacketLimit {
		return miningPacket{}, nil, errors.New("server mining packet exceeds size limit")
	}
	var packet miningPacket
	if err := json.Unmarshal(content, &packet); err != nil {
		return packet, nil, err
	}
	if len(packet.Episodes)+len(packet.Outcomes) > 20000 {
		return packet, nil, errors.New("server mining packet capacity exceeded")
	}
	repository, err := NormalizeRepository(packet.Repository)
	if err != nil || repository != packet.Repository {
		return packet, nil, errors.New("invalid mining packet repository")
	}
	for _, membership := range packet.Episodes {
		if err := membership.Validate(); err != nil || membership.Repository != repository {
			return packet, nil, errors.New("invalid mining packet episode")
		}
	}
	for _, outcome := range packet.Outcomes {
		if err := outcome.Validate(); err != nil || outcome.Repository != repository {
			return packet, nil, errors.New("invalid mining packet outcome")
		}
	}
	return packet, content, nil
}

func (l Lake) recordMining(ctx context.Context, repository string, memberships []mining.Membership, outcome *mining.Outcome) error {
	repository, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	unlock, err := l.lockEvidence(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	packet, _, err := readMiningPacket(l.miningPath(repository))
	if errors.Is(err, os.ErrNotExist) {
		packet = miningPacket{Repository: repository}
	} else if err != nil {
		return err
	}
	if packet.Repository != repository {
		return errors.New("mining packet ownership mismatch")
	}
	episodes := map[string]mining.Membership{}
	outcomes := map[string]mining.Outcome{}
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	for _, m := range append(packet.Episodes, memberships...) {
		if err := m.Validate(); err != nil || m.Repository != repository {
			return errors.New("invalid mined episode")
		}
		if m.ObservedAt.Before(cutoff) {
			continue
		}
		key := fmt.Sprintf("%s:%d:%s", m.RunID, m.Attempt, m.Context.HopID)
		if previous, exists := episodes[key]; exists {
			merged, err := previous.Context.Merge(m.Context)
			if err != nil {
				return err
			}
			m.Context, m.ObservedAt = merged, previous.ObservedAt
		}
		episodes[key] = m
	}
	if outcome != nil {
		packet.Outcomes = append(packet.Outcomes, *outcome)
	}
	for _, o := range packet.Outcomes {
		if err := o.Validate(); err != nil || o.Repository != repository {
			return errors.New("invalid mined outcome")
		}
		if o.ObservedAt.Before(cutoff) {
			continue
		}
		previous, exists := outcomes[o.URL]
		if exists && o.UpdatedAt.Equal(previous.UpdatedAt) {
			if o.State != previous.State || o.StateReason != previous.StateReason || o.Merged != previous.Merged ||
				(o.CloseActor != "" && previous.CloseActor != "" && o.CloseActor != previous.CloseActor) {
				return errors.New("conflicting same-time outcome observations")
			}
			// Replays and API repair must not move the original observation clock.
			if previous.CloseActor != "" || o.CloseActor == "" {
				continue
			}
			o.ObservedAt = previous.ObservedAt
		} else if exists && o.UpdatedAt.Before(previous.UpdatedAt) {
			continue
		}
		outcomes[o.URL] = o
	}
	if len(episodes)+len(outcomes) > 20000 {
		return errors.New("server mining packet capacity exceeded")
	}
	packet.Episodes, packet.Outcomes = nil, nil
	for _, m := range episodes {
		packet.Episodes = append(packet.Episodes, m)
	}
	for _, o := range outcomes {
		packet.Outcomes = append(packet.Outcomes, o)
	}
	sort.Slice(packet.Episodes, func(i, j int) bool {
		a, b := packet.Episodes[i], packet.Episodes[j]
		return fmt.Sprintf("%s:%d:%s", a.RunID, a.Attempt, a.Context.HopID) <
			fmt.Sprintf("%s:%d:%s", b.RunID, b.Attempt, b.Context.HopID)
	})
	sort.Slice(packet.Outcomes, func(i, j int) bool { return packet.Outcomes[i].URL < packet.Outcomes[j].URL })
	content, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	if len(content) > miningPacketLimit {
		return errors.New("server mining packet exceeds size limit")
	}
	return WriteFileAtomic(l.miningPath(repository), content)
}

// mineDownloadedInfo reads only compact, identity-bound artifacts already
// acquired by the server collector. Episode discovery itself spends no API quota.
func (l Lake) mineDownloadedInfo(ctx context.Context, repository, directory string) error {
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		runnerLog.Printf("episode membership unavailable: no downloaded compact artifacts")
		return nil
	} else if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	paths, err := l.repositoryShards(ctx, l.ShardDirectory(), repository)
	if err != nil {
		return err
	}
	identities := map[string]bool{}
	var cached []mining.Membership
	for _, path := range paths {
		if err := scanCachedMemberships(ctx, path, repository, &cached, identities); err != nil {
			return err
		}
	}
	var memberships []mining.Membership
	files := 0
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		files++
		if files > 100000 || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unbounded or symlinked mining artifact tree")
		}
		if entry.IsDir() || entry.Name() != "aw_info.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 128<<10 {
			return errors.New("invalid compact episode artifact")
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		content, err := root.ReadFile(relative)
		if err != nil {
			return err
		}
		var artifact struct {
			Repository string         `json:"repository"`
			RunID      json.Number    `json:"run_id"`
			Attempt    string         `json:"run_attempt"`
			Context    mining.Context `json:"context"`
		}
		if err := json.Unmarshal(content, &artifact); err != nil {
			return fmt.Errorf("decode compact episode artifact: %w", err)
		}
		if artifact.Context.EpisodeID == "" {
			return nil
		}
		attempt, err := strconv.Atoi(artifact.Attempt)
		if err != nil || !strings.EqualFold(artifact.Repository, repository) {
			return errors.New("compact episode artifact identity mismatch")
		}
		m := mining.Membership{Repository: repository, RunID: string(artifact.RunID), Attempt: attempt,
			Context: artifact.Context, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
		if err := m.Validate(); err != nil {
			return err
		}
		if !identities[fmt.Sprintf("%s:%d", m.RunID, m.Attempt)] {
			return errors.New("episode artifact does not match a collected run attempt")
		}
		memberships = append(memberships, m)
		if len(memberships) > 20000 {
			return errors.New("downloaded episode evidence exceeds bounded packet capacity")
		}
		return nil
	})
	if err != nil || len(memberships) == 0 {
		return err
	}
	return l.recordMining(ctx, repository, memberships, nil)
}

// miningSupplement is called under the lake lock. Hashing and application see
// the same evidence; ingestion publishes the supplement in its own transaction.
func (p Projector) miningSupplement(ctx context.Context) (string, func(context.Context, *postgresx.Writer) error, error) {
	entries, err := os.ReadDir(p.Lake.miningDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	hasher := sha256.New()
	var paths []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			return "", nil, errors.New("unrecognized server mining evidence")
		}
		path := filepath.Join(p.Lake.miningDirectory(), entry.Name())
		packet, content, err := readMiningPacket(path)
		if err != nil {
			return "", nil, err
		}
		if path != p.Lake.miningPath(packet.Repository) {
			return "", nil, errors.New("mining evidence filename ownership mismatch")
		}
		if p.Enrollment.Metadata != nil {
			enrolled, err := p.Enrollment.Enrolled(ctx, packet.Repository)
			if err != nil {
				return "", nil, err
			}
			if !enrolled {
				continue
			}
		}
		_, _ = hasher.Write([]byte(entry.Name() + "\x00"))
		_, _ = hasher.Write(content)
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return "", nil, nil
	}
	revision := "server-mining-v1:" + hex.EncodeToString(hasher.Sum(nil))
	apply := func(ctx context.Context, writer *postgresx.Writer) error {
		for _, path := range paths {
			packet, _, err := readMiningPacket(path)
			if err != nil {
				return err
			}
			for _, membership := range packet.Episodes {
				if err := writer.AppendEpisode(ctx, membership); err != nil {
					return err
				}
			}
			for _, outcome := range packet.Outcomes {
				if err := writer.AppendOutcome(ctx, outcome); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return revision, apply, nil
}
