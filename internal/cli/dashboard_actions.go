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
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	ghaw "github.com/github/gh-aw/pkg/cli"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func dashboardActionCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "__dashboard_aw", Hidden: true}
	validateEngine := func(string) error { return nil }
	cmd.AddCommand(ghaw.NewUpgradeCommand(validateEngine), ghaw.NewUpdateCommand(validateEngine))
	return cmd
}

func dashboardActionArgs(args, repositories []string) error {
	if len(args) == 0 || !slices.Contains([]string{"upgrade", "update"}, args[0]) {
		return errors.New("only gh-aw maintenance commands are supported")
	}
	options := args[1:]
	if args[0] == "update" {
		if len(options) == 0 || !strings.HasPrefix(options[0], "https://github.com/") ||
			strings.Contains(options[0], "..") || strings.ContainsAny(options[0], "?#\\ \r\n\x00") {
			return errors.New("updates require an exact GitHub source")
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
			publish = true
		case "--pre-releases":
			if args[0] != "upgrade" {
				return errors.New("pre-releases is supported only for compiler upgrades")
			}
		default:
			return errors.New("unsupported maintenance option")
		}
	}
	if !publish || !slices.Contains(repositories, repository) {
		return errors.New("maintenance requires pull-request publication within the selected repository scope")
	}
	return nil
}

func approveDashboardAction(ctx context.Context, args []string) (bool, error) {
	inputName, outputName := "/dev/tty", "/dev/tty"
	if runtime.GOOS == "windows" {
		inputName, outputName = "CONIN$", "CONOUT$"
	}
	input, err := os.Open(inputName)
	if err != nil {
		return false, errors.New("trusted terminal approval is unavailable; run gh cao dashboard from an interactive terminal")
	}
	defer func() { _ = input.Close() }()
	if !term.IsTerminal(int(input.Fd())) {
		return false, errors.New("trusted terminal approval requires a terminal")
	}
	output, err := os.OpenFile(outputName, os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = output.Close() }()
	if _, err := fmt.Fprintf(output, "\nDashboard maintenance requests:\n  gh aw %s\nApprove this command using your local GitHub CLI identity? [y/N] ", strings.Join(args, " ")); err != nil {
		return false, err
	}
	text, err := readDashboardApproval(ctx, input)
	return strings.EqualFold(strings.TrimSpace(text), "y") && err == nil, err
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
}

func (w *dashboardActionWriter) Write(data []byte) (int, error) {
	for _, value := range data {
		if value == '\n' {
			if err := w.flush(); err != nil {
				return 0, err
			}
		} else if !w.suppressed {
			w.buffer = append(w.buffer, value)
			if len(w.buffer) > 8192 {
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

func startDashboardActionAdapter(ctx context.Context, repositories []string, cache string) (string, func(), error) {
	return startDashboardActionAdapterWithApproval(ctx, repositories, cache, approveDashboardAction)
}

func startDashboardActionAdapterWithApproval(ctx context.Context, repositories []string, cache string, approve func(context.Context, []string) (bool, error)) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	capability := func() (string, error) {
		data := make([]byte, 24)
		_, err := rand.Read(data)
		return "/" + hex.EncodeToString(data), err
	}
	prefix, err := capability()
	if err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	adapterPrefix, err := capability()
	if err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	adapterCtx, cancel := context.WithCancel(ctx)
	var approval sync.Mutex
	var executions sync.Mutex
	active := map[string]context.CancelFunc{}
	canceled := map[string]bool{}
	mux := http.NewServeMux()
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
	mux.HandleFunc("POST "+adapterPrefix+"/execute", func(w http.ResponseWriter, r *http.Request) {
		var args []string
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&args) != nil ||
			dashboardActionArgs(args, repositories) != nil {
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
		approval.Lock()
		approved, approvalErr := approve(executionCtx, args)
		approval.Unlock()
		if !approved || executionCtx.Err() != nil {
			message := "Terminal approval declined."
			if approvalErr != nil {
				message = approvalErr.Error()
			}
			_ = send(dashboardActionEvent{Stream: "stderr", Data: message + "\n"})
			_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
			return
		}
		executable, err := os.Executable()
		if err != nil {
			_ = send(dashboardActionEvent{Stream: "stderr", Data: "Native maintenance executable is unavailable.\n"})
			_ = send(dashboardActionEvent{Complete: true, ExitCode: &code})
			return
		}
		child := exec.CommandContext(executionCtx, executable, append([]string{"__dashboard_aw"}, args...)...)
		configureDashboardProcess(child)
		child.Env = append(os.Environ(), "GH_HOST=github.com")
		child.WaitDelay = 5 * time.Second
		secrets := []string{}
		for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
			if value := os.Getenv(name); value != "" {
				secrets = append(secrets, value)
			}
		}
		stdout := &dashboardActionWriter{stream: "stdout", send: send, secrets: secrets}
		stderr := &dashboardActionWriter{stream: "stderr", send: send, secrets: secrets}
		child.Stdout, child.Stderr = stdout, stderr
		err = child.Run()
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
			"prefix": prefix, "adapterUrl": "http://" + listener.Addr().String() + adapterPrefix,
		}
		if runtime.GOOS != "windows" {
			settings["operatorActions"] = map[string]any{
				"queuePath": filepath.Join(cache, "commands.sqlite"), "repositories": repositories,
			}
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

var _ io.Writer = (*dashboardActionWriter)(nil)
