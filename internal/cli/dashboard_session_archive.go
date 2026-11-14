package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dashboardDuckDBVersion must match the server's pinned converter version.
const dashboardDuckDBVersion = "v1.4.3"

const dashboardSessionArchivePolicyLimit = 1 << 20

// dashboardSessionArchiveDefaultPolicy is the working checkout's reviewed
// approval; the archive is never enabled without an explicit policy file.
const dashboardSessionArchiveDefaultPolicy = ".github/cao/session-archive-policy.json"

// resolveDashboardSessionArchiveOptions enables the archive automatically when
// the pinned DuckDB CLI and an approved policy are present. --session-archive,
// --session-archive-policy or --duckdb make a missing prerequisite fatal;
// --no-session-archive disables it.
func resolveDashboardSessionArchiveOptions(options *dashboardOptions, diagnostics io.Writer) error {
	explicit := options.sessionArchive || options.sessionArchivePolicy != "" || options.duckDB != ""
	if options.noSessionArchive {
		if explicit {
			return errors.New("--no-session-archive cannot be combined with --session-archive, --session-archive-policy or --duckdb")
		}
		return nil
	}
	err := resolveDashboardSessionArchivePrerequisites(options)
	if err == nil {
		options.sessionArchive = true
		return nil
	}
	if explicit {
		return err
	}
	options.sessionArchive, options.sessionArchivePolicy, options.duckDB = false, "", ""
	if !errors.Is(err, errDashboardSessionArchiveUnavailable) {
		_, _ = fmt.Fprintf(diagnostics, "Session archive disabled: %v (pass --no-session-archive to silence)\n", err)
	}
	return nil
}

// errDashboardSessionArchiveUnavailable marks a quiet auto-detection miss:
// no DuckDB CLI was found, so the operator has not set the archive up.
var errDashboardSessionArchiveUnavailable = errors.New("DuckDB CLI not found")

func resolveDashboardSessionArchivePrerequisites(options *dashboardOptions) error {
	name := options.duckDB
	if name == "" {
		name = "duckdb"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if options.duckDB == "" {
			err = errDashboardSessionArchiveUnavailable
		}
		return fmt.Errorf("the session archive requires the pinned DuckDB %s CLI; install it from "+
			"https://github.com/duckdb/duckdb/releases/tag/%s (verify the SHA-256 in server/SESSION-ARCHIVE.md) "+
			"and put it on PATH or pass --duckdb: %w", dashboardDuckDBVersion, dashboardDuckDBVersion, err)
	}
	if path, err = filepath.Abs(path); err != nil {
		return err
	}
	if version, err := dashboardDuckDBVersionOf(path); err != nil {
		return fmt.Errorf("cannot read DuckDB version from %s: %w", path, err)
	} else if version != dashboardDuckDBVersion {
		return fmt.Errorf("DuckDB at %s is %s; the session archive requires exactly %s (pass --duckdb)", path, version, dashboardDuckDBVersion)
	}
	policy := options.sessionArchivePolicy
	if policy == "" {
		if _, err := os.Stat(dashboardSessionArchiveDefaultPolicy); err != nil {
			return fmt.Errorf("no approved content policy; pass --session-archive-policy or create %s", dashboardSessionArchiveDefaultPolicy)
		}
		policy = dashboardSessionArchiveDefaultPolicy
	}
	if policy, err = filepath.Abs(policy); err != nil {
		return err
	}
	if _, err := readDashboardSessionArchivePolicy(policy); err != nil {
		return err
	}
	options.duckDB, options.sessionArchivePolicy = path, policy
	return nil
}

func dashboardDuckDBVersionOf(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(string(output)) {
		if dashboardDuckDBVersionPattern.MatchString(field) {
			return field, nil
		}
	}
	return "", errors.New("unrecognized --version output")
}

var dashboardDuckDBVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// dashboardSessionArchive owns the CLI side of the local archive: the private
// directory the server's archive owns, the approved policy copy, and per-run
// capture frames. The server process alone opens and writes the archive.
type dashboardSessionArchive struct {
	root, store, frames, policy, duckDB string
	window                              time.Duration
	mu                                  sync.Mutex
	logs                                map[string]string
	diagnostics                         io.Writer
}

func prepareDashboardSessionArchive(cache string, options dashboardOptions, diagnostics io.Writer) (*dashboardSessionArchive, error) {
	policy, err := readDashboardSessionArchivePolicy(options.sessionArchivePolicy)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(cache, "session-archive")
	archive := &dashboardSessionArchive{
		root: root, store: filepath.Join(root, "store"), frames: filepath.Join(root, "frames"),
		policy: filepath.Join(root, "policy.json"), duckDB: options.duckDB, window: options.window,
		logs: map[string]string{}, diagnostics: diagnostics,
	}
	for _, directory := range []string{root, archive.store, archive.frames} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("session archive requires real private directories under the dashboard cache")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, err
		}
	}
	if err := writeDashboardFileAtomic(archive.policy, policy); err != nil {
		return nil, err
	}
	return archive, nil
}

