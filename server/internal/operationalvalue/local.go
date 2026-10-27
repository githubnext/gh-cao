package operationalvalue

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const (
	hostedQueryRequestBytes  = 1 << 20
	hostedQueryResponseBytes = 16 << 20
	hostedQueryLimit         = 32
)

type QueryExecutor func(context.Context, []query.Definition, []string) (map[string]model.Source, error)

type LocalValuePlan struct {
	Repositories        []string `json:"repositories"`
	DisabledCampaigns   []string `json:"disabledCampaigns"`
	DisabledWorkflows   []string `json:"disabledWorkflows"`
	DisabledCollections []string `json:"disabledCollections"`
}

func WriteCollectionHealth(ctx context.Context, config Config, policy, snapshot string) error {
	arguments := make([]string, 0, 19+2*len(config.Repositories))
	arguments = append(arguments,
		filepath.Join(config.Root, "activity", "workflow-values.mjs"),
		"--root", config.Root, "--policy", policy, "--defer", "shared-budget-unavailable",
		"--database", filepath.Join(snapshot, "unused-value.sqlite"),
		"--output", filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-values.jsonl"),
		"--inventory", filepath.Join(snapshot, "inventory-sources.json"),
		"--workspace", filepath.Join(snapshot, "workflow-value-workspaces"),
		"--localRequest", filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-value-local.json"),
		"--snapshot", "hosted",
	)
	for _, repository := range config.Repositories {
		arguments = append(arguments, "--repository", repository)
	}
	return runLocalRuntime(ctx, config, arguments)
}

func ComputeLocalSnapshot(ctx context.Context, config Config, policy, snapshot, retentionDays string) error {
	if err := runLocalRuntime(ctx, config, []string{
		filepath.Join(config.Root, "activity", "cao.mjs"), "ingest-jsonl",
		"--database", config.Database, "--runs-dir", filepath.Join(snapshot, "gh-aw-logs-runs"),
		"--records-dir", filepath.Join(snapshot, "gh-aw-logs-records"),
		"--retention-days", retentionDays, "--run-retention-days", retentionDays,
	}); err != nil {
		return err
	}
	return runLocalRuntime(ctx, config, []string{
		filepath.Join(config.Root, "activity", "local-values.mjs"), "compute",
		"--root", config.Root, "--policy", policy, "--snapshot", snapshot, "--snapshotKey", "hosted",
		"--database", config.Database, "--output", config.Output,
	})
}

func ReadLocalValuePlan(ctx context.Context, config Config, policy, snapshot string) (LocalValuePlan, error) {
	content, err := runLocalRuntimeOutput(ctx, config, []string{
		filepath.Join(config.Root, "activity", "local-values.mjs"), "plan",
		"--policy", policy, "--snapshot", snapshot, "--snapshotKey", "hosted",
	})
	if err != nil {
		return LocalValuePlan{}, err
	}
	var plan LocalValuePlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return LocalValuePlan{}, errors.New("local Value plan returned an invalid result")
	}
	return plan, nil
}

func ComputeHostedSnapshot(
	ctx context.Context,
	config Config,
	policy, snapshot, output string,
	execute QueryExecutor,
) error {
	if execute == nil {
		return errors.New("hosted local Value requires the canonical PostgreSQL query boundary")
	}
	bridge, err := startHostedQueryBridge(ctx, execute)
	if err != nil {
		return err
	}
	defer bridge.Close()
	config.Environment = append(slices.Clone(config.Environment), "CAO_DASHBOARD_QUERY_SOCKET="+bridge.path)
	return runLocalRuntime(ctx, config, []string{
		filepath.Join(config.Root, "activity", "local-values.mjs"), "compute",
		"--root", config.Root, "--policy", policy, "--snapshot", snapshot, "--snapshotKey", "hosted",
		"--output", output,
	})
}

func runLocalRuntime(ctx context.Context, config Config, arguments []string) error {
	content, err := runLocalRuntimeOutput(ctx, config, arguments)
	if err != nil {
		return err
	}
	if len(content) != 0 {
		var result struct {
			Warnings []json.RawMessage `json:"warnings"`
		}
		if err := json.Unmarshal(content, &result); err != nil {
			return errors.New("local Value runtime returned an invalid result")
		}
		if len(result.Warnings) != 0 {
			slog.WarnContext(ctx, "local Value adapters deferred or failed; only completed evidence retained",
				"warning_count", len(result.Warnings))
		}
	}
	return nil
}

