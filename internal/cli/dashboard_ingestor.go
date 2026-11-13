package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// Calls are serialized by the owning collection loop.
type dashboardIngestor struct {
	child      *exec.Cmd
	input      io.WriteCloser
	output     io.ReadCloser
	decoder    *json.Decoder
	cancel     context.CancelFunc
	ctx        context.Context
	closed     bool
	runBundles bool
}

func startDashboardIngestor(ctx context.Context, server, site, cache, namespace, postgresURL string, diagnostics io.Writer) (*dashboardIngestor, error) {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	child := exec.CommandContext(ctx, server, "ingest", "--stdin", "--source", cache,
		"--redis-namespace", namespace,
		"--database-queries", filepath.Join(site, "src/data/queries/database.json"))
	child.Env = append(dashboardServerEnvironment(), "CAO_POSTGRES_URL="+postgresURL)
	child.Stderr = diagnostics
	child.WaitDelay = 5 * time.Second
	configureDashboardProcess(child)
	input, err := child.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := child.StdoutPipe()
	if err != nil {
		cancel()
		_ = input.Close()
		return nil, err
	}
	if err := child.Start(); err != nil {
		cancel()
		_ = input.Close()
		_ = output.Close()
		return nil, errors.New("canonical ingestion process could not start")
	}
	worker := &dashboardIngestor{
		child: child, input: input, output: output,
		decoder: json.NewDecoder(io.LimitReader(output, 256<<20)), cancel: cancel, ctx: parent,
	}
	var response struct {
		Type  string   `json:"type"`
		Modes []string `json:"modes"`
	}
	if err := worker.decoder.Decode(&response); err != nil || response.Type != "ready" {
		cancel()
		_ = worker.Close()
		return nil, errors.New("canonical ingestion process did not become ready; the server must support ingest --stdin")
	}
	worker.runBundles = slices.Contains(response.Modes, "run-bundles")
	return worker, nil
}

func (w *dashboardIngestor) Ingest() error {
	return w.ingest("snapshot")
}

func (w *dashboardIngestor) IngestRunBundles() error {
	if !w.runBundles {
		return errors.New("canonical ingestion process does not support Run-bundle publications; rebuild the server")
	}
	return w.ingest("run-bundles")
}

func (w *dashboardIngestor) ingest(mode string) error {
	if w.closed {
		return errors.New("canonical ingestion process is closed")
	}
	if err := json.NewEncoder(w.input).Encode(map[string]string{"type": "ingest", "mode": mode}); err != nil {
		return errors.New("canonical ingestion process is unavailable")
	}
	var response struct {
		Type   string          `json:"type"`
		Result json.RawMessage `json:"result"`
	}
	if err := w.decoder.Decode(&response); err != nil {
		return errors.New("canonical ingestion response was interrupted")
	}
	if response.Type != "complete" || len(response.Result) == 0 || string(response.Result) == "null" {
		return errors.New("dashboard PostgreSQL ingestion failed; previous canonical revision retained")
	}
	return nil
}

func (w *dashboardIngestor) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	_ = w.input.Close()
	stop := time.AfterFunc(5*time.Second, w.cancel)
	defer stop.Stop()
	err := w.child.Wait()
	_ = w.output.Close()
	canceled := w.ctx.Err() != nil
	w.cancel()
	if err != nil && !canceled {
		return errors.New("canonical ingestion process stopped unexpectedly")
	}
	return nil
}