// readDashboardSessionArchivePolicy checks only that the operator supplied an
// explicit approval; the server validates the complete closed policy.
func readDashboardSessionArchivePolicy(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read --session-archive-policy: %w", err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, dashboardSessionArchivePolicyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read --session-archive-policy: %w", err)
	}
	var policy struct {
		Version         string              `json:"version"`
		ContentApproved bool                `json:"contentApproved"`
		RightsApproved  bool                `json:"rightsApproved"`
		Events          map[string][]string `json:"events"`
	}
	if len(content) > dashboardSessionArchivePolicyLimit || json.Unmarshal(content, &policy) != nil ||
		policy.Version == "" || !policy.ContentApproved || !policy.RightsApproved || len(policy.Events) == 0 {
		return nil, errors.New("--session-archive-policy must be a bounded JSON policy with a version, " +
			"explicit contentApproved and rightsApproved, and a closed events allowlist")
	}
	return content, nil
}

func writeDashboardFileAtomic(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// environment configures only the serving child, which owns the archive.
// Starting the dashboard with --session-archive is the local reader's approval.
func (a *dashboardSessionArchive) environment(namespace string, repositories []string) []string {
	return []string{
		"CAO_SESSION_ARCHIVE_ENABLED=true",
		"CAO_SESSION_ARCHIVE_DURABLE_STORAGE=true",
		"CAO_SESSION_ARCHIVE_DIRECTORY=" + a.store,
		"CAO_SESSION_ARCHIVE_NAMESPACE=" + namespace,
		"CAO_SESSION_ARCHIVE_REPOSITORIES=" + strings.Join(repositories, ","),
		"CAO_SESSION_ARCHIVE_POLICY=" + a.policy,
		"CAO_SESSION_ARCHIVE_DUCKDB=" + a.duckDB,
		"CAO_SESSION_ARCHIVE_READ_APPROVED=true",
	}
}

// recordRuns writes one capture frame per completed run attempt whose cached
// artifact directory exists. Frames carry identity only; the server reads the
// session log from the artifact directory and records gaps for missing logs.
func (a *dashboardSessionArchive) recordRuns(source dashboardRepositoryEvidence, runs []map[string]any) {
	if err := a.writeFrames(source, runs, time.Now()); err != nil {
		_, _ = fmt.Fprintf(a.diagnostics, "Session archive frames for %s were not updated: %v\n", source.Repository, err)
	}
}

func (a *dashboardSessionArchive) writeFrames(source dashboardRepositoryEvidence, runs []map[string]any, now time.Time) error {
	if source.Directory == "" {
		return nil
	}
	logs, err := filepath.Abs(source.Directory)
	if err != nil {
		return err
	}
	parts := strings.Split(source.Repository, "/")
	if len(parts) != 2 {
		return errors.New("invalid repository evidence identity")
	}
	frames := filepath.Join(a.frames, dashboardCoordinate(source.Repository))
	if err := os.MkdirAll(frames, 0o700); err != nil {
		return err
	}
	raw := map[string]map[string]any{}
	for _, run := range source.Runs {
		raw[nativeScalar(run["databaseId"])] = run
	}
	for _, run := range runs {
		if run["repositoryFullName"] != source.Repository {
			continue
		}
		id := nativeScalar(run["githubRunId"])
		attempt := int(nativeNumber(run["attempt"]))
		github := raw[id]
		if !dashboardRunID.MatchString(id) || attempt < 1 || github == nil || nativeText(github["status"]) != "completed" {
			continue
		}
		if info, err := os.Stat(filepath.Join(logs, "run-"+id)); err != nil || !info.IsDir() {
			continue
		}
		content, err := json.Marshal(map[string]any{"schema_version": 5, "kind": "run", "run": map[string]any{
			"run_id": id, "run_attempt": strconv.Itoa(attempt), "repository": source.Repository,
			"organization": parts[0], "status": "completed", "updated_at": nativeText(github["updatedAt"]),
		}})
		if err != nil {
			return err
		}
		if err := writeDashboardFileAtomic(filepath.Join(frames, "run-"+id+"-"+strconv.Itoa(attempt)+".jsonl"), append(content, '\n')); err != nil {
			return err
		}
	}
	// Frames are rewritten while their run remains in the evidence window.
	entries, err := os.ReadDir(frames)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.ModTime().Before(now.Add(-a.window-24*time.Hour)) {
			_ = os.Remove(filepath.Join(frames, entry.Name()))
		}
	}
	a.mu.Lock()
	a.logs[source.Repository] = logs
	a.mu.Unlock()
	return nil
}

// captureRequest asks the serving owner to admit every recorded frame set.
func (a *dashboardSessionArchive) captureRequest() ([]byte, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.logs) == 0 {
		return nil, false
	}
	type source struct {
		Repository string `json:"repository"`
		Logs       string `json:"logs"`
		Frames     string `json:"frames"`
	}
	sources := []source{}
	for _, repository := range slices.Sorted(maps.Keys(a.logs)) {
		sources = append(sources, source{repository, a.logs[repository], filepath.Join(a.frames, dashboardCoordinate(repository))})
	}
	content, err := json.Marshal(map[string]any{"type": "session-archive-capture", "sources": sources})
	if err != nil {
		return nil, false
	}
	return append(content, '\n'), true
}
