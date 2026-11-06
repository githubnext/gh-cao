package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

const dashboardMaximumDuration = 48 * time.Hour

type dashboardOptions struct {
	repositories []string
	cache        string
	logs         string
	listen       string
	server       string
	site         string
	window       time.Duration
	duration     time.Duration
	refresh      time.Duration
	count        int
}

func dashboardCommand(version string, gh ghExecutor, logs dashboardLogsCollector) *cobra.Command {
	options := &dashboardOptions{}
	cmd := &cobra.Command{
		Use: "dashboard", Short: "Mine recent repository activity and serve a temporary local dashboard",
		Args: cobra.NoArgs,
		Long: `Mine recent gh-aw activity and serve the existing Go dashboard on loopback.

No CAO configuration, repository enrollment, Actions installation, Node.js,
database service, or source checkout is required by release binaries.
GitHub CLI authentication is required for collection. The pinned gh-aw log
collector is embedded; no separate gh-aw installation is needed. Existing
gh-aw artifact caches are reused. The evidence window and server lifetime
default to 48 hours; collection is bounded and never promises complete history.
Ctrl-C stops the server; the private local snapshot is retained for reuse.
Collection never changes policy, workflows, or remote repository state.`,
	}
	cmd.PersistentFlags().StringArrayVarP(&options.repositories, "repo", "R", nil, "Exact OWNER/REPO (repeatable; defaults to current repository)")
	cmd.PersistentFlags().StringVar(&options.cache, "cache-dir", "", "Private local cache directory (default user cache, keyed by repository scope)")
	cmd.PersistentFlags().DurationVar(&options.window, "window", dashboardMaximumDuration, "Recent evidence window (positive, at most 48h)")
	cmd.PersistentFlags().IntVar(&options.count, "count", 200, "Per-repository limit for recent runs and cached usage samples (1-1000 each)")
	cmd.PersistentFlags().StringVar(&options.logs, "logs-dir", "", "Existing gh-aw artifact cache to reuse (single repository only)")
	cmd.Flags().StringVar(&options.listen, "listen", "127.0.0.1:0", "Numeric loopback listener; port 0 selects an available port")
	cmd.Flags().DurationVar(&options.duration, "duration", dashboardMaximumDuration, "Server lifetime (positive, at most 48h)")
	cmd.Flags().DurationVar(&options.refresh, "refresh", 5*time.Minute, "Refresh interval (at least 1m)")
	cmd.Flags().StringVar(&options.server, "server-executable", "", "Explicit existing cao-dashboard binary for source development")
	cmd.Flags().StringVar(&options.site, "site", "", "Explicit built dashboard site for source development")
	cmd.MarkFlagsRequiredTogether("server-executable", "site")
	resolve := func(cmd *cobra.Command) ([]string, string, error) {
		return resolveDashboardOptions(cmd, options, gh)
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if options.duration <= 0 || options.duration > dashboardMaximumDuration {
			return errors.New("--duration must be positive and at most 48h")
		}
		if options.refresh < time.Minute {
			return errors.New("--refresh must be at least 1m")
		}
		if err := validateDashboardListen(options.listen); err != nil {
			return err
		}
		repositories, cache, err := resolve(cmd)
		if err != nil {
			return err
		}
		return serveDashboard(cmd, version, repositories, cache, *options, gh, logs)
	}
	status := &cobra.Command{Use: "status", Short: "Show retained snapshot scope, collection time, and coverage", Args: cobra.NoArgs}
	status.RunE = func(cmd *cobra.Command, _ []string) error {
		repositories, cache, err := resolve(cmd)
		if err != nil {
			return err
		}
		metadata, err := readDashboardStatus(cmd.Context(), cache, repositories)
		if err != nil {
			return err
		}
		return printJSON(cmd, metadata)
	}
	refresh := &cobra.Command{Use: "refresh", Short: "Refresh the scoped snapshot without starting a server", Args: cobra.NoArgs}
	refresh.RunE = func(cmd *cobra.Command, _ []string) error {
		repositories, cache, err := resolve(cmd)
		if err != nil {
			return err
		}
		metadata, err := collectDashboard(cmd.Context(), repositories, cache, *options, gh, logs)
		if err != nil {
			return err
		}
		return printJSON(cmd, metadata)
	}
	prompt := &cobra.Command{Use: "prompt QUERY_ID", Short: "Render an operational prompt from the retained local snapshot", Args: cobra.ExactArgs(1)}
	prompt.Flags().String("database", "", "Selected automatically from the dashboard cache")
	_ = prompt.Flags().MarkHidden("database")
	prompt.Flags().String("dashboard", "", "Optional Dashboard Language document")
	prompt.Flags().StringArray("param", nil, "Query parameter NAME=VALUE (repeatable)")
	prompt.RunE = func(cmd *cobra.Command, args []string) error {
		repositories, cache, err := resolve(cmd)
		if err != nil {
			return err
		}
		if _, err := readDashboardStatus(cmd.Context(), cache, repositories); err != nil {
			return err
		}
		if err := cmd.Flags().Set("database", filepath.Join(cache, "gh-aw-logs.sqlite")); err != nil {
			return err
		}
		return nativePrompt(cmd, args[0])
	}
	run := &cobra.Command{Use: "run", Short: "Explicitly cancel or rerun one observed run using your GitHub identity", Args: cobra.NoArgs}
	for _, action := range []string{"cancel", "rerun"} {
		child := &cobra.Command{Use: action + " RUN_ID", Short: action + " one run in the selected repository", Args: cobra.ExactArgs(1)}
		child.RunE = func(cmd *cobra.Command, args []string) error {
			repositories, cache, err := resolve(cmd)
			if err != nil {
				return err
			}
			if len(repositories) != 1 {
				return errors.New("run operations require exactly one --repo")
			}
			if !dashboardRunID.MatchString(args[0]) {
				return errors.New("RUN_ID must be a positive GitHub run ID")
			}
			if err := requireDashboardRun(cmd.Context(), cache, repositories[0], args[0]); err != nil {
				return err
			}
			_, _, err = gh(cmd.Context(), "run", cmd.Name(), args[0], "--repo", "github.com/"+repositories[0])
			if err != nil {
				return errors.New("GitHub run operation failed; verify your repository permissions and the current run state")
			}
			return printJSON(cmd, map[string]any{"repository": repositories[0], "runId": args[0], "action": cmd.Name()})
		}
		run.AddCommand(child)
	}
	cmd.AddCommand(status, refresh, prompt, run)
	return cmd
}

