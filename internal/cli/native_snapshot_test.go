package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func nativeTestSnapshot(t *testing.T, collections map[string][]map[string]any) string {
	t.Helper()
	endpoint := os.Getenv("POSTGRES_URL")
	if endpoint == "" {
		t.Skip("POSTGRES_URL is required for native PostgreSQL query tests")
	}
	executable := os.Getenv("CAO_DASHBOARD_EXECUTABLE")
	if executable == "" {
		executable, _ = filepath.Abs("../../.tmp/cao-dashboard")
		if _, err := os.Stat(executable); err != nil {
			t.Skip("build .tmp/cao-dashboard to run native PostgreSQL query tests")
		}
		t.Setenv("CAO_DASHBOARD_EXECUTABLE", executable)
	}
	schema := "native_" + strings.ToLower(rand.Text())
	command := exec.CommandContext(t.Context(), "psql", "-X", "--dbname", endpoint, "-v", "ON_ERROR_STOP=1",
		"-c", "CREATE SCHEMA "+schema)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create isolated PostgreSQL schema: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		command := exec.CommandContext(context.WithoutCancel(t.Context()), "psql", "-X", "--dbname", endpoint,
			"-v", "ON_ERROR_STOP=1", "-c", "DROP SCHEMA "+schema+" CASCADE")
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("remove isolated PostgreSQL schema: %v\n%s", err, output)
		}
	})
	connection, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	parameters := connection.Query()
	parameters.Set("search_path", schema)
	connection.RawQuery = parameters.Encode()
	dsn := connection.String()
	namespace := "native-test"
	t.Setenv("CAO_DATABASE_NAMESPACE", namespace)
	command = exec.CommandContext(t.Context(), executable, "database", "bootstrap", "--store", "canonical", "--namespace", namespace)
	command.Env = append(os.Environ(), "CAO_DATABASE_ADMIN_URL="+dsn, "OTEL_SDK_DISABLED=true")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap canonical PostgreSQL: %v\n%s", err, output)
	}
	directory := t.TempDir()
	if err := publishDashboardSnapshot(t.Context(), directory, collections); err != nil {
		t.Fatal(err)
	}
	command = exec.CommandContext(t.Context(), executable, "ingest", "--source", directory, "--postgres-url", dsn,
		"--redis-namespace", namespace, "--database-queries", "../../dashboard/site/src/data/queries/database.json")
	command.Env = append(os.Environ(), "OTEL_SDK_DISABLED=true")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ingest canonical PostgreSQL: %v\n%s", err, output)
	}
	return dsn
}

func TestNativePostgresCanonicalRead(t *testing.T) {
	dsn := nativeTestSnapshot(t, map[string][]map[string]any{
		"repositories": {{"id": "octo/one", "owner": "octo", "name": "one"}},
	})
	db, err := openNativeSnapshot(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := readNativeCollection(t.Context(), db, "repositories")
	if err != nil || len(rows) != 1 || rows[0]["name"] != "one" {
		t.Fatalf("canonical PostgreSQL read: %v %v", rows, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readNativeCollection(ctx, db, "repositories"); err == nil {
		t.Fatal("canceled PostgreSQL read succeeded")
	}
}

func TestNativePostgresRequired(t *testing.T) {
	if _, err := openNativeSnapshot(""); err == nil || !strings.Contains(fmt.Sprint(err), "PostgreSQL is required") {
		t.Fatalf("missing PostgreSQL configuration: %v", err)
	}
}

func TestNativePostgresDailyCounts(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	dsn := nativeTestSnapshot(t, map[string][]map[string]any{
		"repositories": {{"id": "repo:one", "owner": "octo", "name": "one"}},
		"workflows":    {{"id": "workflow:one", "repositoryId": "repo:one", "path": ".github/workflows/test.md"}},
		"runs": {{"id": "run:one", "repositoryId": "repo:one", "workflowId": "workflow:one",
			"owner": "octo", "repository": "one", "githubRunId": "1", "attempt": 1,
			"workflowPath": ".github/workflows/test.md",
			"startedAt":    now.Format(time.RFC3339), "status": "completed", "conclusion": "success"}},
	})
	db, err := openNativeSnapshot(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, available, undated, err := dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
	if err != nil || !available || undated != 0 || len(rows) != 1 ||
		rows[0]["total"] != float64(1) || rows[0]["successful"] != float64(1) {
		t.Fatalf("native PostgreSQL daily counts: %+v available=%t undated=%d error=%v", rows, available, undated, err)
	}
	metadata, err := nativeDailyCountMetadata(t.Context(), db, undated)
	if err != nil || metadata["aggregate-coverage"] != "available-evidence-only" ||
		metadata["aggregate-time-grain"] != "utc-run-start-day" {
		t.Fatalf("native PostgreSQL aggregate provenance: %+v %v", metadata, err)
	}
}
