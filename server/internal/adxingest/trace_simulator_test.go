package adxingest

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestTraceHTTPProtocolSimulatorRestartAndExactVisibility(t *testing.T) {
	var mu sync.Mutex
	tables := map[string][]model.Row{}
	fail, hidden, early := true, true, false
	maxRequest := 0
	condition := regexp.MustCompile(`Namespace==("(?:[^"\\]|\\.)*") and RunId==("(?:[^"\\]|\\.)*") and ProjectionId==("(?:[^"\\]|\\.)*")`)
	host := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.URL.Path, "/ingest/") {
			table := filepath.Base(request.URL.Path)
			if _, ok := adxtrace.Tables[table]; !ok {
				t.Errorf("unapproved table %s", table)
				response.WriteHeader(400)
				return
			}
			scanner := bufio.NewScanner(request.Body)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			bytes := 0
			for scanner.Scan() {
				bytes += len(scanner.Bytes()) + 1
				var row model.Row
				if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
					t.Error(err)
					return
				}
				tables[table] = append(tables[table], row)
				if table == "TraceManifest" && hidden {
					early = true
				}
			}
			maxRequest = max(maxRequest, bytes)
			if bytes > 1<<20 {
				t.Error("oversized actual HTTP request")
			}
			if scanner.Err() != nil {
				t.Error(scanner.Err())
			}
			if fail && table == "TraceContentParts" {
				fail = false
				response.WriteHeader(503)
				return
			}
			_, _ = response.Write([]byte("{}"))
			return
		}
		var query struct {
			DB  string `json:"db"`
			CSL string `json:"csl"`
		}
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			t.Error(err)
			response.WriteHeader(400)
			return
		}
		if query.DB != "database" {
			t.Error("missing scoped database")
		}
		var rows [][]any
		if strings.HasPrefix(query.CSL, ".show database") {
			rows = [][]any{{0}}
		} else {
			for table, stored := range tables {
				index := strings.Index(query.CSL, "("+table+" | where")
				if index < 0 {
					continue
				}
				match := condition.FindStringSubmatch(query.CSL[index:])
				if len(match) != 4 {
					t.Errorf("unexpected verification query: %s", query.CSL)
					continue
				}
				var namespace, id, version string
				_ = json.Unmarshal([]byte(match[1]), &namespace)
				_ = json.Unmarshal([]byte(match[2]), &id)
				_ = json.Unmarshal([]byte(match[3]), &version)
				digests := []string{}
				for _, row := range stored {
					if row["Namespace"] == namespace && row["RunId"] == id && row["ProjectionId"] == version {
						digests = append(digests, row["RowDigest"].(string))
					}
				}
				slices.Sort(digests)
				digests = slices.Compact(digests)
				if hidden && table == "TraceContentParts" && len(digests) > 0 {
					digests = digests[1:]
				}
				sum := sha256.Sum256([]byte(strings.Join(digests, "")))
				rows = append(rows, []any{table, len(digests), hex.EncodeToString(sum[:])})
			}
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"Tables": []any{map[string]any{"TableName": "PrimaryResult", "Rows": rows}}})
	}))
	defer host.Close()
	client := &Client{endpoint: host.URL, database: "database", http: host.Client(), storageBudget: 1 << 30}
	c := adxtrace.Config{Enabled: true, Directory: t.TempDir(), DurableStorage: true, Repositories: []string{"octo/api"}, Mode: "full",
		Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"user.message": {"content"}}}}
	// #nosec G302 -- private directory needs owner traversal.
	if err := os.Chmod(c.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	worker, err := adxtrace.New(t.Context(), c, "namespace", client)
	if err != nil {
		t.Fatal(err)
	}
	logs, shards := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(logs, "run-42", "usage"), 0o700); err != nil {
		t.Fatal(err)
	}
	event, _ := json.Marshal(map[string]any{"type": "user.message", "data": map[string]any{"content": strings.Repeat("body", 500000)}})
	for path, content := range map[string][]byte{
		filepath.Join(logs, "run-42", "aw_info.json"):              []byte(`{"run_id":42,"run_attempt":1,"repository":"octo/api"}`),
		filepath.Join(logs, "run-42", "usage", "aw_session.jsonl"): event,
		filepath.Join(shards, "repo-1.jsonl"):                      []byte(`{"kind":"run","run":{"run_id":42,"run_attempt":1,"repository":"octo/api","status":"completed","updated_at":"2026-01-01T00:00:00Z"}}`),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := worker.Capture(t.Context(), "octo/api", logs, filepath.Join(shards, "repo-")); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("ambiguous HTTP ingestion must remain pending")
	}
	restarted, err := adxtrace.New(t.Context(), c, "namespace", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Step(t.Context()); err == nil {
		t.Fatal("hidden content must prevent readiness")
	}
	mu.Lock()
	hidden = false
	mu.Unlock()
	for i := 0; i < 10; i++ {
		more, err := restarted.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if early || len(tables["TraceManifest"]) == 0 || maxRequest > 1<<20 {
		t.Fatal("HTTP protocol readiness/budget violation")
	}
	if len(tables["TraceContentParts"]) < 3 {
		t.Fatal("did not exercise multipart retries")
	}
}
