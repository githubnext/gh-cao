package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
)

type localSQLiteBackfill struct {
	database *sqlitex.Store
	snapshot string
	overlay  string
	catalog  string
	interval time.Duration
	traces   collect.TraceCapture
}

type localGitHubToken string

func (token localGitHubToken) InstallationToken(context.Context, int64) (string, error) {
	return string(token), nil
}

func localBackfillToken(ctx context.Context) (localGitHubToken, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return localGitHubToken(token), nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "", errors.New("GitHub credentials unavailable; set GH_TOKEN or sign in with gh auth login")
	}
	return localGitHubToken(strings.TrimSpace(string(output))), nil
}

func (b *localSQLiteBackfill) run(ctx context.Context, logger *log.Logger, publish func(int64)) {
	for {
		if err := b.refresh(ctx, logger, publish); err != nil && ctx.Err() == nil {
			logger.Printf("local SQLite backfill incomplete: %v; retaining committed data and retrying in %s", err, b.interval)
		}
		timer := time.NewTimer(b.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (b *localSQLiteBackfill) refresh(ctx context.Context, logger *log.Logger, publish func(int64)) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	token, err := localBackfillToken(ctx)
	if err != nil {
		return err
	}
	catalog, err := filepath.Abs(b.catalog)
	if err != nil {
		return err
	}
	overlay, err := filepath.Abs(b.overlay)
	if err != nil {
		return err
	}
	for _, binary := range []string{"node", "gh"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("local backfill requires %s", binary)
		}
	}
	// A private per-pass lake avoids mixing evidence from different snapshots
	// or concurrent local hosts, and is removed even after a failed pass.
	workspace, err := os.MkdirTemp(filepath.Dir(overlay), ".cao-backfill-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	runner := collect.Runner{
		Lake: collect.Lake{Directory: workspace}, CatalogRoot: catalog, Tokens: token,
		RunLimit: 10000, RequestTimeoutMinutes: 2, Timeout: 5 * time.Minute,
		Traces: b.traces,
	}
	if err := runner.Validate(); err != nil {
		return err
	}
	targets, err := b.database.BackfillTargets(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	// Validate the entire immutable scope before making any remote requests.
	for _, target := range targets {
		if _, err := collect.NormalizeRepository(target.Repository); err != nil {
			return errors.New("snapshot contains an invalid backfill repository")
		}
	}
	if len(targets) == 0 {
		logger.Print("local SQLite backfill: snapshot has no repositories to refresh")
		return nil
	}
	logger.Printf("local SQLite backfill starting repositories=%d", len(targets))
	var collectionErr error
	collected := 0
	for _, target := range targets {
		runner.Since = target.Since
		if err := runner.Collect(ctx, collect.Task{Repository: target.Repository}); err != nil {
			// Subprocess output can contain untrusted evidence or credentials.
			collectionErr = fmt.Errorf("GitHub evidence collection failed for %s", target.Repository)
			logger.Print(collectionErr)
			if err := runner.Lake.ForgetContext(ctx, target.Repository); err != nil {
				return errors.New("failed to discard incomplete local collection evidence")
			}
			continue
		}
		collected++
	}
	if collected == 0 {
		return collectionErr
	}
	stage := filepath.Join(workspace, "overlay.sqlite")
	if err := b.database.StageOverlay(ctx, stage); err != nil {
		return fmt.Errorf("stage local SQLite delta: %w", err)
	}
	// #nosec G204 -- executable and script are fixed local runtime resources.
	command := exec.CommandContext(ctx, "node", filepath.Join(catalog, "activity", "local-backfill.mjs"),
		stage, b.snapshot, b.database.SnapshotRevision(), runner.Lake.ShardDirectory())
	command.Dir = catalog
	// Ingestion consumes files only; do not give it the collection token.
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	if err := command.Run(); err != nil {
		return errors.New("canonical local backfill ingestion failed")
	}
	if err := b.database.PublishOverlay(ctx, stage); err != nil {
		return fmt.Errorf("publish local SQLite delta: %w", err)
	}
	state, err := b.database.State(ctx)
	if err != nil {
		return err
	}
	publish(state.Revision)
	logger.Printf("local SQLite backfill published revision=%d", state.Revision)
	return collectionErr
}
