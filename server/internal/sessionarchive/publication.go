package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const retirementGrace = 5 * time.Minute

func (a *Archive) Flush(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	return a.flush(ctx)
}

func (a *Archive) flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.state.Pending == nil {
		next, changed, err := a.prepare(ctx)
		if err != nil || !changed {
			return err
		}
		a.state.Pending = &next
		if err := a.save(); err != nil {
			a.state.Pending = nil
			return err
		}
	}
	next := *a.state.Pending
	content, err := json.Marshal(next)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("generations/%020d-%s.json", next.Generation, hash(content))
	if err := durableWrite(a.root, path, content, true); err != nil {
		return err
	}
	if a.publisher != nil {
		remote := cloneManifest(next)
		for i := range remote.Files {
			remote.Files[i].Path = filepath.Join(a.config.Directory, remote.Files[i].Path)
		}
		if err := a.publisher.Publish(ctx, remote); err != nil {
			return err
		}
	}
	oldState := a.state
	a.state.Manifest, a.state.Active, a.state.Pending = next, path, nil
	records := make(map[string]admission, len(a.state.Records))
	for key, r := range a.state.Records {
		if r.Stage != "" {
			r.Stage, r.Bytes = "", 0
		}
		records[key] = r
	}
	a.state.Records = records
	if err := a.save(); err != nil {
		a.state = oldState
		return err
	}
	// Staging is released only after both destination and local receipt commit.
	for _, r := range oldState.Records {
		if r.Stage != "" {
			if err := a.root.Remove(r.Stage); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if a.publisher != nil {
		for _, f := range next.Files {
			if err := a.root.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func cloneManifest(m Manifest) Manifest {
	content, _ := json.Marshal(m)
	var clone Manifest
	_ = json.Unmarshal(content, &clone)
	return clone
}

func (a *Archive) allowed(m Member) bool {
	return !a.state.WithdrawnRepositories[m.Repository] && !a.state.Fences[memberKey(m)].Withdrawn
}

func (a *Archive) prepare(ctx context.Context) (Manifest, bool, error) {
	now := a.now().UTC()
	next := Manifest{Version: ContractVersion, Generation: a.state.Manifest.Generation + 1, CreatedAt: now}
	members := map[string]Member{}
	for _, m := range a.state.Manifest.Members {
		r, exists := a.state.Records[memberKey(m)]
		if !exists {
			return Manifest{}, false, ErrIntegrity
		}
		if a.allowed(m) && !r.CapturedAt.Before(now.Add(-a.config.Retention)) {
			members[memberKey(m)] = m
		}
	}
	groups := map[string][]Projection{}
	for _, r := range a.state.Records {
		if r.Stage == "" || !a.allowed(r.Member) || r.CapturedAt.Before(now.Add(-a.config.Retention)) {
			continue
		}
		content, err := privateRead(a.root, r.Stage, 4*a.config.MaxTraceBytes)
		if err != nil {
			return Manifest{}, false, err
		}
		var p Projection
		if err := decode(content, &p); err != nil || p.Member() != r.Member || validateProjection(p) != nil {
			return Manifest{}, false, ErrIntegrity
		}
		group := p.Repository + "\x00" + p.Day
		groups[group] = append(groups[group], p)
		members[memberKey(r.Member)] = r.Member
	}
	groupKeys := make([]string, 0, len(groups))
	for group := range groups {
		groupKeys = append(groupKeys, group)
	}
	slices.Sort(groupKeys)
	for _, key := range groupKeys {
		ps := groups[key]
		slices.SortFunc(ps, func(a, b Projection) int { return strings.Compare(a.ID, b.ID) })
		day := ps[0].Day
		if day == "" {
			day = "undated"
		}
		directory := "data/" + hash([]byte(a.config.Namespace)) + "/" + hash([]byte(ps[0].Repository)) + "/" + day
		if err := a.root.MkdirAll(directory, 0o700); err != nil {
			return Manifest{}, false, err
		}
		// No compaction: each publication packs all admitted traces in a partition.
		tmp := directory + "/conversion.parquet"
		if err := a.root.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, false, err
		}
		file, err := a.converter.Write(ctx, filepath.Join(a.config.Directory, tmp), ps)
		if err != nil {
			return Manifest{}, false, err
		}
		dest := directory + "/" + file.Digest + ".parquet"
		if _, err := a.root.Lstat(dest); err == nil {
			if err := a.converter.Verify(ctx, filepath.Join(a.config.Directory, dest), file); err != nil {
				return Manifest{}, false, err
			}
			if err := a.root.Remove(tmp); err != nil {
				return Manifest{}, false, err
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := a.root.Rename(tmp, dest); err != nil {
				return Manifest{}, false, err
			}
		} else {
			return Manifest{}, false, err
		}
		if err := syncParents(a.root, directory); err != nil {
			return Manifest{}, false, err
		}
		file.Path = dest
		next.Files = append(next.Files, file)
	}
	for _, f := range a.state.Manifest.Files {
		if slices.ContainsFunc(f.Members, func(m Member) bool { return members[memberKey(m)] == m }) {
			next.Files = append(next.Files, f)
		}
	}
	for _, m := range members {
		next.Members = append(next.Members, m)
	}
	slices.SortFunc(next.Members, func(a, b Member) int { return strings.Compare(memberKey(a), memberKey(b)) })
	slices.SortFunc(next.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	keys := make([]string, 0, len(a.state.Fences))
	for key := range a.state.Fences {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		next.Fences = append(next.Fences, a.state.Fences[key])
	}
	files, err := diskFiles(a.root, 4*a.config.MaxEntries+10)
	if err != nil {
		return Manifest{}, false, err
	}
	var total int64
	for path, bytes := range files {
		if strings.HasPrefix(path, "data/") {
			total += bytes
		}
	}
	if total > a.config.MaxArchiveBytes {
		return Manifest{}, false, ErrCapacity
	}
	changed := !reflect.DeepEqual(next.Members, a.state.Manifest.Members) ||
		!reflect.DeepEqual(next.Fences, a.state.Manifest.Fences)
	return next, changed, nil
}

func (a *Archive) Withdraw(ctx context.Context, repository, runID string, attempt int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if !validIdentity(a.config.Namespace, repository, runID, attempt) || !slices.Contains(a.config.Repositories, repository) {
		return ErrIntegrity
	}
	// Resolve any ambiguous publication before preparing a different generation.
	if a.state.Pending != nil {
		if err := a.flush(ctx); err != nil {
			return err
		}
	}
	key := memberKey(Member{Namespace: a.config.Namespace, Repository: repository, RunID: runID, Attempt: attempt})
	f := a.state.Fences[key]
	f.Namespace, f.Repository, f.RunID, f.Attempt, f.Withdrawn = a.config.Namespace, repository, runID, attempt, true
	a.state.Fences[key] = f
	a.cancelReaders(repository)
	if err := a.save(); err != nil {
		return err
	}
	return a.flush(ctx)
}

func (a *Archive) WithdrawRepository(ctx context.Context, repository string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if !slices.Contains(a.config.Repositories, repository) {
		return ErrIntegrity
	}
	if a.state.Pending != nil {
		if err := a.flush(ctx); err != nil {
			return err
		}
	}
	a.state.WithdrawnRepositories[repository] = true
	for key, fence := range a.state.Fences {
		if fence.Repository == repository {
			fence.Withdrawn = true
			a.state.Fences[key] = fence
		}
	}
	a.cancelReaders(repository)
	if err := a.save(); err != nil {
		return err
	}
	return a.flush(ctx)
}

func (a *Archive) cancelReaders(repository string) {
	for reader := range a.readers {
		if slices.Contains(reader.scope.Repositories, repository) {
			reader.revoked = true
			reader.cancel()
		}
	}
}

func (a *Archive) Maintain(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if err := a.flush(ctx); err != nil {
		return err
	}
	return a.collectGarbage(false)
}

func (a *Archive) referenced() map[string]bool {
	references := map[string]bool{"owner.lock": true, "state.json": true}
	if a.state.Active != "" {
		references[a.state.Active] = true
	}
	for _, r := range a.state.Records {
		if r.Stage != "" && a.allowed(r.Member) && !r.CapturedAt.Before(a.now().Add(-a.config.Retention)) {
			references[r.Stage] = true
		}
	}
	for _, manifest := range []*Manifest{&a.state.Manifest, a.state.Pending} {
		if manifest == nil {
			continue
		}
		for _, f := range manifest.Files {
			if a.publisher == nil || manifest == a.state.Pending {
				references[f.Path] = true
			}
		}
	}
	for reader := range a.readers {
		if reader.ctx.Err() != nil || a.now().After(reader.expires) {
			reader.cancel()
			delete(a.readers, reader)
			continue
		}
		for _, f := range reader.manifest.Files {
			references[f.Path] = true
		}
	}
	return references
}

func (a *Archive) collectGarbage(recovery bool) error {
	references := a.referenced()
	files, err := diskFiles(a.root, 4*a.config.MaxEntries+10)
	if err != nil {
		return err
	}
	for path := range files {
		if references[path] {
			continue
		}
		info, err := a.root.Lstat(path)
		if err != nil {
			return err
		}
		if recovery || strings.HasSuffix(path, ".tmp") || a.now().Sub(info.ModTime()) >= retirementGrace {
			if err := a.root.Remove(path); err != nil {
				return err
			}
			if err := syncDirectory(a.root, filepath.Dir(path)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Archive) recover(ctx context.Context) error {
	for _, r := range a.state.Records {
		if r.Stage == "" {
			continue
		}
		content, err := privateRead(a.root, r.Stage, 4*a.config.MaxTraceBytes)
		var p Projection
		if err != nil || decode(content, &p) != nil || p.Member() != r.Member || validateProjection(p) != nil {
			return ErrIntegrity
		}
	}
	for _, manifest := range []*Manifest{&a.state.Manifest, a.state.Pending} {
		if manifest == nil || a.publisher != nil && manifest == &a.state.Manifest {
			continue
		}
		for _, f := range manifest.Files {
			// Pending remote generations may refer to older, already uploaded files.
			if a.publisher != nil {
				if _, err := a.root.Lstat(f.Path); errors.Is(err, os.ErrNotExist) {
					if slices.ContainsFunc(a.state.Manifest.Files, func(old File) bool { return old.Digest == f.Digest }) {
						continue
					}
				}
			}
			if err := a.converter.Verify(ctx, filepath.Join(a.config.Directory, f.Path), f); err != nil {
				return err
			}
		}
	}
	return a.collectGarbage(true)
}
