package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var runnerLog = logger.New("cao:collect:runner")

// TokenProvider mints a short-lived installation token for one collection.
type TokenProvider interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

type rateLimitProvider interface {
	RateLimit(ctx context.Context, installationID int64) (int, time.Time, error)
}

// Runner acquires one repository's evidence.
//
// It invokes the same `gh aw logs --audit` command and the same
// `activity/cao.mjs` compactor the Actions profile invokes. There is no second
// implementation of the audit mapping or the shard format; that shared
// implementation is what makes the two profiles equivalent.
type Runner struct {
	Lake Lake
	// CatalogRoot contains activity/cao.mjs.
	CatalogRoot string
	// NodeBinary and GitHubBinary are resolved from PATH when empty.
	NodeBinary   string
	GitHubBinary string
	// GitHubAPIURL overrides the REST API base URL for the gh subprocess.
	GitHubAPIURL string
	// WindowDays bounds the retained collection window.
	WindowDays int
	// Since narrows local refreshes without changing the retained evidence window.
	Since time.Time
	// RunLimit bounds runs collected per repository per invocation.
	RunLimit int
	// MaxStorageMB bounds on-disk artifact usage per invocation.
	MaxStorageMB int
	// RequestTimeout is the per-request timeout passed to the CLI, in minutes.
	RequestTimeoutMinutes int
	// Timeout bounds one whole collection.
	Timeout time.Duration
	// MaxOutputBytes caps captured subprocess output.
	MaxOutputBytes int
	Tokens         TokenProvider
	Budget         *githubapp.Budget
}

// runnerValidationStage identifies which prerequisite check failed during
// Runner.Validate. It is useful for diagnosing misconfiguration without
// logging the catalog root path or other configuration values.
type runnerValidationStage string

const (
	runnerValidationStageLake            runnerValidationStage = "lake"
	runnerValidationStageCatalogRoot     runnerValidationStage = "catalog-root"
	runnerValidationStageActivityCLI     runnerValidationStage = "activity-cli"
	runnerValidationStageWorkflowCatalog runnerValidationStage = "workflow-catalog"
	runnerValidationStageTokens          runnerValidationStage = "tokens"
)

// validateActivityCatalog reports whether catalogRoot names a non-blank
// directory containing the activity/cao.mjs compactor the collect subprocess
// invokes. It is split out from Validate so the catalog-root and
// activity-CLI checks can be exercised without constructing a full Runner,
// and stat is injected so tests can exercise both outcomes without relying
// on real filesystem layout beyond a temporary directory.
func validateActivityCatalog(catalogRoot string, stat func(string) (os.FileInfo, error)) error {
	if strings.TrimSpace(catalogRoot) == "" {
		return errors.New("catalog root containing activity/cao.mjs is required")
	}
	script := filepath.Join(catalogRoot, "activity", "cao.mjs")
	if _, err := stat(script); err != nil {
		return fmt.Errorf("activity CLI is unavailable at %s: %w", script, err)
	}
	return nil
}

func validateWorkflowCatalog(catalogRoot string) error {
	directory := filepath.Join(catalogRoot, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read gh-aw workflow catalog: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".lock.yml") {
			return nil
		}
	}
	return errors.New("gh-aw workflow catalog must contain at least one .lock.yml file")
}

// Validate reports whether the runner can collect at all. On failure it logs
// only which prerequisite stage failed, so misconfiguration can be diagnosed
// without exposing the catalog root or other configuration values.
func (r Runner) Validate() error {
	if err := r.Lake.Validate(); err != nil {
		runnerLog.Printf("runner validation failed stage=%s", runnerValidationStageLake)
		return err
	}
	if err := validateActivityCatalog(r.CatalogRoot, os.Stat); err != nil {
		stage := runnerValidationStageCatalogRoot
		if strings.TrimSpace(r.CatalogRoot) != "" {
			stage = runnerValidationStageActivityCLI
		}
		runnerLog.Printf("runner validation failed stage=%s", stage)
		return err
	}
	if err := validateWorkflowCatalog(r.CatalogRoot); err != nil {
		runnerLog.Printf("runner validation failed stage=%s", runnerValidationStageWorkflowCatalog)
		return err
	}
	if r.Tokens == nil {
		runnerLog.Printf("runner validation failed stage=%s", runnerValidationStageTokens)
		return errors.New("an installation token provider is required")
	}
	return nil
}

func (r Runner) node() string {
	if strings.TrimSpace(r.NodeBinary) != "" {
		return r.NodeBinary
	}
	return "node"
}

func (r Runner) gh() string {
	if strings.TrimSpace(r.GitHubBinary) != "" {
		return r.GitHubBinary
	}
	return "gh"
}

func (r Runner) windowDays() int {
	if r.WindowDays <= 0 {
		return 30
	}
	return r.WindowDays
}

func (r Runner) runLimit() int {
	if r.RunLimit <= 0 {
		return 10000
	}
	return r.RunLimit
}

func (r Runner) maxStorage() int {
	if r.MaxStorageMB <= 0 {
		return 1200
	}
	return r.MaxStorageMB
}

func (r Runner) requestTimeout() int {
	if r.RequestTimeoutMinutes <= 0 {
		return 15
	}
	return r.RequestTimeoutMinutes
}

func (r Runner) timeout() time.Duration {
	if r.Timeout <= 0 {
		return 20 * time.Minute
	}
	return r.Timeout
}

