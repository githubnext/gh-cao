package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var volatileRecoveryLog = logger.New("cao:collect:volatile-recovery")

// reconstructScope serializes fresh GitHub enumeration with webhook mutations.
// A partial enumeration never authorizes erasure or out-of-scope acknowledgments.
func (b Backfill) reconstructScope(ctx context.Context) (repositories []enrolledRepository, installations, failures int, err error) {
	volatileRecoveryLog.Printf("scope reconstruction started")
	err = b.Enrollment.withMutationLock(ctx, func(ctx context.Context) error {
		listed, listErr := b.Enumerator.ListInstallations(ctx)
		if listErr != nil {
			return listErr
		}
		installations = len(listed)
		type scope struct {
			installation githubapp.Installation
			names        []string
		}
		scopes := make([]scope, 0, len(listed))
		fresh := make(map[string]int64)
		for _, installation := range listed {
			covered, readErr := b.Enumerator.ListRepositories(ctx, installation.ID)
			if readErr != nil {
				failures++
				return fmt.Errorf("reconstruct installation scope: %w", readErr)
			}
			names, enrolled, skipped := normalizeEnumeratedRepositories(covered)
			if skipped != 0 {
				failures++
				return errors.New("fresh enrollment contains invalid repository identities")
			}
			for _, name := range names {
				if previous, exists := fresh[name]; exists && previous != installation.ID {
					return errors.New("fresh enrollment contains conflicting repository ownership")
				}
				fresh[name] = installation.ID
				if b.ScopeLimit > 0 && len(fresh) > b.ScopeLimit {
					return operational.ErrCapacity
				}
			}
			for i := range enrolled {
				enrolled[i].installationID = installation.ID
			}
			repositories = append(repositories, enrolled...)
			scopes = append(scopes, scope{installation, names})
		}
		retained, readErr := b.Lake.RetainedRepositories(ctx, b.ScopeLimit)
		if readErr != nil {
			return readErr
		}
		previous := make(map[string]struct{}, len(retained))
		for _, name := range retained {
			previous[name] = struct{}{}
		}
		cursor := "0"
		for {
			names, next, scanErr := b.Enrollment.ScanRepositories(ctx, cursor, 500)
			if scanErr != nil {
				return scanErr
			}
			for _, name := range names {
				previous[name] = struct{}{}
				if b.ScopeLimit > 0 && len(previous) > b.ScopeLimit {
					return operational.ErrCapacity
				}
			}
			if next == "0" || next == "" {
				break
			}
			cursor = next
		}
		for _, s := range scopes {
			if err := b.Enrollment.addRepositories(ctx, s.installation.ID, s.names); err != nil {
				return err
			}
		}
		withdrawn := withdrawnRepositories(fresh, previous)
		volatileRecoveryLog.Printf("scope reconstruction pruning withdrawn=%d", len(withdrawn))
		for _, name := range withdrawn {
			token, err := operationToken()
			if err != nil {
				return err
			}
			if err := b.Queue.LockRepository(ctx, name, token, b.Projector.lockTTL()); err != nil {
				return err
			}
			owner, err := b.Enrollment.InstallationFor(ctx, name)
			if err == nil {
				_, err = b.Enrollment.removeRepositories(ctx, owner, []string{name})
			}
			if err == nil {
				err = b.Lake.ForgetContext(ctx, name)
			}
			unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			unlockErr := b.Queue.UnlockRepository(unlockCtx, name, token)
			cancel()
			if err != nil || unlockErr != nil {
				return errors.Join(err, unlockErr)
			}
		}
		oldInstallations, err := b.Enrollment.Installations(ctx)
		if err != nil {
			return err
		}
		currentInstallations := make(map[int64]struct{}, len(listed))
		for _, installation := range listed {
			currentInstallations[installation.ID] = struct{}{}
		}
		for _, installation := range oldInstallations {
			if _, present := currentInstallations[installation]; present {
				continue
			}
			if err := b.Enrollment.Metadata.Clear(ctx, installationRepositoriesKey(installation)); err != nil {
				return err
			}
			if err := b.Enrollment.Metadata.RemoveMembers(ctx, installationsKey, fmt.Sprint(installation)); err != nil {
				return err
			}
		}
		return nil
	})
	volatileRecoveryLog.Printf("scope reconstruction completed installations=%d failures=%d err=%t", installations, failures, err != nil)
	return
}

