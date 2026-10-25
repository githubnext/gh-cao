package adxingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/githubnext/gh-cao/server/internal/model"
)

// This is a protocol simulator, not a Kusto engine. It accepts only the
// production ingestion/verification/size requests and preserves duplicates.
type adxSimulator struct {
	mu            sync.Mutex
	server        *httptest.Server
	tables        map[string][]model.Row
	failTable     string
	hideTable     string
	partial       bool
	bytes         int64
	requests      int
	manifestEarly bool
}

var verificationPart = regexp.MustCompile(`\((\w+) \| where Namespace==("(?:\\.|[^"\\])*") and RunId==("(?:\\.|[^"\\])*") and ProjectionId==("(?:\\.|[^"\\])*")(?: and SourceRevision==(\d+))? \| project Table="(\w+)", RowDigest\)`)

func newSimulator(t *testing.T) (*adxSimulator, *Client) {
	t.Helper()
	sim := &adxSimulator{tables: map[string][]model.Row{}}
	sim.server = httptest.NewServer(http.HandlerFunc(sim.handle))
	t.Cleanup(sim.server.Close)
	client := sim.server.Client()
	client.CheckRedirect = noRedirect
	client.Transport = &oauth2.Transport{
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "simulator-only"}),
		Base:   client.Transport,
	}
	return sim, &Client{endpoint: sim.server.URL, database: "CAO", http: client, storageBudget: 50_000_000_000}
}

func (s *adxSimulator) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer simulator-only" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/v1/rest/ingest/CAO/") {
		table := strings.TrimPrefix(r.URL.Path, "/v1/rest/ingest/CAO/")
		if !validTable(table) || r.URL.Query().Get("streamFormat") != "MultiJSON" || r.URL.Query().Get("mappingName") != "cao_v1" {
			http.Error(w, "bad mapping", http.StatusBadRequest)
			return
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.UseNumber()
		for {
			row := model.Row{}
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, "bad row", http.StatusBadRequest)
				return
			}
			if table == "SessionVersions" {
				if len(s.tables["SessionFacts"]) == 0 {
					s.manifestEarly = true
				}
			}
			s.tables[table] = append(s.tables[table], row)
		}
		if table == s.failTable {
			s.failTable = ""
			// Simulate an ambiguous response after a committed ingestion.
			http.Error(w, "retry synthetic", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct{ DB, CSL string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DB != "CAO" {
		http.Error(w, "bad query", http.StatusBadRequest)
		return
	}
	var rows [][]any
	switch {
	case r.URL.Path == "/v1/rest/mgmt" && strings.HasPrefix(body.CSL, ".show database ['CAO'] extents"):
		rows = [][]any{{s.bytes}}
	case r.URL.Path == "/v1/rest/query":
		parts := verificationPart.FindAllStringSubmatch(body.CSL, -1)
		if len(parts) == 0 || !strings.HasSuffix(body.CSL, "| summarize by Table, RowDigest") {
			http.Error(w, "unsupported query", http.StatusBadRequest)
			return
		}
		seen := map[string]bool{}
		for _, part := range parts {
			if part[1] == s.hideTable || part[1] != part[6] {
				continue
			}
			var namespace, runID, projectionID string
			if json.Unmarshal([]byte(part[2]), &namespace) != nil || json.Unmarshal([]byte(part[3]), &runID) != nil ||
				json.Unmarshal([]byte(part[4]), &projectionID) != nil {
				http.Error(w, "invalid literal", http.StatusBadRequest)
				return
			}
			for _, row := range s.tables[part[1]] {
				if row["Namespace"] != namespace || row["RunId"] != runID || row["ProjectionId"] != projectionID ||
					part[5] != "" && fmt.Sprint(row["SourceRevision"]) != part[5] {
					continue
				}
				digest, _ := row["RowDigest"].(string)
				key := part[1] + ":" + digest
				if !seen[key] {
					rows = append(rows, []any{part[1], digest})
					seen[key] = true
				}
			}
		}
	default:
		http.Error(w, "unsupported request", http.StatusBadRequest)
		return
	}
	severity := 4
	if s.partial {
		severity = 2
	}
	response := map[string]any{"Tables": []any{
		map[string]any{"TableName": "Table_0", "Rows": rows},
		map[string]any{"TableName": "QueryStatus", "Columns": []any{map[string]string{"ColumnName": "Severity"}},
			"Rows": [][]any{{severity}}},
	}}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		panic(err)
	}
}

func (s *adxSimulator) latestRevision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest int64
	for _, row := range s.tables["SessionVersions"] {
		revision, _ := strconv.ParseInt(fmt.Sprint(row["SourceRevision"]), 10, 64)
		latest = max(latest, revision)
	}
	return latest
}