// Collect acquires and compacts evidence for exactly one repository.
//
// It reserves installation budget first and passes the derived reserve to the
// collection subprocess, so the subprocess fails closed rather than exhausting
// the installation.
func (r Runner) Collect(ctx context.Context, task Task) error {
	repository, err := NormalizeRepository(task.Repository)
	if err != nil {
		return err
	}
	if err := r.Lake.Prepare(); err != nil {
		return err
	}
	if err := r.Lake.MigrateRepositoryShards(ctx, repository); err != nil {
		return err
	}
	reserve := 2000
	if r.Budget != nil {
		reserved, err := r.Budget.Reserve(ctx, task.InstallationID)
		if errors.Is(err, githubapp.ErrBudgetUnknown) {
			provider, ok := r.Tokens.(rateLimitProvider)
			if !ok {
				return errors.New("rate-limit budget is unknown and cannot be refreshed")
			}
			remaining, reset, refreshErr := provider.RateLimit(ctx, task.InstallationID)
			if refreshErr != nil {
				return refreshErr
			}
			if refreshErr = r.Budget.Observe(ctx, task.InstallationID, remaining, reset); refreshErr != nil {
				return refreshErr
			}
			reserved, err = r.Budget.Reserve(ctx, task.InstallationID)
		}
		if err != nil {
			return err
		}
		reserve = reserved
	}
	token, err := r.Tokens.InstallationToken(ctx, task.InstallationID)
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "cao-collect-")
	if err != nil {
		return fmt.Errorf("create collection workspace: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(workspace)
	}()
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	if err := r.downloadLogs(ctx, repository, workspace, token, reserve); err != nil {
		return err
	}
	if err := r.Lake.mineDownloadedInfo(ctx, repository, filepath.Join(workspace, "logs")); err != nil {
		return fmt.Errorf("mine recorded episode evidence: %w", err)
	}
	if err := r.Lake.mineCachedInfo(ctx, repository); err != nil {
		return fmt.Errorf("backfill cached episode evidence: %w", err)
	}
	if err := r.execute(ctx, r.node(), []string{
		filepath.Join(r.CatalogRoot, "activity", "session-evidence.mjs"),
		filepath.Join(workspace, "logs"),
		filepath.Join(r.Lake.ShardDirectory(), r.Lake.ShardPrefix(repository)),
		repository,
	}, nil); err != nil {
		return err
	}
	if err := r.compact(ctx, repository); err != nil {
		return err
	}
	runnerLog.Printf("collected evidence repository=%s", repository)
	return nil
}

func (r Runner) downloadLogs(ctx context.Context, repository, workspace, token string, reserve int) error {
	shardPrefix := filepath.Join(r.Lake.ShardDirectory(), r.Lake.ShardPrefix(repository))
	startDate := fmt.Sprintf("-%dd", r.windowDays())
	if !r.Since.IsZero() {
		startDate = r.Since.UTC().Format(time.RFC3339)
	}
	arguments := []string{
		filepath.Join(r.CatalogRoot, "activity", "collect-workflow-logs.mjs"), r.gh(), "--audit",
		"--repo", repository,
		"--output", filepath.Join(workspace, "logs"),
		"--summary-file", "",
		"--cached-jsonl", shardPrefix + "*",
		"--artifacts", "usage,experiment",
		"--start-date", startDate,
		"--cache-before", fmt.Sprintf("-%dd", r.windowDays()),
		"--count", strconv.Itoa(r.runLimit()),
		"--timeout", strconv.Itoa(r.requestTimeout()),
		"--max-github-api-rate-limit", strconv.Itoa(-reserve),
		"--max-storage", strconv.Itoa(r.maxStorage()),
		"--prune-older-runs",
	}
	return r.execute(ctx, r.node(), arguments, []string{"GH_TOKEN=" + token})
}

func (r Runner) compact(ctx context.Context, repository string) error {
	arguments := []string{
		filepath.Join(r.CatalogRoot, "activity", "cao.mjs"),
		"compact-jsonl",
		"--input-dir", r.Lake.ShardDirectory(),
		"--group", repository + "=" + r.Lake.ShardPrefix(repository),
	}
	return r.execute(ctx, r.node(), arguments, nil)
}

func (r Runner) execute(ctx context.Context, name string, arguments []string, environment []string) error {
	// #nosec G204 -- name and arguments are constructed from validated
	// configuration and a normalized repository reference.
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = r.CatalogRoot
	command.Env = append(collectionEnvironment(r.GitHubAPIURL), environment...)
	var output bytes.Buffer
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	writer := &boundedWriter{buffer: &output, limit: limit}
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, summarize(output.String()))
	}
	return nil
}

// collectionEnvironment builds a minimal environment for the subprocess. The
// process environment is not inherited wholesale so ambient credentials cannot
// leak into a collection.
func collectionEnvironment(apiURL string) []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=" + os.Getenv("LANG"),
	}
	if value := os.Getenv("GH_HOST"); value != "" {
		environment = append(environment, "GH_HOST="+value)
	}
	if value := strings.TrimSpace(apiURL); value != "" {
		environment = append(environment, "GITHUB_API_URL="+value)
		return environment
	}
	if value := os.Getenv("GITHUB_API_URL"); value != "" {
		environment = append(environment, "GITHUB_API_URL="+value)
	}
	return environment
}

type boundedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(content []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		return len(content), nil
	}
	if len(content) > remaining {
		content = content[:remaining]
	}
	if _, err := w.buffer.Write(content); err != nil {
		return 0, err
	}
	return len(content), nil
}

func summarize(output string) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) > 2000 {
		trimmed = trimmed[len(trimmed)-2000:]
	}
	return trimmed
}
