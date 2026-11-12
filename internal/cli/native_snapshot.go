package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

var nativeCollections = []string{
	"repositories", "workflows", "runs", "domains", "tools", "skills", "friction", "audits", "issues",
	"operationalValues", "operationalValueEvidence", "workflowValueCollections",
	"experiments", "experimentAssignments", "graders", "graderObservations", "evals", "evalObservations", "transactions",
}

func nativeString(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func nativeBool(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

func nativeStrings(cmd *cobra.Command, name string) []string {
	value, _ := cmd.Flags().GetStringArray(name)
	return value
}

func nativeSnapshotPath(cmd *cobra.Command) string {
	if value := nativeString(cmd, "postgres-url"); value != "" {
		return value
	}
	return os.Getenv("CAO_POSTGRES_URL")
}

type nativeCanonicalSession struct {
	mu      sync.Mutex
	child   *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	encoder *json.Encoder
	decoder *json.Decoder
	cleanup func()
}

func openNativeSnapshot(dsn string) (*nativeCanonicalSession, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nativeErrorf("PostgreSQL is required; pass --postgres-url or set CAO_POSTGRES_URL")
	}
	server, cleanup, err := nativeDashboardExecutable()
	if err != nil {
		return nil, err
	}
	child := exec.Command(server, "read")
	child.Env = append(dashboardServerEnvironment(), "CAO_POSTGRES_URL="+dsn)
	if namespace := os.Getenv("CAO_DATABASE_NAMESPACE"); namespace != "" {
		child.Args = append(child.Args, "--redis-namespace", namespace)
	}
	input, err := child.StdinPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	output, err := child.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cleanup()
		return nil, err
	}
	if err := child.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		cleanup()
		return nil, fmt.Errorf("start canonical PostgreSQL reader: %w", err)
	}
	return &nativeCanonicalSession{child: child, input: input, output: output,
		encoder: json.NewEncoder(input), decoder: json.NewDecoder(output), cleanup: cleanup}, nil
}

func (db *nativeCanonicalSession) ReadCanonical(ctx context.Context, request dashboardquery.CanonicalReadRequest) (dashboardquery.CanonicalReadResult, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return dashboardquery.CanonicalReadResult{}, err
	}
	if err := db.encoder.Encode(request); err != nil {
		return dashboardquery.CanonicalReadResult{}, fmt.Errorf("write canonical read request: %w", err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = db.child.Process.Kill()
		case <-done:
		}
	}()
	defer close(done)
	var result dashboardquery.CanonicalReadResult
	if err := db.decoder.Decode(&result); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("read canonical PostgreSQL response: %w", err)
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func (db *nativeCanonicalSession) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	defer db.cleanup()
	inputErr := db.input.Close()
	waitErr := db.child.Wait()
	return errors.Join(inputErr, waitErr)
}

func readNativeCollection(ctx context.Context, db dashboardquery.CanonicalReader, collection string) (result []map[string]any, err error) {
	if !slices.Contains(nativeCollections, collection) && collection != "campaigns" && collection != "marketplacePackages" {
		return nil, nativeErrorf("Unknown collection: %s", collection)
	}
	return dashboardquery.ReadCollection(ctx, db, collection, 0, 0)
}

func requireNativeSnapshot(cmd *cobra.Command, named bool) error {
	if strings.TrimSpace(nativeSnapshotPath(cmd)) == "" {
		return nativeErrorf("PostgreSQL is required; ingest canonical artifacts with cao-dashboard ingest, then pass --postgres-url or set CAO_POSTGRES_URL")
	}
	return nil
}

func nativeLimit(cmd *cobra.Command, fallback int) (int, error) {
	value := nativeString(cmd, "limit")
	if value == "" && !cmd.Flags().Changed("limit") {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("--limit must be a positive integer")
	}
	return number, nil
}

func nativeField(record map[string]any, field string) any {
	value, _ := nativeFieldLookup(record, field)
	return value
}

func nativeFieldLookup(record map[string]any, field string) (any, bool) {
	var value any = record
	for _, part := range strings.Split(field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func nativeScalar(value any) string {
	return dashboardquery.String(value)
}
