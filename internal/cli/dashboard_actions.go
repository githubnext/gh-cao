package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	ghaw "github.com/github/gh-aw/pkg/cli"
	"github.com/spf13/cobra"
)

func dashboardActionCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "__dashboard_aw", Hidden: true}
	validateEngine := func(string) error { return nil }
	cmd.AddCommand(ghaw.NewUpgradeCommand(validateEngine), ghaw.NewUpdateCommand(validateEngine))
	disable := &cobra.Command{Use: "disable [workflow]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		repository, _ := cmd.Flags().GetString("repo")
		return ghaw.DisableWorkflowsByNames(cmd.Context(), args, repository)
	}}
	disable.Flags().String("repo", "", "Exact target repository")
	cmd.AddCommand(disable)
	enable := &cobra.Command{Use: "enable [workflow]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		repository, _ := cmd.Flags().GetString("repo")
		return ghaw.EnableWorkflowsByNames(cmd.Context(), args, repository)
	}}
	enable.Flags().String("repo", "", "Exact target repository")
	cmd.AddCommand(enable)
	return cmd
}

var dashboardWorkflowTarget = regexp.MustCompile(`^(?:\.github/workflows/)?[A-Za-z0-9][A-Za-z0-9_.-]*(?:\.md)?$`)
var dashboardSourceTarget = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$`)

var dashboardEnrollmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.*-]+/[A-Za-z0-9_.*-]+$`)

func nativeRepositoryOperationsAllowed(repository string) bool {
	return !strings.EqualFold(repository, "github/gh-aw")
}

// dashboardEnrollmentArgs reports whether args stage local review-mode
// enrollment. The pattern is new by definition, so scope does not apply.
func dashboardEnrollmentArgs(args []string) bool {
	return len(args) == 3 && args[0] == "repos" && args[1] == "add" && len(args[2]) <= 200 &&
		!strings.Contains(args[2], "..") && dashboardEnrollmentPattern.MatchString(args[2])
}

func dashboardSetupArgs(args []string) bool {
	return len(args) == 3 && args[0] == "setup" && args[1] == "--repository" && len(args[2]) <= 200 &&
		!strings.Contains(args[2], "..") && dashboardEnrollmentPattern.MatchString(args[2])
}

func dashboardActionArgs(args, repositories []string) error {
	if len(args) > 0 && args[0] == "modernization" {
		return dashboardModernizationArgs(args, repositories)
	}
	if dashboardSetupArgs(args) {
		if !nativeRepositoryOperationsAllowed(args[2]) {
			return errors.New("github/gh-aw is excluded from CAO operations")
		}
		return nil
	}
	if dashboardEnrollmentArgs(args) {
		if !nativeRepositoryOperationsAllowed(args[2]) {
			return errors.New("github/gh-aw is excluded from CAO operations")
		}
		return nil
	}
	if len(args) == 0 || !slices.Contains([]string{"upgrade", "update", "disable", "enable"}, args[0]) {
		return errors.New("only CAO setup, repository enrollment, and gh-aw maintenance commands are supported")
	}
	options := args[1:]
	if args[0] != "upgrade" && len(options) > 0 && !strings.HasPrefix(options[0], "--") {
		target := options[0]
		if strings.Contains(target, "..") || !dashboardWorkflowTarget.MatchString(target) &&
			(args[0] != "update" || !dashboardSourceTarget.MatchString(target)) {
			return errors.New("maintenance requires an exact workflow or GitHub update source")
		}
		options = options[1:]
	}
	repository, publish := "", false
	seen := map[string]bool{}
	for index := 0; index < len(options); index++ {
		option := options[index]
		if seen[option] {
			return errors.New("repeated maintenance option")
		}
		seen[option] = true
		switch option {
		case "--repo":
			if index+1 == len(options) {
				return errors.New("maintenance requires an exact repository")
			}
			index++
			repository = strings.ToLower(options[index])
		case "--create-pull-request":
			if args[0] == "disable" || args[0] == "enable" {
				return errors.New("enable/disable does not publish a pull request")
			}
			publish = true
		case "--pre-releases":
			if args[0] != "upgrade" {
				return errors.New("pre-releases is supported only for compiler upgrades")
			}
		default:
			return errors.New("unsupported maintenance option")
		}
	}
	if !nativeRepositoryOperationsAllowed(repository) {
		return errors.New("github/gh-aw is excluded from CAO operations")
	}
	if (args[0] != "disable" && args[0] != "enable" && !publish) || !slices.Contains(repositories, repository) || repository == "" {
		return errors.New("maintenance requires pull-request publication within the selected repository scope")
	}
	return nil
}

