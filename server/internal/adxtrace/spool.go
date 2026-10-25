package adxtrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("trace spool is busy")
var ErrCapacity = errors.New("trace spool budget reached")

type record struct {
	ID, Version, Repository, RunID, Day, File, State, Reason, Delivered string
	ArtifactDigest, PolicyDigest                                        string
	Attempt, Revision                                                   int64
	CapturedAt                                                          time.Time
}

type frame struct {
	ID, Repository, Day, State, Delivered, MembershipDigest string
	Cutoff                                                  time.Time
	Revision                                                int64
	Population, SourceGaps, Excluded                        int
	Members                                                 map[string]string
}

type ledger struct {
	Version     int
	Fingerprint string
	Revision    int64
	Records     map[string]*record
	Frames      map[string]*frame
	Withdrawn   map[string]bool
	Datasets    map[string]*dataset
}

type spool struct {
	config      Config
	namespace   string
	fingerprint string
	mu          sync.Mutex
}

func parent(path string) string { return filepath.Dir(path) }
func base(path string) string   { return filepath.Base(path) }

func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("invalid private trace file")
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, errors.New("trace file exceeds budget")
	}
	return content, nil
}

func atomicWrite(root *os.Root, name string, content []byte) (err error) {
	file, err := root.OpenFile(name+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = root.Remove(name + ".tmp")
		}
	}()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = root.Rename(name+".tmp", name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func newSpool(ctx context.Context, config Config, namespace string) (*spool, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled || namespace == "" {
		return nil, errors.New("trace spool requires enabled configuration and destination identity")
	}
	policy, err := json.Marshal(config.Policy)
	if err != nil {
		return nil, err
	}
	if err := decode(policy, &config.Policy); err != nil {
		return nil, err
	}
	// The dedicated directory must already exist so a typo cannot create
	// sensitive data on a different, possibly transient, mount.
	info, err := os.Lstat(config.Directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("trace spool must be an existing private directory")
	}
	config.Directory, err = filepath.Abs(config.Directory)
	if err != nil {
		return nil, err
	}
	identity, _ := json.Marshal(struct {
		Namespace, Policy, Mode, Seed string
		Count                         int
		Repositories                  []string
		MaxTrace                      int64
		Delay                         time.Duration
	}{namespace, policyDigest(config.Policy), config.Mode, config.Seed, config.SampleCount,
		config.Repositories, config.MaxTraceBytes, config.FrameDelay})
	s := &spool{config: config, namespace: namespace, fingerprint: digest(identity)}
	if err := s.locked(ctx, func(root *os.Root, state *ledger) error { return s.save(root, state) }); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *spool) locked(ctx context.Context, action func(*os.Root, *ledger) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.config.Directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	lock, err := root.OpenFile("lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("trace spool lock is not private")
	}
	if err := tryLockTrace(lock); err != nil {
		return err
	}
	state := ledger{Version: ContractVersion, Fingerprint: s.fingerprint,
		Records: map[string]*record{}, Frames: map[string]*frame{}, Withdrawn: map[string]bool{}, Datasets: map[string]*dataset{}}
	content, err := readBounded(root, "ledger.json", 64<<20)
	if err == nil {
		if err := decode(content, &state); err != nil || state.Version != ContractVersion || state.Fingerprint != s.fingerprint ||
			state.Records == nil || state.Frames == nil || state.Withdrawn == nil ||
			state.Datasets == nil || len(state.Records)+len(state.Frames)+len(state.Datasets) > s.config.MaxEntries {
			return errors.New("trace ledger is corrupt, incompatible or outside budget")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Only this process can own the file lock. Temporary/orphan files are
	// uncommitted writes, never an alternative source of authority.
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	referenced := map[string]bool{"ledger.json": true, "lock": true}
	for _, item := range state.Records {
		if item.File != "" {
			referenced[item.File] = true
		}
	}
	for _, item := range state.Datasets {
		if item.File != "" {
			referenced[item.File] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") || (strings.HasSuffix(name, ".trace") || strings.HasSuffix(name, ".dataset")) && !referenced[name] {
			if err := root.Remove(name); err != nil {
				return err
			}
		} else if !referenced[name] {
			return errors.New("trace directory contains an unexpected file")
		}
	}
	return action(root, &state)
}

func (s *spool) save(root *os.Root, state *ledger) error {
	if len(state.Records)+len(state.Frames)+len(state.Datasets) > s.config.MaxEntries {
		return ErrCapacity
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(content) > 64<<20 {
		return ErrCapacity
	}
	var bytes int64
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("invalid spool entry")
		}
		bytes += info.Size()
	}
	// Account for the atomic replacement's peak, including the old ledger.
	if bytes+int64(len(content)) > s.config.MaxSpoolBytes {
		return ErrCapacity
	}
	return atomicWrite(root, "ledger.json", content)
}

func (s *spool) admit(ctx context.Context, e Evidence, reason string, now time.Time) error {
	return s.locked(ctx, func(root *os.Root, state *ledger) error {
		if state.Withdrawn[e.Repository] {
			return errors.New("trace repository is withdrawn")
		}
		old := state.Records[e.ID]
		if old != nil && old.State == "eligible" && reason != "" {
			return nil
		}
		if old != nil && old.Version == e.Version && old.Reason == reason && old.State != "eligible" {
			return nil
		}
		if old != nil && (old.State == "withdrawn" || old.State == "expired") {
			return nil
		}
		if old != nil && old.Version == e.Version && old.State == "eligible" {
			return nil
		}
		if old == nil && len(state.Records)+len(state.Frames)+len(state.Datasets) >= s.config.MaxEntries {
			return ErrCapacity
		}
		r := &record{ID: e.ID, Version: e.Version, Repository: e.Repository, RunID: e.RunID, Day: e.Day,
			Attempt: e.Attempt, ArtifactDigest: e.ArtifactDigest, PolicyDigest: e.PolicyDigest,
			CapturedAt: now.UTC(), State: "eligible", Reason: reason}
		if reason != "" {
			r.State = "source-gap"
			if reason == "size-exclusion" || reason == "content-exclusion" {
				r.State = "excluded"
			}
		}
		frameID := s.frameID(e.Repository, e.Day)
		if old != nil && old.Day != e.Day {
			if previous := state.Frames[s.frameID(old.Repository, old.Day)]; previous != nil {
				previous.State, previous.Delivered = "invalidated", ""
				state.Revision++
				previous.Revision = state.Revision
			}
		}
		if f := state.Frames[frameID]; f != nil {
			f.State, f.Delivered = "invalidated", ""
			state.Revision++
			f.Revision = state.Revision
			if old == nil {
				f.SourceGaps++
				if s.config.Mode == "sample" {
					r.State, r.Reason = "source-gap", "late-arrival"
				}
			}
		}
		state.Revision++
		r.Revision = state.Revision
		var invalidatedFiles []string
		if old != nil && old.Version != r.Version {
			invalidatedFiles = invalidateDatasets(state, r.ID)
		}
		if r.State == "eligible" {
			for table, rows := range e.Tables {
				if err := seal(s.namespace, e.ID, e.Version, e.Attempt, r.Revision, table, rows); err != nil {
					return err
				}
			}
			content, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if int64(len(content)) > 4*s.config.MaxTraceBytes {
				return errors.New("trace projection exceeds bounded expansion")
			}
			var bytes int64
			entries, err := fs.ReadDir(root.FS(), ".")
			if err != nil {
				return err
			}
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				bytes += info.Size()
			}
			if bytes+int64(len(content))+(1<<20) > s.config.MaxSpoolBytes {
				return ErrCapacity
			}
			r.File = digest(content) + ".trace"
			if old == nil || old.File != r.File {
				if err := atomicWrite(root, r.File, content); err != nil {
					return err
				}
			}
		}
		state.Records[r.ID] = r
		if err := s.save(root, state); err != nil {
			return err
		}
		for _, file := range invalidatedFiles {
			if err := root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if old != nil && old.File != "" && old.File != r.File {
			return root.Remove(old.File)
		}
		return nil
	})
}

func (s *spool) frameID(repository, day string) string {
	return digest([]byte(s.namespace + "\x00" + repository + "\x00" + day))
}

func readEvidence(root *os.Root, r *record, maxBytes int64) (Evidence, error) {
	content, err := readBounded(root, r.File, maxBytes*4)
	if err != nil {
		return Evidence{}, err
	}
	if digest(content)+".trace" != r.File {
		return Evidence{}, errors.New("trace spool payload integrity failure")
	}
	var e Evidence
	if err := decode(content, &e); err != nil || e.ID != r.ID || e.Version != r.Version || e.Repository != r.Repository {
		return Evidence{}, errors.New("trace spool payload identity failure")
	}
	return e, nil
}

func (s *spool) withdraw(ctx context.Context, repository string) error {
	if !slices.Contains(s.config.Repositories, repository) {
		return errors.New("withdrawal is outside trace scope")
	}
	return s.locked(ctx, func(root *os.Root, state *ledger) error {
		state.Withdrawn[repository] = true
		var remove []string
		for _, r := range state.Records {
			if r.Repository != repository || r.State == "withdrawn" {
				continue
			}
			r.State, r.Delivered = "withdrawn", ""
			remove = append(remove, invalidateDatasets(state, r.ID)...)
			state.Revision++
			r.Revision = state.Revision
			if r.File != "" {
				remove = append(remove, r.File)
				r.File = ""
			}
		}
		for _, f := range state.Frames {
			if f.Repository == repository {
				f.State, f.Delivered = "invalidated", ""
				state.Revision++
				f.Revision = state.Revision
			}
		}
		if err := s.save(root, state); err != nil {
			return err
		}
		for _, file := range remove {
			if err := root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove withdrawn trace: %w", err)
			}
		}
		return nil
	})
}

func (s *spool) reserve(root *os.Root, additional int64) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var bytes int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("invalid spool file")
		}
		bytes += info.Size()
	}
	if additional < 0 || bytes+additional+(1<<20) > s.config.MaxSpoolBytes {
		return ErrCapacity
	}
	return nil
}
