package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func archiveDefinition(t *testing.T, clauses string) sessionarchive.Definition {
	t.Helper()
	var definition sessionarchive.Definition
	content := `{"name":"test-events","subject":"Inspect approved retained events.","from":"archive-events",` + clauses + `}`
	if err := json.Unmarshal([]byte(content), &definition); err != nil {
		t.Fatal(err)
	}
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestHostedAnalyticalAcceptanceIsolation(t *testing.T) {
	n, protocol := nativeProtocol(t)
	before := n.config.NativeAcceptanceBinding()
	if n.AnalyticalAvailable() {
		t.Fatal("existing capture acceptance implicitly enabled analytical queries")
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	definition := archiveDefinition(t, `"select":[{"field":"Sequence"}]`)
	if _, _, reader, err := n.ReadAnalytical(t.Context(), scope, "octo/api", definition); !errors.Is(err, ErrConformance) || reader != nil {
		t.Fatalf("unaccepted analytical plan reached query execution: %v", err)
	}
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 1, protocol.now.Add(-time.Hour), nativeSource("approved"), ""); err != nil {
		t.Fatalf("default-off analytics broke existing accepted capture: %v", err)
	}
	n.config.AnalyticalAcceptance = n.config.AnalyticalAcceptanceBinding()
	if !n.AnalyticalAvailable() || n.config.NativeAcceptanceBinding() != before || n.Status().ActiveTraces != 1 {
		t.Fatal("analytical acceptance changed native privacy identity or capture state")
	}
}

func TestHostedAnalyticalProtocol(t *testing.T) {
	for _, outcome := range []string{"complete", "limited", "over-budget", "fractional", "extra-field", "wrong-schema", "wrong-gaps", "query-failure", "withdrawal"} {
		t.Run(outcome, func(t *testing.T) {
			a, protocol, _ := newProtocol(t)
			a.config.AnalyticalAcceptance = a.config.AnalyticalAcceptanceBinding()
			manifest := fixture(t, 1, "selected")
			if err := a.Publish(t.Context(), manifest); err != nil {
				t.Fatal(err)
			}
			definition := archiveDefinition(t, `"select":[{"field":"RunAttempt","as":"attempt"},{"field":"Type"}],"limit":1`)
			withdrawn := make(chan struct{})
			protocol.query = func(w http.ResponseWriter, _ *http.Request, kql string) {
				for _, required := range []string{"ArchiveObject in", "join kind=inner", "let source=materialize", "nulls last", "| take 2", "make_list(bag_pack", "array_length(Gaps)", `| extend ["__archive_empty_0"]=iff(isempty(`} {
					if !strings.Contains(kql, required) {
						t.Errorf("missing trusted KQL stage %q", required)
					}
				}
				if strings.Contains(kql, "PayloadJSON") || strings.Contains(kql, "dcount(") {
					t.Error("analytical query reads payload or approximates exact counts")
				}
				rows := []map[string]any{{"attempt": int64(9007199254740993), "Type": "user.message"}}
				gaps := 0
				columns := []string{"rows", "tracesWithGaps"}
				switch outcome {
				case "limited":
					rows = append(rows, rows[0])
				case "over-budget":
					rows = append(rows, rows[0], rows[0])
				case "fractional":
					rows[0]["attempt"] = 1.5
				case "extra-field":
					rows[0]["PayloadJSON"] = "must not escape"
				case "wrong-schema":
					columns[0] = "unrecognized"
				case "wrong-gaps":
					gaps = 2
				case "query-failure":
					http.Error(w, "synthetic query failure", http.StatusServiceUnavailable)
					return
				case "withdrawal":
					defer close(withdrawn)
					if err := a.WithdrawRepository(t.Context(), "octo/api"); err != nil {
						t.Error(err)
					}
				}
				respond(w, columns, [][]any{{rows, gaps}}, true)
			}
			scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
			_, result, reader, err := a.ReadAnalytical(t.Context(), scope, "octo/api", definition)
			if outcome == "withdrawal" {
				select {
				case <-withdrawn:
				case <-time.After(5 * time.Second):
					t.Fatal("in-flight withdrawal did not finish")
				}
			}
			if reader != nil {
				t.Cleanup(func() { _ = reader.Close() })
			}
			if outcome != "complete" && outcome != "limited" {
				if err == nil || reader != nil || a.reader.Load() != nil {
					t.Fatalf("invalid or revoked result escaped/leaked reader: %#v %v", result, err)
				}
				return
			}
			if err != nil || len(result.Rows) != 1 || result.Rows[0]["attempt"] != json.Number("9007199254740993") ||
				result.Metadata.Limited != (outcome == "limited") || result.Metadata.UpstreamCoverage != "unknown" {
				t.Fatalf("analytical envelope lost precision or selection metadata: %#v %v", result, err)
			}
			if a.reader.Load() == nil || reader == nil {
				t.Fatal("analytical reader released before final delivery")
			}
			if err := a.WithdrawRepository(t.Context(), "octo/api"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(reader.Deliver(func() error { t.Fatal("withdrawn data delivered"); return nil }), ErrWithdrawn) {
				t.Fatal("analytical results bypassed withdrawal at delivery")
			}
		})
	}
}

func TestNativeAnalyticalSourceFence(t *testing.T) {
	n, protocol := nativeProtocol(t)
	n.config.AnalyticalAcceptance = n.config.AnalyticalAcceptanceBinding()
	if err := n.RequireSourceFencing(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	if err := n.CaptureSessionAt(t.Context(), source, "123", 1, protocol.now.Add(-time.Hour), nativeSource("approved"), ""); err != nil {
		t.Fatal(err)
	}
	if err := n.CaptureSessionAt(t.Context(), source, "456", 1, protocol.now.Add(-time.Hour), nil, "missing-session"); err == nil {
		t.Fatal("synthetic admission gap was not explicit")
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	definition := archiveDefinition(t, `"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`)
	if _, _, reader, err := n.ReadAnalytical(t.Context(), scope, "octo/api", definition); !errors.Is(err, ErrScope) || reader != nil {
		t.Fatalf("unscoped analytical query bypassed source fencing: %v", err)
	}
	protocol.query = func(w http.ResponseWriter, _ *http.Request, kql string) {
		if !strings.Contains(kql, literal(n.state.Manifest.Members[0].Version)) {
			t.Error("query omitted source-tagged exact membership")
		}
		respond(w, []string{"rows", "tracesWithGaps"}, [][]any{{[]map[string]any{{"events": 3}}, 0}}, true)
	}
	_, result, reader, err := n.ReadAnalyticalAt(t.Context(), scope, source, definition)
	if err != nil || reader == nil || result.Metadata.AdmissionGaps != 1 {
		t.Fatalf("native analytical query lost source-scoped admission gaps: %#v %v", result, err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := n.WithdrawSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(reader.Deliver(func() error { t.Fatal("old epoch delivered"); return nil }), ErrWithdrawn) {
		t.Fatal("source withdrawal did not revoke analytical delivery")
	}
	_ = reader.Close()
	if _, _, reader, err := n.ReadAnalyticalAt(t.Context(), scope, source, definition); !errors.Is(err, ErrWithdrawn) || reader != nil {
		t.Fatalf("withdrawn source remained analytically queryable: %v", err)
	}
}

func TestHostedAnalyticalBudgetsAndEmptyArchive(t *testing.T) {
	a, protocol, _ := newProtocol(t)
	a.config.AnalyticalAcceptance = a.config.AnalyticalAcceptanceBinding()
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	definition := archiveDefinition(t, `"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`)
	protocol.query = func(w http.ResponseWriter, _ *http.Request, kql string) {
		if strings.Contains(kql, "external_table") || !strings.Contains(kql, "datatable(") {
			t.Error("empty archive scanned external data")
		}
		respond(w, []string{"rows", "tracesWithGaps"}, [][]any{{[]map[string]any{{"events": 0}}, 0}}, true)
	}
	_, result, reader, err := a.ReadAnalytical(t.Context(), scope, "octo/api", definition)
	if err != nil || len(result.Rows) != 1 || result.Rows[0]["events"] != json.Number("0") {
		t.Fatalf("empty archive did not preserve count grain: %#v %v", result, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	definition.Limit = new(4097)
	if _, _, reader, err := a.ReadAnalytical(t.Context(), scope, "octo/api", definition); !errors.Is(err, sessionarchive.ErrLimit) || reader != nil {
		t.Fatalf("unsupported explicit output bound was silently clamped: %v", err)
	}
	definition.Limit = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, reader, err := a.ReadAnalytical(ctx, scope, "octo/api", definition); !errors.Is(err, context.Canceled) || reader != nil {
		t.Fatalf("canceled analytical read was admitted: %v", err)
	}
	definition = archiveDefinition(t, `"select":[{"field":"Sequence"}]`)
	protocol.query = func(w http.ResponseWriter, _ *http.Request, _ string) {
		rows := make([]map[string]any, 4097)
		for i := range rows {
			rows[i] = map[string]any{"Sequence": i}
		}
		respond(w, []string{"rows", "tracesWithGaps"}, [][]any{{rows, 0}}, true)
	}
	if _, _, reader, err := a.ReadAnalytical(t.Context(), scope, "octo/api", definition); !errors.Is(err, sessionarchive.ErrLimit) || reader != nil || a.reader.Load() != nil {
		t.Fatalf("complete-result overflow was silently truncated or leaked: %v", err)
	}
}

func TestKustoAnalyticalCorpus(t *testing.T) {
	engine := kustoArchiveEngine(t)
	content, err := os.ReadFile("../sessionarchive/testdata/analytical.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Sessions []struct {
			RunID    string   `json:"runId"`
			Attempt  int64    `json:"attempt"`
			EventIDs []string `json:"eventIds"`
		}
		Cases []struct {
			Name    string
			Clauses json.RawMessage
			Rows    []map[string]any
			Limited bool
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	if err := decoder.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var events []sessionarchive.Event
	manifest := fixture(t, 1, "corpus")
	manifest.Members = nil
	for _, session := range corpus.Sessions {
		p := approvedProjectionAt(t, "private", "octo/api", session.RunID, "approved", session.Attempt)
		for i := range p.Events {
			p.Events[i].EventID = session.EventIDs[i]
			p.Events[i].Gaps = []string{"synthetic-omission"}
		}
		events = append(events, p.Events...)
		manifest.Members = append(manifest.Members, p.Member())
	}
	manifest.Files[0].Members = manifest.Members
	manifest.Files[0].Day = "2026-10-25"
	membership := membershipKQL("Events", manifest.Files, manifest.Members)
	prefix, source, _ := strings.Cut(membership, ";\n")
	source = strings.Replace(source, `external_table("Events")`, kustoEventSource(events, manifest.Files[0]), 1)
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			definition := archiveDefinition(t, strings.TrimSuffix(strings.TrimPrefix(string(test.Clauses), "{"), "}"))
			plan, err := definition.Plan()
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Require(sessionarchive.AnalyticalCapabilities(4096)); err != nil {
				t.Fatal(err)
			}
			content, err := engine.Execute(t.Context(), "/v1/rest/query", prefix+";\n"+analyticalKQL(plan, source))
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Tables []queryTable }
			if json.Unmarshal(content, &response) != nil || len(response.Tables) == 0 ||
				len(response.Tables[0].Rows) != 1 || len(response.Tables[0].Rows[0]) != 2 {
				t.Fatal("real Kusto did not return the bounded analytical envelope")
			}
			var rows []map[string]any
			decoder := json.NewDecoder(strings.NewReader(string(response.Tables[0].Rows[0][0])))
			decoder.UseNumber()
			if err := decoder.Decode(&rows); err != nil {
				t.Fatal(err)
			}
			limited := len(rows) > plan.Limit
			if limited {
				rows = rows[:plan.Limit]
			}
			if !reflect.DeepEqual(rows, test.Rows) || limited != test.Limited || string(response.Tables[0].Rows[0][1]) != "2" {
				t.Fatalf("real Kusto differs from shared DuckDB corpus: %#v; expected %#v", rows, test.Rows)
			}
		})
	}
	for _, clauses := range []string{
		`"select":[{"field":"Sequence"}]`,
		`"aggregate":{"by":["Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`,
		`"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`,
	} {
		definition := archiveDefinition(t, clauses)
		plan, err := definition.Plan()
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Require(sessionarchive.AnalyticalCapabilities(4096)); err != nil {
			t.Fatal(err)
		}
		content, err := engine.Execute(t.Context(), "/v1/rest/query", analyticalKQL(plan, "datatable("+EventSchema()+")[]\n"))
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Tables []queryTable }
		if json.Unmarshal(content, &response) != nil || len(response.Tables) == 0 ||
			len(response.Tables[0].Rows) != 1 || len(response.Tables[0].Rows[0]) != 2 {
			t.Fatal("real Kusto rejected the typed empty archive")
		}
		expected := "[]"
		if len(plan.Values) > 0 && len(plan.By) == 0 {
			expected = `[{"events":0}]`
		}
		var compact bytes.Buffer
		if json.Compact(&compact, response.Tables[0].Rows[0][0]) != nil ||
			compact.String() != expected || string(response.Tables[0].Rows[0][1]) != "0" {
			t.Fatal("real Kusto fabricated empty archive rows or coverage")
		}
	}
}
