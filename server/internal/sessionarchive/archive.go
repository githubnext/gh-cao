package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

// Publisher acknowledges only after its durable active-generation pointer is
// published. The same pending manifest is retried verbatim after ambiguous I/O.
type Publisher interface {
	Publish(context.Context, Manifest) error
}

type admission struct {
	Member     Member
	Day, Stage string
	Bytes      int64
	CapturedAt time.Time
	Gap        string
}

type archiveState struct {
	Version               int
	Fingerprint           string
	Manifest              Manifest
	Active                string
	Pending               *Manifest
	Records               map[string]admission
	Fences                map[string]Fence
	WithdrawnRepositories map[string]bool
}

type Archive struct {
	mu         sync.Mutex
	config     Config
	root       *os.Root
	lock       *os.File
	converter  *Converter
	publisher  Publisher
	state      archiveState
	readers    map[*Snapshot]bool
	closed     bool
	uncertain  bool
	now        func() time.Time
	writeState func([]byte) error
}

func Open(c Config) (*Archive, error) { return OpenWithPublisher(c, nil) }

func OpenWithPublisher(c Config, publisher Publisher) (_ *Archive, err error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, errors.New("session archive is not enabled")
	}
	info, err := os.Lstat(c.Directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("session archive requires an existing private durable POSIX directory")
	}
	c.Directory, err = filepath.Abs(c.Directory)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return nil, err
	}
	a := &Archive{config: c, root: root, publisher: publisher, readers: map[*Snapshot]bool{}, now: time.Now}
	a.writeState = func(content []byte) error { return durableWrite(root, "state.json", content, false) }
	defer func() {
		if err != nil {
			_ = a.Close()
		}
	}()
	a.lock, err = root.OpenFile("owner.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = ownerLock(a.lock); err != nil {
		return nil, err
	}
	a.converter, err = NewConverter(ConverterConfig{DuckDBPath: c.DuckDBPath, Timeout: c.QueryTimeout})
	if err != nil {
		return nil, err
	}
	identity, _ := json.Marshal(struct {
		Namespace    string
		Repositories []string
		Policy       adxtrace.Policy
		Remote       bool
	}{c.Namespace, c.Repositories, c.Policy, publisher != nil})
	a.state = archiveState{Version: ContractVersion, Fingerprint: hash(identity), Records: map[string]admission{},
		Fences: map[string]Fence{}, WithdrawnRepositories: map[string]bool{},
		Manifest: Manifest{Version: ContractVersion}}
	content, err := privateRead(root, "state.json", 16<<20)
	switch {
	case err == nil:
		fingerprint := a.state.Fingerprint
		if err := decode(content, &a.state); err != nil || a.state.Version != ContractVersion ||
			a.state.Fingerprint != fingerprint || a.state.Records == nil || a.state.Fences == nil || a.state.WithdrawnRepositories == nil {
			return nil, ErrIntegrity
		}
		if err := a.validateState(); err != nil {
			return nil, err
		}
		if a.state.Active != "" {
			manifest, err := privateRead(root, a.state.Active, 16<<20)
			if err != nil {
				return nil, err
			}
			expected, _ := json.Marshal(a.state.Manifest)
			if string(manifest) != string(expected) || !strings.HasSuffix(a.state.Active, hash(manifest)+".json") {
				return nil, ErrIntegrity
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	default:
		files, err := diskFiles(root, 4*c.MaxEntries+10)
		if err != nil {
			return nil, err
		}
		for path := range files {
			if path != "owner.lock" {
				return nil, errors.New("archive state missing from a nonempty archive; refusing to discard replay fences")
			}
		}
	}
	if err := a.recover(context.Background()); err != nil {
		return nil, err
	}
	if err := a.save(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Archive) validateState() error {
	if len(a.state.Records) > a.config.MaxEntries || len(a.state.Fences)+len(a.state.WithdrawnRepositories) > a.config.MaxEntries {
		return ErrCapacity
	}
	versions := 0
	for key, fence := range a.state.Fences {
		if !validIdentity(fence.Namespace, fence.Repository, fence.RunID, fence.Attempt) ||
			fence.Namespace != a.config.Namespace || key != memberKey(Member{Namespace: fence.Namespace, Repository: fence.Repository, RunID: fence.RunID, Attempt: fence.Attempt}) {
			return ErrIntegrity
		}
		versions += len(fence.Versions)
	}
	if versions > a.config.MaxEntries {
		return ErrCapacity
	}
	for key, r := range a.state.Records {
		if key != memberKey(r.Member) || !validIdentity(r.Member.Namespace, r.Member.Repository, r.Member.RunID, r.Member.Attempt) ||
			(r.Stage != "" && !archivePath(r.Stage)) {
			return ErrIntegrity
		}
	}
	for _, manifest := range []*Manifest{&a.state.Manifest, a.state.Pending} {
		if manifest == nil {
			continue
		}
		if manifest.Version != ContractVersion || len(manifest.Members) > a.config.MaxEntries || len(manifest.Files) > a.config.MaxEntries {
			return ErrIntegrity
		}
		keys := map[string]bool{}
		for _, m := range manifest.Members {
			if !validIdentity(m.Namespace, m.Repository, m.RunID, m.Attempt) || m.Namespace != a.config.Namespace || keys[memberKey(m)] {
				return ErrIntegrity
			}
			keys[memberKey(m)] = true
			found := 0
			for _, f := range manifest.Files {
				if slices.Contains(f.Members, m) {
					found++
				}
			}
			if found != 1 {
				return ErrIntegrity
			}
		}
		for _, f := range manifest.Files {
			if !archivePath(f.Path) || !strings.HasSuffix(f.Path, f.Digest+".parquet") || f.Bytes < 1 || f.Bytes > 128<<20 {
				return ErrIntegrity
			}
		}
	}
	return nil
}

func (a *Archive) save() error {
	if err := a.validateState(); err != nil {
		return err
	}
	content, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	if len(content) > 16<<20 {
		return ErrCapacity
	}
	err = a.writeState(content)
	if err == nil {
		a.uncertain = false
	} else if errors.Is(err, ErrUncertain) {
		a.uncertain = true
	}
	return err
}

func (a *Archive) reconcile() error {
	if a.uncertain {
		return a.save()
	}
	return nil
}

func (a *Archive) Capture(ctx context.Context, repository, logs, shardPrefix string) error {
	return adxtrace.CaptureEvidence(ctx, a.config.traceConfig(), repository, logs, shardPrefix, a.now, func(e adxtrace.Evidence, reason string) error {
		if reason != "" {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.closed {
				return ErrClosed
			}
			m := Member{Namespace: a.config.Namespace, Repository: e.Repository, RunID: e.RunID, Attempt: e.Attempt, ID: e.ID}
			key := memberKey(m)
			if r, ok := a.state.Records[key]; !ok || r.Stage == "" {
				previous := r
				if !ok {
					r = admission{Member: m, Day: e.Day, CapturedAt: e.CapturedAt}
				}
				r.Gap = reason
				a.state.Records[key] = r
				if err := a.save(); err != nil {
					if errors.Is(err, ErrUncertain) {
						return err
					}
					if ok {
						a.state.Records[key] = previous
					} else {
						delete(a.state.Records, key)
					}
					return err
				}
			}
			return fmt.Errorf("session archive acquisition gap: %s", reason)
		}
		p, err := FromEvidence(a.config.Namespace, e)
		if err != nil {
			return err
		}
		return a.Admit(ctx, p)
	})
}

// Admit persists a whole approved projection before the acquisition workspace
// can be cleaned. It never publishes a truncated or metadata-only trace.
func (a *Archive) Admit(ctx context.Context, p Projection) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.reconcile(); err != nil {
		return err
	}
	if a.state.Pending != nil {
		return ErrBusy
	}
	if err := validateProjection(p); err != nil {
		return err
	}
	if p.Namespace != a.config.Namespace || !slices.Contains(a.config.Repositories, p.Repository) {
		return ErrIntegrity
	}
	now := a.now().UTC()
	if p.CapturedAt.Before(now.Add(-a.config.ReplayHorizon)) || p.CapturedAt.After(now.Add(time.Minute)) {
		return ErrReplay
	}
	if p.Day != "" {
		day, err := time.Parse(time.DateOnly, p.Day)
		if err != nil || day.Before(now.Add(-a.config.ReplayHorizon).Truncate(24*time.Hour)) {
			return ErrReplay
		}
	}
	m, key := p.Member(), memberKey(p.Member())
	fence := a.state.Fences[key]
	if fence.Withdrawn || a.state.WithdrawnRepositories[p.Repository] {
		return ErrWithdrawn
	}
	if old, ok := a.state.Records[key]; ok && old.Member.Version == p.Version {
		if old.Gap != "" {
			old.Gap = ""
			a.state.Records[key] = old
			return a.save()
		}
		return a.save()
	}
	if slices.Contains(fence.Versions, p.Version) {
		return ErrReplay
	}
	content, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if int64(len(content)) > 4*a.config.MaxTraceBytes || a.stagingBytes()+int64(len(content)) > a.config.MaxStagingBytes {
		return ErrCapacity
	}
	previous, existed := a.state.Records[key]
	fenceBefore, hadFence := a.state.Fences[key]
	stage := "staging/" + p.ID + "-" + p.Version + ".json"
	if err := durableWrite(a.root, stage, content, true); err != nil {
		return err
	}
	fence.Namespace, fence.Repository, fence.RunID, fence.Attempt = p.Namespace, p.Repository, p.RunID, p.Attempt
	fence.Versions = append(slices.Clone(fence.Versions), p.Version)
	a.state.Fences[key] = fence
	a.state.Records[key] = admission{Member: m, Stage: stage, Bytes: int64(len(content)), Day: p.Day, CapturedAt: p.CapturedAt}
	if err := a.save(); err != nil {
		if errors.Is(err, ErrUncertain) {
			return err
		}
		if existed {
			a.state.Records[key] = previous
		} else {
			delete(a.state.Records, key)
		}
		if hadFence {
			a.state.Fences[key] = fenceBefore
		} else {
			delete(a.state.Fences, key)
		}
		_ = a.root.Remove(stage)
		return err
	}
	if previous.Stage != "" && previous.Stage != stage {
		return a.root.Remove(previous.Stage)
	}
	return nil
}

func (a *Archive) PrepareCache(ctx context.Context, repository, sourcePrefix, targetPrefix string) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	if err := a.reconcile(); err != nil {
		a.mu.Unlock()
		return err
	}
	known := map[string]bool{}
	for _, r := range a.state.Records {
		if r.Member.Repository == repository && r.Gap == "" {
			known[r.Member.RunID+":"+strconv.FormatInt(r.Member.Attempt, 10)] = true
		}
	}
	a.mu.Unlock()
	return adxtrace.PrepareCaptureCache(ctx, slices.Contains(a.config.Repositories, repository), sourcePrefix, targetPrefix, known)
}

func (a *Archive) stagingBytes() int64 {
	var n int64
	for _, r := range a.state.Records {
		if r.Stage != "" {
			n += r.Bytes
		}
	}
	return n
}

func (a *Archive) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	status := Status{Enabled: !a.closed, Generation: a.state.Manifest.Generation,
		ActiveTraces: len(a.state.Manifest.Members), StagingBytes: a.stagingBytes()}
	for _, r := range a.state.Records {
		if r.Stage != "" {
			status.StagedTraces++
		}
		if r.Gap != "" {
			status.Gaps++
		}
	}
	for _, f := range a.state.Manifest.Files {
		status.ArchiveBytes += f.Bytes
	}
	return status
}

func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	for reader := range a.readers {
		reader.cancel()
	}
	var err error
	if a.root != nil {
		err = a.root.Close()
	}
	if a.lock != nil {
		err = errors.Join(err, a.lock.Close())
	}
	return err
}