type dashboardActionEvent struct {
	Stream   string `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"`
	Complete bool   `json:"complete,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// Output stays line-framed so a registered credential cannot straddle RPC frames.
type dashboardActionWriter struct {
	stream     string
	buffer     []byte
	suppressed bool
	secrets    []string
	send       func(dashboardActionEvent) error
	maxLine    int
}

func (w *dashboardActionWriter) Write(data []byte) (int, error) {
	for _, value := range data {
		if value == '\n' {
			if err := w.flush(); err != nil {
				return 0, err
			}
		} else if !w.suppressed {
			w.buffer = append(w.buffer, value)
			limit := w.maxLine
			if limit == 0 {
				limit = 8192
			}
			if len(w.buffer) > limit {
				w.buffer = nil
				w.suppressed = true
			}
		}
	}
	return len(data), nil
}

func (w *dashboardActionWriter) flush() error {
	data := string(w.buffer)
	if w.suppressed {
		data = "[Overlong command output line suppressed.]"
	}
	for _, secret := range w.secrets {
		data = strings.ReplaceAll(data, secret, "[REDACTED]")
		// Conservatively suppress a credential prefix at EOF.
		for size := min(len(secret)-1, len(data)); size > 3; size-- {
			if strings.HasSuffix(data, secret[:size]) {
				data = data[:len(data)-size] + "[REDACTED]"
				break
			}
		}
	}
	w.buffer, w.suppressed = nil, false
	return w.send(dashboardActionEvent{Stream: w.stream, Data: data + "\n"})
}

func startDashboardActionAdapter(ctx context.Context, repositories []string, cache string, ingest chan<- struct{}) (string, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	return startDashboardActionAdapterWithExecutable(ctx, repositories, cache, executable, ingest)
}

// ingest, when non-nil, receives coalesced operator requests to start an
// ingestion pass now. It must be buffered; requests never block the adapter.
func startDashboardActionAdapterWithExecutable(ctx context.Context, repositories []string, cache, executable string, ingest chan<- struct{}) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	capability := func() (string, error) {
		data := make([]byte, 24)
		_, err := rand.Read(data)
		return "/" + hex.EncodeToString(data), err
	}
	adapterPrefix, err := capability()
	if err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	adapterCtx, cancel := context.WithCancel(ctx)
	var executions sync.Mutex
	active := map[string]context.CancelFunc{}
	canceled := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+adapterPrefix+"/repositories/search", dashboardRepositorySearch)
	mux.HandleFunc("GET "+adapterPrefix+"/repositories/{owner}/{repo}/update-access", func(w http.ResponseWriter, r *http.Request) {
		repository := strings.ToLower(r.PathValue("owner") + "/" + r.PathValue("repo"))
		if !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") || len(repository) > 200 {
			http.Error(w, "invalid repository identity", http.StatusBadRequest)
			return
		}
		scope, err := dashboardUpdateRepositories(r.Context(), repositories)
		if err != nil {
			http.Error(w, "managed update scope is unavailable; fix the local CAO configuration", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": slices.Contains(scope, repository)})
	})
	mux.HandleFunc("POST "+adapterPrefix+"/cancel", func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Command-ID")
		if !dashboardRunID.MatchString(id) {
			http.Error(w, "invalid command identity", http.StatusBadRequest)
			return
		}
		executions.Lock()
		canceled[id] = true
		if stop := active[id]; stop != nil {
			stop()
		}
		executions.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	if ingest != nil {
		mux.HandleFunc("POST "+adapterPrefix+"/ingest", func(w http.ResponseWriter, _ *http.Request) {
			select {
			case ingest <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusAccepted)
		})
	}
	mux.HandleFunc("POST "+adapterPrefix+"/execute", func(w http.ResponseWriter, r *http.Request) {
		var args []string
		var input *string
		content, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if readErr != nil {
			http.Error(w, "invalid scoped maintenance command", http.StatusForbidden)
			return
		}
		if json.Unmarshal(content, &args) != nil {
			var request struct {
				Args  []string `json:"args"`
				Input *string  `json:"input"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(content)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
				len(request.Args) == 0 || request.Args[0] != "modernization" {
				http.Error(w, "invalid native domain command", http.StatusForbidden)
				return
			}
			args, input = request.Args, request.Input
		}
		if len(args) > 0 && args[0] != "modernization" && len(content) > 8192 {
			http.Error(w, "maintenance command exceeds the existing request bound", http.StatusForbidden)
			return
		}
		if len(args) > 0 && args[0] == "modernization" && !dashboardModernizationInput(args, input) {
			http.Error(w, "invalid modernization plan input", http.StatusForbidden)
			return
		}
		scope := repositories
		if len(args) > 0 && (args[0] == "update" || args[0] == "upgrade") {
			var err error
			scope, err = dashboardUpdateRepositories(r.Context(), repositories)
			if err != nil {
				http.Error(w, "managed update scope is unavailable; no command was started", http.StatusForbidden)
				return
			}
		}
		if dashboardActionArgs(args, scope) != nil {
			http.Error(w, "invalid scoped maintenance command", http.StatusForbidden)
			return
		}
		id := r.Header.Get("X-Command-ID")
		if !dashboardRunID.MatchString(id) {
			http.Error(w, "invalid command identity", http.StatusBadRequest)
			return
		}
		executionCtx, stop := context.WithTimeout(adapterCtx, 15*time.Minute)
		defer stop()
		stopRequest := context.AfterFunc(r.Context(), stop)
		defer stopRequest()
		executions.Lock()
		if active[id] != nil {
			executions.Unlock()
			http.Error(w, "command already executing", http.StatusConflict)
			return
		}
		active[id] = stop
		if canceled[id] {
			stop()
		}
		executions.Unlock()
		defer func() {
			executions.Lock()
			// A completed identity is not replayable within this invocation.
			canceled[id] = true
			executions.Unlock()
		}()
		w.Header().Set("Content-Type", "application/x-ndjson")
		var output sync.Mutex
		send := func(event dashboardActionEvent) error {
			output.Lock()
			defer output.Unlock()
			if err := json.NewEncoder(w).Encode(event); err != nil {
				return err
			}
			return http.NewResponseController(w).Flush()
		}
		code := 1
		if executionCtx.Err() != nil {
			code = 130
			_ = send(dashboardActionEvent{Stream: "stderr", Data: "Command canceled before execution.\n"})
			_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
			return
		}
		if err := send(dashboardActionEvent{Stream: "stdout", Data: "Executing queued command using the local GitHub CLI identity.\n"}); err != nil {
			return
		}
		childArgs := append([]string{"__dashboard_aw"}, args...)
		if dashboardEnrollmentArgs(args) || dashboardSetupArgs(args) {
			// Native CAO commands update only the dashboard's control checkout.
			childArgs = slices.Clone(args)
		}
		if args[0] == "modernization" {
			childArgs = slices.Clone(args)
		}
		child := exec.CommandContext(executionCtx, executable, childArgs...)
		if args[0] == "disable" || args[0] == "enable" {
			directory, prepareErr := prepareDashboardDisableCheckout(executionCtx, cache, args)
			if prepareErr != nil {
				_ = send(dashboardActionEvent{Stream: "stderr", Data: "Exact target checkout could not be prepared; no workflows were enabled or disabled.\n"})
				_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
				return
			}
			defer func() { _ = os.RemoveAll(directory) }()
			child.Dir = filepath.Join(directory, "repository")
		}
		configureDashboardProcess(child)
		child.Env = append(os.Environ(), "GH_HOST=github.com")
		if args[0] == "modernization" {
			child.Env = append(dashboardDisableEnvironment(os.Environ()), "CAO_MODERNIZATION_PREVIEW_DIR="+filepath.Join(cache, "modernization-previews"))
			if input != nil {
				child.Stdin = strings.NewReader(*input)
			}
		}
		if args[0] == "update" || args[0] == "upgrade" {
			environment, readinessErr := dashboardGitEnvironment(executionCtx, child.Env)
			if readinessErr != nil {
				_ = send(dashboardActionEvent{Stream: "stderr", Data: readinessErr.Error() + "; no maintenance command was started.\n"})
				_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
				return
			}
			child.Env = environment
		}
		if args[0] == "disable" || args[0] == "enable" {
			child.Env = dashboardDisableEnvironment(os.Environ())
		}
		child.WaitDelay = 5 * time.Second
		secrets := []string{}
		for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
			if value := os.Getenv(name); value != "" {
				secrets = append(secrets, value)
			}
		}
		stdout := &dashboardActionWriter{stream: "stdout", send: send, secrets: secrets}
		if args[0] == "modernization" {
			stdout.maxLine = 1 << 20
		}
		stderr := &dashboardActionWriter{stream: "stderr", send: send, secrets: secrets}
		child.Stdout, child.Stderr = stdout, stderr
		err := child.Run()
		if len(stdout.buffer) > 0 || stdout.suppressed {
			_ = stdout.flush()
		}
		if len(stderr.buffer) > 0 || stderr.suppressed {
			_ = stderr.flush()
		}
		if child.ProcessState != nil {
			code = child.ProcessState.ExitCode()
			if code < 0 {
				code = 130
			}
		}
		if err != nil && child.ProcessState == nil {
			_ = send(dashboardActionEvent{Stream: "stderr", Data: "Native maintenance process could not start.\n"})
		}
		_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
	})
	var requests sync.WaitGroup
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		defer requests.Done()
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return adapterCtx }}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(os.Stderr, "Dashboard maintenance adapter failed: %v\n", err)
			cancel()
		}
	}()
	config, err := os.CreateTemp(cache, ".operator-actions-*.json")
	if err == nil {
		settings := map[string]any{
			"adapterUrl": "http://" + listener.Addr().String() + adapterPrefix,
		}
		if runtime.GOOS != "windows" {
			settings["operatorActions"] = map[string]any{
				"repositories": repositories,
			}
			_, managed := ctx.Value(dashboardConfigurationKey{}).(*dashboardConfiguration)
			settings["managedUpdates"] = managed
		}
		err = json.NewEncoder(config).Encode(settings)
		err = errors.Join(err, config.Close())
	}
	cleanup := func() {
		cancel()
		closeCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(closeCtx)
		_ = server.Close()
		requests.Wait()
		if config != nil {
			_ = os.Remove(config.Name())
		}
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return config.Name(), cleanup, nil
}

