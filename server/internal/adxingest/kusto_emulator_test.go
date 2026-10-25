package adxingest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

type kustoAcceptance struct {
	engine *simulator.Kusto
	client *Client
	mu     sync.Mutex
	fail   string
	hold   string
}

func newKustoAcceptance(t *testing.T) *kustoAcceptance {
	t.Helper()
	endpoint := os.Getenv("CAO_KUSTO_EMULATOR_URL")
	if endpoint == "" {
		t.Skip("requires Microsoft's real Kusto emulator; run scripts/test-kusto-emulator.sh")
	}
	database := "CAOAcceptance" + rand.Text()
	tables := make([]string, 0, len(families)+1+len(adxtrace.Tables))
	for _, family := range families {
		tables = append(tables, family.table)
	}
	tables = append(tables, "SessionVersions")
	for table := range adxtrace.Tables {
		tables = append(tables, table)
	}
	engine, err := simulator.NewKusto(endpoint, database, tables)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	k := &kustoAcceptance{engine: engine}
	k.execute(t, "/v1/rest/mgmt", ".create database "+database+
		` persist (@"/kustodata/`+database+`/md", @"/kustodata/`+database+`/data")`)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer cancel()
		if _, err := k.engine.Execute(ctx, "/v1/rest/mgmt", ".drop database "+database); err != nil {
			t.Errorf("drop synthetic database: %v", err)
		}
	})
	k.apply(t, Schema())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		table := strings.TrimPrefix(r.URL.Path, "/v1/rest/ingest/"+database+"/")
		if table == k.hold {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
			return // Simulated visibility lag; verification still queries Kusto.
		}
		if table == k.fail {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, r)
			if response.Code != http.StatusOK {
				t.Errorf("fault injection did not commit to Kusto: %s", response.Body.String())
				http.Error(w, response.Body.String(), response.Code)
				return
			}
			k.fail = ""
			http.Error(w, "synthetic lost acknowledgement after real ingestion", http.StatusServiceUnavailable)
			return
		}
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	transport, err := simulator.NewLocalTransport(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	// Test-only construction: production NewClient still rejects local HTTP
	// and still requires the real Entra credential and HTTPS endpoint contract.
	k.client = &Client{endpoint: server.URL, database: database, storageBudget: 1 << 30,
		http: &http.Client{Transport: transport, CheckRedirect: noRedirect, Timeout: 30 * time.Second}}
	return k
}

func (k *kustoAcceptance) execute(t *testing.T, path, query string) []byte {
	t.Helper()
	content, err := k.engine.Execute(t.Context(), path, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	var response struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(content, &response); err != nil || len(response.Error) > 0 {
		t.Fatalf("Kusto rejected %s: %s", query, content)
	}
	return content
}

func (k *kustoAcceptance) apply(t *testing.T, schema string) {
	t.Helper()
	var commands []string
	for _, line := range strings.Split(schema, "\n") {
		if strings.HasPrefix(line, "//") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, ".") {
			commands = append(commands, line)
		} else {
			if len(commands) == 0 {
				t.Fatal("generated schema does not start with a management command")
			}
			commands[len(commands)-1] += "\n" + line
		}
	}
	for _, command := range commands {
		if strings.Contains(command, " policy streamingingestion ") {
			continue // Documented emulator limitation; not engine validation.
		}
		k.execute(t, "/v1/rest/mgmt", strings.TrimSuffix(command, ";"))
	}
}

func (k *kustoAcceptance) query(t *testing.T, query string) [][]any {
	t.Helper()
	rows, err := k.client.query(t.Context(), "/v1/rest/query", query)
	if err != nil {
		// Preserve the engine's diagnostic for functional acceptance failures.
		content := k.execute(t, "/v1/rest/query", query)
		t.Fatalf("production query decoder: %v; engine response: %s", err, content)
	}
	return rows
}

func (k *kustoAcceptance) assert(t *testing.T, query string, expected [][]any) {
	t.Helper()
	got := k.query(t, query)
	actualJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("%s\nactual: %s\nexpected: %s", query, actualJSON, expectedJSON)
	}
}

func (k *kustoAcceptance) loseAcknowledgement(table string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.fail = table
}

func (k *kustoAcceptance) holdIngestion(table string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hold = table
}

func TestKustoEmulatorRejectsPartialResults(t *testing.T) {
	k := newKustoAcceptance(t)
	if _, err := k.client.query(t.Context(), "/v1/rest/query",
		"set truncationmaxrecords=1; range x from 1 to 3 step 1"); err == nil {
		t.Fatal("production decoder accepted an actual Kusto partial-result response")
	}
}

func TestKustoEmulatorScalarSessions(t *testing.T) {
	k := newKustoAcceptance(t)
	reader := &snapshotReader{session: testSession()}
	worker, services := testWorker(t, reader, k.client)
	k.loseAcknowledgement("SessionFacts")
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("ambiguous acknowledgement did not fail")
	}
	k.assert(t, "cao_SessionFacts() | count", [][]any{{0}})
	restarted, err := NewWorker(testConfig(), reader, k.client, services)
	if err != nil {
		t.Fatal(err)
	}
	step(t, restarted)
	k.assert(t, "SessionFacts | count", [][]any{{2}})
	k.assert(t, `cao_SessionFacts() | project InputTokens, isnull(OutputTokens),
        array_index_of(PresentFields, "OutputTokens") >= 0, array_index_of(NullFields, "OutputTokens") >= 0,
        array_index_of(PresentFields, "ReasoningTokens") == -1, Completeness`,
		[][]any{{0, true, true, true, true, "partial"}})
	k.assert(t, "cao_ToolEvents() | project Type, Name", [][]any{{"tool_call", "read"}})
	k.assert(t, "cao_FirewallEvents() | count", [][]any{{0}})
	k.assert(t, "cao_GraderObservations() | count", [][]any{{0}})
	k.assert(t, "cao_EvalObservations() | count", [][]any{{0}})
	step(t, restarted)
	reader.session.Revision, reader.session.Run["inputTokens"] = 2, int64(7)
	step(t, restarted)
	k.assert(t, "cao_SessionFacts() | project InputTokens", [][]any{{7}})
	k.assert(t, "cao_ReadySessions() | project SourceRevision", [][]any{{2}})
	if readState(t, restarted).Pending != nil {
		t.Fatal("real engine verification did not finish the durable job")
	}
	t.Log("real engine scalar retry, missingness and immutable correction passed")
}