func runLocalRuntimeOutput(ctx context.Context, config Config, arguments []string) ([]byte, error) {
	timeout := config.WorkerTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	node := config.NodeBinary
	if node == "" {
		node = "node"
	}
	command := exec.CommandContext(ctx, node, arguments...) // #nosec G204 -- fixed trusted local runtime and explicit paths.
	command.Dir = config.Root
	command.Env = slices.DeleteFunc(slices.Clone(config.Environment), func(value string) bool {
		return strings.HasPrefix(value, "GH_TOKEN=") || strings.HasPrefix(value, "GITHUB_TOKEN=") ||
			strings.HasPrefix(value, "CAO_OPERATIONAL_VALUE_GH_TOKEN=")
	})
	command.Env = append(command.Env, "CAO_VALUE_SHARED_PROCESS_GROUP=1")
	configureProcessTree(command)
	command.Cancel = func() error { return terminateProcessTree(command.Process) }
	command.WaitDelay = 5 * time.Second
	stdout, stderr := newLimitedBuffer(1<<20, cancel), newLimitedBuffer(16384, cancel)
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	_ = terminateProcessTree(command.Process)
	if err != nil || stdout.Exceeded() || stderr.Exceeded() {
		return nil, errors.New("local Value runtime failed or exceeded its budget; accepted evidence retained")
	}
	return stdout.Bytes(), nil
}

type hostedQueryBridge struct {
	path     string
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
}

type hostedQueryRequest struct {
	SchemaVersion int                `json:"schemaVersion"`
	Queries       []query.Definition `json:"queries"`
	Requested     []string           `json:"requested"`
}

type hostedQueryResponse struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Sources       map[string]model.Source `json:"sources,omitempty"`
	Error         string                  `json:"error,omitempty"`
}

func startHostedQueryBridge(ctx context.Context, execute QueryExecutor) (*hostedQueryBridge, error) {
	directory, err := os.MkdirTemp("", "cao-value-query-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "query.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(directory)
		return nil, err
	}
	bridgeCtx, cancel := context.WithCancel(ctx)
	bridge := &hostedQueryBridge{
		path: path, listener: listener, cancel: cancel, done: make(chan struct{}),
	}
	go bridge.serve(bridgeCtx, execute)
	return bridge, nil
}

func (b *hostedQueryBridge) Close() {
	b.cancel()
	_ = b.listener.Close()
	<-b.done
	_ = os.RemoveAll(filepath.Dir(b.path))
}

func (b *hostedQueryBridge) serve(ctx context.Context, execute QueryExecutor) {
	defer close(b.done)
	var queries int
	var mutex sync.Mutex
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			return
		}
		queries++
		if queries > hostedQueryLimit {
			_ = writeHostedQueryResponse(connection, hostedQueryResponse{
				SchemaVersion: 1, Error: "Hosted local Value exceeded its canonical query budget",
			})
			_ = connection.Close()
			continue
		}
		mutex.Lock()
		b.serveConnection(ctx, connection, execute)
		mutex.Unlock()
	}
}

func (b *hostedQueryBridge) serveConnection(
	ctx context.Context,
	connection net.Conn,
	execute QueryExecutor,
) {
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	content, err := io.ReadAll(io.LimitReader(connection, hostedQueryRequestBytes+1))
	if err != nil || len(content) > hostedQueryRequestBytes {
		_ = writeHostedQueryResponse(connection, hostedQueryResponse{
			SchemaVersion: 1, Error: "Hosted local Value exceeded its canonical query byte budget",
		})
		return
	}
	decoder := json.NewDecoder(bufio.NewReader(strings.NewReader(string(content))))
	decoder.DisallowUnknownFields()
	var request hostedQueryRequest
	if err := decoder.Decode(&request); err != nil || request.SchemaVersion != 1 ||
		len(request.Queries) == 0 || len(request.Queries) > query.MaxPlanQueries ||
		len(request.Requested) == 0 || len(request.Requested) > query.MaxPlanQueries {
		_ = writeHostedQueryResponse(connection, hostedQueryResponse{
			SchemaVersion: 1, Error: "Hosted local Value sent an invalid canonical query",
		})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		_ = writeHostedQueryResponse(connection, hostedQueryResponse{
			SchemaVersion: 1, Error: "Hosted local Value sent multiple canonical queries",
		})
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sources, err := execute(queryCtx, request.Queries, request.Requested)
	response := hostedQueryResponse{SchemaVersion: 1, Sources: sources}
	if err != nil {
		response.Sources = nil
		response.Error = fmt.Sprintf("Canonical PostgreSQL query failed: %v", err)
	}
	_ = writeHostedQueryResponse(connection, response)
}

func writeHostedQueryResponse(connection net.Conn, response hostedQueryResponse) error {
	content, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(content) > hostedQueryResponseBytes {
		content, _ = json.Marshal(hostedQueryResponse{
			SchemaVersion: 1, Error: "Canonical PostgreSQL query response exceeded its byte budget",
		})
	}
	content = append(content, '\n')
	_, err = connection.Write(content)
	return err
}