// withdrawnRepositories reports the previously retained repository names that
// no longer appear in the freshly enumerated scope, sorted for deterministic
// pruning order. It is a pure function extracted from reconstructScope's
// inline loop so the withdrawal set itself is independently testable without
// touching the queue, enrollment store, or evidence lake.
func withdrawnRepositories(fresh map[string]int64, previous map[string]struct{}) []string {
	withdrawn := make([]string, 0, len(previous))
	for name := range previous {
		if _, included := fresh[name]; !included {
			withdrawn = append(withdrawn, name)
		}
	}
	sort.Strings(withdrawn)
	return withdrawn
}

// RetainedRepositories discovers identity from authoritative raw shard content,
// never from lossy filename prefixes or from browser state.
func (l Lake) RetainedRepositories(ctx context.Context, limit int) ([]string, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(l.ShardDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.Type().IsRegular() || !strings.Contains(entry.Name(), "-logs-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		name, err := retainedShardRepository(ctx, filepath.Join(l.ShardDirectory(), entry.Name()))
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(entry.Name(), l.ShardPrefix(name)) &&
			!strings.HasPrefix(entry.Name(), legacyShardPrefix(name)) {
			return nil, errors.New("retained shard filename does not match its authoritative identity")
		}
		seen[name] = struct{}{}
		if limit > 0 && len(seen) > limit {
			return nil, operational.ErrCapacity
		}
	}
	mined, err := os.ReadDir(l.miningDirectory())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range mined {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, errors.New("unrecognized retained mining evidence")
		}
		path := filepath.Join(l.miningDirectory(), entry.Name())
		packet, _, err := readMiningPacket(path)
		if err != nil {
			return nil, err
		}
		if path != l.miningPath(packet.Repository) {
			return nil, errors.New("retained mining filename ownership mismatch")
		}
		seen[packet.Repository] = struct{}{}
		if limit > 0 && len(seen) > limit {
			return nil, operational.ErrCapacity
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names, nil
}

func retainedShardRepository(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path is a regular shard discovered in the configured evidence lake.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var identity string
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var record struct {
			Kind       string `json:"kind"`
			Collection string `json:"collection"`
			Record     struct {
				ID           string `json:"id"`
				Owner        string `json:"owner"`
				Name         string `json:"name"`
				RepositoryID string `json:"repositoryId"`
			} `json:"record"`
			Request struct {
				Repository string `json:"repository"`
			} `json:"request"`
			Run struct {
				FullName     string          `json:"repository_full_name"`
				Repository   json.RawMessage `json:"repository"`
				Organization string          `json:"organization"`
			} `json:"run"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return "", errors.New("retained shard contains invalid JSON")
		}
		candidates := []string{record.Request.Repository, record.Run.FullName}
		if record.Kind == "record" {
			if record.Collection == "repositories" {
				candidates = append(candidates, strings.TrimPrefix(record.Record.ID, "repository:"))
				if record.Record.Owner != "" || record.Record.Name != "" {
					candidates = append(candidates, record.Record.Owner+"/"+record.Record.Name)
				}
			}
			if record.Record.RepositoryID != "" {
				candidates = append(candidates, strings.TrimPrefix(record.Record.RepositoryID, "repository:"))
			}
		}
		var repository string
		if len(record.Run.Repository) != 0 && json.Unmarshal(record.Run.Repository, &repository) == nil {
			if strings.Contains(repository, "/") {
				candidates = append(candidates, repository)
			} else if record.Run.Organization != "" && repository != "" {
				candidates = append(candidates, record.Run.Organization+"/"+repository)
			}
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			name, err := NormalizeRepository(candidate)
			if err != nil {
				return "", err
			}
			if identity != "" && identity != name {
				return "", errors.New("retained shard contains conflicting repository identities")
			}
			identity = name
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read retained shard identity: %w", err)
	}
	if identity == "" {
		return "", errors.New("retained shard has no authoritative repository identity")
	}
	return identity, nil
}