func resolveDashboardOptions(cmd *cobra.Command, options *dashboardOptions, gh ghExecutor) ([]string, string, error) {
	if options.window <= 0 || options.window > dashboardMaximumDuration {
		return nil, "", errors.New("--window must be positive and at most 48h")
	}
	if options.count < 1 || options.count > 1000 {
		return nil, "", errors.New("--count must be between 1 and 1000")
	}
	repositories := slices.Clone(options.repositories)
	if len(repositories) > 20 {
		return nil, "", errors.New("dashboard supports at most 20 explicitly selected repositories")
	}
	for _, repository := range repositories {
		if !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") {
			return nil, "", errors.New("--repo requires an exact OWNER/REPO on github.com")
		}
	}
	if len(repositories) == 0 {
		stdout, _, err := gh(cmd.Context(), "repo", "view", "--json", "nameWithOwner,url")
		if err != nil {
			return nil, "", errors.New("cannot resolve the current GitHub repository; pass --repo OWNER/REPO")
		}
		var identity struct {
			Name string `json:"nameWithOwner"`
			URL  string `json:"url"`
		}
		if json.Unmarshal([]byte(stdout), &identity) != nil || !repositoryPattern.MatchString(identity.Name) ||
			identity.URL != "https://github.com/"+identity.Name {
			return nil, "", errors.New("current repository must resolve to an exact github.com repository")
		}
		repositories = []string{identity.Name}
	}
	for index := range repositories {
		// Explicit host prevents an ambient GH_HOST from silently changing scope.
		repositories[index] = strings.ToLower(repositories[index])
	}
	slices.Sort(repositories)
	repositories = slices.Compact(repositories)
	if options.logs != "" && len(repositories) != 1 {
		return nil, "", errors.New("--logs-dir requires exactly one repository")
	}
	cache := options.cache
	if cache == "" {
		root, err := os.UserCacheDir()
		if err != nil {
			return nil, "", err
		}
		digest := sha256.Sum256([]byte("github.com\x00" + strings.Join(repositories, "\x00")))
		cache = filepath.Join(root, "gh-cao", "dashboard", hex.EncodeToString(digest[:12]))
	}
	cache, err := filepath.Abs(cache)
	return repositories, cache, err
}

func boundedDashboardGH(ctx context.Context, args ...string) (string, string, error) {
	value, err := ghcli.ExecBounded(ctx, 32<<20, args...)
	return value, "", err
}
