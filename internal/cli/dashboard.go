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
const dashboardMaximumWindow = 7 * 24 * time.Hour

type dashboardOptions struct {
	repositories    []string
	cache           string
	logs            string
	listen          string
	server          string
	site            string
	postgresURL     string
	containerEngine string
	window          time.Duration
	duration        time.Duration
	refresh         time.Duration
	count           int
	// onCollecting, when set, runs synchronously once a pass has published its
	// "collecting" status so the serving process can show it before the pass ends.
	onCollecting func()
}

func dashboardCommand(version string, gh ghExecutor, logs dashboardLogsCollector) *cobra.Command {
	options := &dashboardOptions{}
	cmd := &cobra.Command{
		Use: "dashboard", Short: "Serve a local dashboard of recent repository activity",
		Args: cobra.NoArgs,
		Long: `Mine recent gh-aw activity and serve the existing Go dashboard on loopback.

No CAO configuration, repository enrollment, Actions installation, Node.js,
or source checkout is required by release binaries. Unless --postgres-url or
CAO_POSTGRES_URL is supplied, a local PostgreSQL container is pulled, started
and provisioned automatically using Docker or Podman (Docker is tried first).
Pass --container-engine docker|podman (or set CAO_CONTAINER_ENGINE) to use only
that engine. Data stays in the ignored local dashboard cache.
GitHub CLI authentication is required for collection. The pinned gh-aw log
collector is embedded; no separate gh-aw installation is needed. Existing
gh-aw artifact caches are reused. The evidence window defaults to seven days;
the server lifetime defaults to 48 hours. Bounded background passes resume
historical discovery while reserving GitHub API quota for other work.
Ctrl-C stops the server; the private local snapshot is retained for reuse.
Collection never changes policy, workflows, or remote repository state.`,
	}
	cmd.Flags().StringArrayVarP(&options.repositories, "repo", "R", nil, "Exact OWNER/REPO (repeatable; defaults to current repository)")
	cmd.Flags().StringVar(&options.cache, "cache-dir", "", "Private cache directory (default .github/cao/dashboard/<repository-scope>)")
	cmd.Flags().DurationVar(&options.window, "window", dashboardMaximumWindow, "Recent evidence window (positive, at most 168h)")
	cmd.Flags().IntVar(&options.count, "count", 200, "Per-repository limit for recent runs and cached usage samples (1-1000 each)")
	cmd.Flags().StringVar(&options.logs, "logs-dir", "", "Existing gh-aw artifact cache to reuse (single repository only)")
	cmd.Flags().StringVar(&options.listen, "listen", "127.0.0.1:0", "Numeric loopback listener; port 0 selects an available port")
	cmd.Flags().DurationVar(&options.duration, "duration", dashboardMaximumDuration, "Server lifetime (positive, at most 48h)")
	cmd.Flags().DurationVar(&options.refresh, "refresh", 5*time.Minute, "Refresh interval (at least 1m)")
	cmd.Flags().StringVar(&options.server, "server-executable", "", "Explicit existing cao-dashboard binary for source development")
	cmd.Flags().StringVar(&options.site, "site", "", "Explicit built dashboard site for source development")
	cmd.Flags().StringVar(&options.postgresURL, "postgres-url", "", "PostgreSQL connection URL (or CAO_POSTGRES_URL)")
	cmd.Flags().StringVar(&options.containerEngine, "container-engine", "", "Container engine for managed PostgreSQL: docker or podman (or CAO_CONTAINER_ENGINE; otherwise auto-detect)")
	_ = cmd.RegisterFlagCompletionFunc("container-engine", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"docker", "podman"}, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.MarkFlagsRequiredTogether("server-executable", "site")
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
		if cmd.Flags().Changed("postgres-url") && strings.TrimSpace(options.postgresURL) == "" {
			return errors.New("--postgres-url must name a PostgreSQL connection when specified")
		}
		if options.postgresURL == "" {
			options.postgresURL = os.Getenv("CAO_POSTGRES_URL")
		}
		if options.postgresURL != "" && strings.TrimSpace(options.postgresURL) == "" {
			return errors.New("CAO_POSTGRES_URL must name a PostgreSQL connection when set")
		}
		engine, err := resolveDashboardContainerEngine(options.containerEngine, cmd.Flags().Changed("container-engine"))
		if err != nil {
			return err
		}
		options.containerEngine = engine
		repositories, cache, err := resolveDashboardOptions(cmd, options, gh)
		if err != nil {
			return err
		}
		return serveDashboard(cmd, version, repositories, cache, *options, gh, logs)
	}
	return cmd
}

func resolveDashboardOptions(cmd *cobra.Command, options *dashboardOptions, gh ghExecutor) ([]string, string, error) {
	if options.window <= 0 || options.window > dashboardMaximumWindow {
		return nil, "", errors.New("--window must be positive and at most 168h")
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
		root, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		digest := sha256.Sum256([]byte("github.com\x00" + strings.Join(repositories, "\x00")))
		cache = filepath.Join(root, ".github", "cao", "dashboard", hex.EncodeToString(digest[:12]))
	}
	cache, err := filepath.Abs(cache)
	return repositories, cache, err
}

func boundedDashboardGH(ctx context.Context, args ...string) (string, string, error) {
	value, err := ghcli.ExecBounded(ctx, 32<<20, args...)
	return value, "", err
}
