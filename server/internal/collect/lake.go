package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var lakeLog = logger.New("cao:collect:lake")

// Lake is the durable evidence lake.
//
// It is laid out exactly like an Activity-published snapshot directory, which
// is the decision that lets projection reuse the Actions profile's ingestion
// implementation unchanged and makes cold start a local replay with no GitHub
// requests.
type Lake struct {
	Directory string
}

// Validate reports whether the lake is usable.
func (l Lake) Validate() error {
	if strings.TrimSpace(l.Directory) == "" {
		return errors.New("evidence lake directory is required")
	}
	if !filepath.IsAbs(l.Directory) {
		return errors.New("evidence lake directory must be an absolute path")
	}
	return nil
}

// ShardDirectory holds raw per-repository shards. It doubles as the collection
// CLI's cached-JSONL directory, so each collection reuses known runs.
func (l Lake) ShardDirectory() string {
	return filepath.Join(l.Directory, "gh-aw-logs-shards")
}

// RunsDirectory holds compacted run-information shards.
func (l Lake) RunsDirectory() string {
	return filepath.Join(l.Directory, "gh-aw-logs-runs")
}

// RecordsDirectory holds compacted detailed record shards.
func (l Lake) RecordsDirectory() string {
	return filepath.Join(l.Directory, "gh-aw-logs-records")
}

// InventoryPath is the logical source inventory the projection consumes.
func (l Lake) InventoryPath() string {
	return filepath.Join(l.Directory, "inventory-sources.json")
}

// ControlPlaneInventoryPath is the source-bound control-plane inventory used
// to produce the enriched logical source inventory.
func (l Lake) ControlPlaneInventoryPath() string {
	return filepath.Join(l.Directory, "control-plane-inventory.json")
}

// ManifestPath is the payload-hash manifest that makes the lake verifiable.
func (l Lake) ManifestPath() string {
	return filepath.Join(l.Directory, "payload-hashes.json")
}

func (l Lake) operationalValuesPath() string {
	return filepath.Join(l.ShardDirectory(), "operational-values.jsonl")
}

// ControlSettingsPath is the synthesized settings file that tells the Activity
// CLI which repositories are in scope during inventory discovery.
func (l Lake) ControlSettingsPath() string {
	return filepath.Join(l.Directory, "control-settings.json")
}

// ShardPrefix encodes the complete normalized coordinate without loss. "~"
// cannot occur in repository segments or legacy names, so both separators and
// the terminator are unambiguous without expanding long repository names.
func (l Lake) ShardPrefix(repository string) string {
	return "repo-v2~" + strings.ReplaceAll(strings.ToLower(repository), "/", "~") + "~-logs-"
}

func legacyShardPrefix(repository string) string {
	return strings.ReplaceAll(repository, "/", "-") + "-logs-"
}

// Prepare creates the lake layout and seeds the files the projection requires
// to exist.
func (l Lake) Prepare() error {
	if err := l.Validate(); err != nil {
		return err
	}
	for _, directory := range []string{
		l.Directory, l.ShardDirectory(), l.RunsDirectory(), l.RecordsDirectory(),
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create evidence lake directory: %w", err)
		}
	}
	seeded, err := l.seedInventoryIfMissing()
	if err != nil {
		return err
	}
	lakeLog.Printf("prepared evidence lake seeded_inventory=%t", seeded)
	return nil
}

