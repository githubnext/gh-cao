package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c := Config{Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "private-archive",
		Repositories: []string{"octo/api"}, DuckDBPath: os.Getenv("DUCKDB_PATH"),
		Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"workflow.info": {"episode"}, "tool.execution_complete": {"output", "error", "success", "toolCallId"},
				"user.message": {"content"}, "assistant.reasoning": {"content"}}}}
	if err := os.Chmod(c.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func testProjection(t *testing.T, c Config, run, output string) Projection {
	t.Helper()
	events := []any{
		map[string]any{"type": "session.format", "data": map[string]any{"version": 1}},
		map[string]any{"type": "session.collection", "data": map[string]any{"complete": true}},
		map[string]any{"type": "workflow.info", "data": map[string]any{"episode": map[string]any{"episodeId": "episode", "hopId": "hop"}}},
		map[string]any{"id": "tool", "type": "tool.execution_complete", "timestamp": nil,
			"data": map[string]any{"output": map[string]any{"content": output, "array": []any{nil, true, false, 0, json.Number("9007199254740993"), 1.25, "λ"}, "token": "private"}, "error": nil, "success": false, "toolCallId": "call"}},
	}
	var source strings.Builder
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(encoded)
		source.WriteByte('\n')
	}
	p, err := Project(c.traceConfig(), c.Namespace, "octo/api", run, time.Now().UTC().Format(time.DateOnly), 1, []byte(source.String()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testConverter(t *testing.T) *Converter {
	t.Helper()
	path := os.Getenv("DUCKDB_PATH")
	if path == "" {
		path = "duckdb"
	}
	if _, err := exec.LookPath(path); err != nil && os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB") != "1" {
		t.Skip("real pinned DuckDB CLI unavailable; set CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1 for required acceptance")
	}
	converter, err := NewConverter(ConverterConfig{DuckDBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return converter
}

func TestDuckDBConverterIntegration(t *testing.T) {
	converter := testConverter(t)
	c := testConfig(t)
	p := testProjection(t, c, "123", strings.Repeat("large approved content ' λ\n", 3000))
	q := testProjection(t, c, "456", "second full session")
	path := filepath.Join(c.Directory, "packed.parquet")
	file, err := converter.Write(t.Context(), path, []Projection{p, q})
	if err != nil {
		t.Fatal(err)
	}
	if file.Rows != 8 || len(file.Members) != 2 {
		t.Fatalf("not packed: %#v", file)
	}
	var actual []Event
	if err := converter.query(t.Context(), "SELECT "+eventSelect+" FROM read_parquet("+literal(path)+") ORDER BY RunId,Sequence", &actual); err != nil {
		t.Fatal(err)
	}
	want := append(append([]Event{}, p.Events...), q.Events...)
	if !reflect.DeepEqual(want, actual) {
		t.Fatal("complete approved payload, JSON number/null/order, or provenance changed")
	}
	if !strings.Contains(actual[3].PayloadJSON, `"9007199254740993"`) && !strings.Contains(actual[3].PayloadJSON, `9007199254740993`) {
		t.Fatal("integer precision lost")
	}
	if strings.Contains(actual[3].PayloadJSON, `"token":"private"`) || !strings.Contains(actual[3].PayloadJSON, `"error":null`) {
		t.Fatal("redaction or null semantics lost")
	}
	if _, err := converter.Write(t.Context(), path, []Projection{p}); err == nil {
		t.Fatal("immutable file overwritten")
	}
	bad := file
	bad.Members = bad.Members[:1]
	if !errors.Is(converter.Verify(t.Context(), path, bad), ErrIntegrity) {
		t.Fatal("partial membership accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := converter.Verify(ctx, path, file); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 1
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(converter.Verify(t.Context(), path, file), ErrIntegrity) {
		t.Fatal("corrupt immutable file accepted")
	}
}

func TestArchiveApprovalAndConfiguration(t *testing.T) {
	c := testConfig(t)
	p := testProjection(t, c, "123", "approved")
	if err := validateProjection(p); err != nil {
		t.Fatal(err)
	}
	c.Policy.ContentApproved = false
	if _, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, []byte(`{}`), time.Now()); err == nil {
		t.Fatal("unapproved content accepted")
	}
	if config, err := FromEnv(func(string) string { return "" }); err != nil || config.Enabled {
		t.Fatal("archive is not independently default off")
	}
	if _, err := FromEnv(func(name string) string {
		if name == "CAO_SESSION_ARCHIVE_ENABLED" {
			return "true"
		}
		if name == "CAO_SESSION_ARCHIVE_MODE" {
			return "sample"
		}
		return ""
	}); err == nil {
		t.Fatal("unimplemented sampled archive silently enabled")
	}
}