// gh-aw disable reads local Markdown inventory and cancels using the ambient
// Git repository. Both must belong to the approved target, never this checkout.
func prepareDashboardDisableCheckout(ctx context.Context, cache string, args []string) (string, error) {
	repository := args[len(args)-1]
	directory, err := os.MkdirTemp(cache, ".disable-checkout-")
	if err != nil {
		return "", err
	}
	target := filepath.Join(directory, "repository")
	command := exec.CommandContext(ctx, "gh", "repo", "clone", repository, target, "--", "--depth=1", "--template=", "--config", "core.hooksPath=/dev/null")
	configureDashboardProcess(command)
	command.WaitDelay = 5 * time.Second
	command.Env = dashboardDisableEnvironment(os.Environ())
	if err := command.Run(); err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	return directory, nil
}

func dashboardDisableEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+3)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		// Preserve CLI authentication/configuration, not ambient repository or
		// injected Git configuration that can redirect the approved checkout.
		if name == "GH_REPO" || name == "GH_HOST" || name == "GH_AW_WORKFLOWS_DIR" || name == "GH_PROMPT_DISABLED" || name == "GIT_TERMINAL_PROMPT" ||
			strings.HasPrefix(name, "GIT_") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
}

var _ io.Writer = (*dashboardActionWriter)(nil)