// seedInventoryIfMissing writes an empty inventory document only when the
// path does not yet exist, so a projection that already populated the
// inventory is never overwritten by a later Prepare call. It reports whether
// it wrote the seed, which Prepare logs to distinguish first-time lake
// creation from a repeat call against an already-populated lake. A stat
// error other than "not exist" is treated the same as an existing file: the
// seed is skipped rather than risk overwriting content Prepare cannot
// actually confirm is absent.
func (l Lake) seedInventoryIfMissing() (bool, error) {
	if _, err := os.Stat(l.InventoryPath()); errors.Is(err, os.ErrNotExist) {
		if err := WriteFileAtomic(l.InventoryPath(), []byte("{}\n")); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Forget deletes every shard collected for repository.
//
// Un-enrollment is a withdrawal of consent, so it must erase retained evidence
// and not merely stop collecting. The caller is responsible for requesting a
// projection afterwards, which is what removes the repository's rows from the
// canonical database.
func (l Lake) Forget(repository string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return l.ForgetContext(ctx, repository)
}

// ForgetContext is Forget with cancellation during ownership preflight.
func (l Lake) ForgetContext(ctx context.Context, repository string) error {
	normalized, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	if err := l.Validate(); err != nil {
		return err
	}
	unlock, err := l.lockEvidence(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var paths []string
	for _, directory := range []string{
		l.ShardDirectory(), l.RunsDirectory(), l.RecordsDirectory(),
	} {
		matches, err := l.repositoryShards(ctx, directory, normalized)
		if err != nil {
			return err
		}
		paths = append(paths, matches...)
	}
	if err := l.filterOperationalValues(func(name string) bool {
		return name != normalized
	}); err != nil {
		return fmt.Errorf("erase shared operational values: %w", err)
	}
	if err := l.forgetInventoryRepository(normalized); err != nil {
		return fmt.Errorf("erase retained repository inventory: %w", err)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove retained evidence: %w", err)
		}
	}
	lakeLog.Printf("forgot repository shards removed=%d", len(paths))
	return nil
}

// repositoryShards preflights every candidate before any mutation. A legacy
// prefix is only a search hint: content, not its lossy name, determines ownership.
func (l Lake) repositoryShards(ctx context.Context, directory, repository string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read evidence lake directory: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		current := strings.HasPrefix(entry.Name(), l.ShardPrefix(repository))
		if !current && !strings.HasPrefix(entry.Name(), legacyShardPrefix(repository)) {
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil, errors.New("non-regular or unrecognized repository shard prevents safe evidence mutation")
		}
		path := filepath.Join(directory, entry.Name())
		owner, err := retainedShardRepository(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("verify repository shard ownership: %w", err)
		}
		if current && owner != repository {
			return nil, errors.New("retained shard filename does not match its authoritative identity")
		}
		if owner == repository {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// MigrateRepositoryShards makes verified legacy evidence available to the new
// repository-scoped cache. Hard links publish without replacing existing files;
// a crash between link and unlink is safely resumable.
func (l Lake) MigrateRepositoryShards(ctx context.Context, repository string) error {
	repository, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	if err := l.Validate(); err != nil {
		return err
	}
	paths, err := l.repositoryShards(ctx, l.ShardDirectory(), repository)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.Base(path)
		if strings.HasPrefix(name, l.ShardPrefix(repository)) {
			continue
		}
		destination := filepath.Join(l.ShardDirectory(),
			l.ShardPrefix(repository)+"legacy-"+strings.TrimPrefix(name, legacyShardPrefix(repository)))
		if err := os.Link(path, destination); err != nil {
			sourceInfo, sourceErr := os.Lstat(path)
			destinationInfo, destinationErr := os.Lstat(destination)
			if !errors.Is(err, os.ErrExist) || sourceErr != nil || destinationErr != nil ||
				!os.SameFile(sourceInfo, destinationInfo) {
				return fmt.Errorf("migrate retained shard without overwriting evidence: %w", err)
			}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove migrated legacy shard: %w", err)
		}
	}
	return nil
}

// Populated reports whether the lake holds evidence that can repopulate an
// empty canonical database without contacting GitHub.
func (l Lake) Populated() (bool, error) {
	if _, err := os.Stat(l.ManifestPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	entries, err := os.ReadDir(l.RunsDirectory())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			return true, nil
		}
	}
	return false, nil
}

// WriteFileAtomic writes a file so a reader observes either the previous
// contents or the complete new contents, never a partial write.
func WriteFileAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".partial-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() {
		_ = os.Remove(name)
	}()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}
