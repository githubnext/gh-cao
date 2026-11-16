package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
	"github.com/githubnext/gh-cao/server/internal/sessionarchiveblob"
)

const analyticalArchiveRequest = `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0",
"query":{"name":"event-counts","subject":"Count approved retained events.","from":"archive-events",
"aggregate":{"by":["Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}}}`

func TestSessionArchiveAnalyticalCapabilitiesHTTP(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	capabilities := (&hostedSessionArchive{}).AnalyticalCapabilities()
	probe := &sessionArchiveProbe{kinds: []string{"timeline", "summary", "session-query"}, capabilities: &capabilities}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true},
	}
	request := archiveRequest(t, app, strings.Replace(analyticalArchiveRequest, `"aggregate":`, `"limit":4097,"aggregate":`, 1), true)
	if request.Code != http.StatusUnprocessableEntity || probe.called != 0 {
		t.Fatal("hosted capabilities did not reject an unsupported limit before execution")
	}
	capabilities.Reducers = nil
	request = archiveRequest(t, app, analyticalArchiveRequest, true)
	if request.Code != http.StatusUnprocessableEntity || probe.called != 0 {
		t.Fatal("unsupported reducer was executed or silently emulated")
	}
	chained := `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{"name":"per-trace","subject":"Count event types per trace.",
"queries":[{"name":"per-type","subject":"Count events per type.","from":"archive-events","aggregate":{"by":["Namespace","Repository","RunId","RunAttempt","TraceId","ProjectionId","Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}}],
"from":"per-type","aggregate":{"by":["Namespace","Repository","RunId","RunAttempt","TraceId","ProjectionId"],"values":[{"field":"Type","as":"types","reducer":"count"}]}}}`
	capabilities = (&hostedSessionArchive{}).AnalyticalCapabilities()
	if request := archiveRequest(t, app, chained, true); request.Code != http.StatusUnprocessableEntity || probe.called != 0 {
		t.Fatal("hosted capabilities executed a chained query without a reviewed KQL lowering")
	}
	capabilities = (&localSessionArchive{}).AnalyticalCapabilities()
	if archiveRequest(t, app, chained, true); probe.called != 1 {
		t.Fatal("local capabilities rejected a valid chained query")
	}
	for _, backend := range []sessionArchiveBackend{&localSessionArchive{}, &hostedSessionArchive{}, &nativeSessionArchive{}} {
		var decoded sessionarchive.Capabilities
		content, err := json.Marshal(backend.AnalyticalCapabilities())
		if err != nil || json.Unmarshal(content, &decoded) != nil || decoded.LanguageVersion != "0.2.0" ||
			len(decoded.Fields) == 0 || decoded.Source != "archive-events" {
			t.Fatal("backend capability vocabulary is missing or unversioned")
		}
	}
}

type sessionArchiveAvailabilityProbe struct {
	hostedArchivePublisher
	available bool
}

func (probe *sessionArchiveAvailabilityProbe) AnalyticalAvailable() bool {
	return probe.available
}

func TestSessionArchiveQueryKindsReflectAnalyticalAvailability(t *testing.T) {
	for _, available := range []bool{false, true} {
		backend := &hostedSessionArchive{publisher: &sessionArchiveAvailabilityProbe{available: available}}
		if got := slices.Contains(backend.QueryKinds(), "session-query"); got != available {
			t.Fatalf("hosted analytical availability=%t advertised session-query=%t", available, got)
		}
	}
	native := &nativeSessionArchive{Native: &sessionarchiveblob.Native{}}
	if slices.Contains(native.QueryKinds(), "session-query") {
		t.Fatal("native archive advertised session-query without analytical acceptance")
	}
}

func TestSessionArchiveAnalyticalHTTPValidation(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{},
	}
	if response := archiveRequest(t, app, analyticalArchiveRequest, true); response.Code != http.StatusForbidden {
		t.Fatalf("analytical queries bypassed full-trace-read: %d", response.Code)
	}
	app.sessionArchive.read.Approved = true
	if response := archiveRequest(t, app, analyticalArchiveRequest, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("analytical queries bypassed authentication: %d", response.Code)
	}
	for name, body := range map[string]string{
		"missing-version": strings.Replace(analyticalArchiveRequest, `"language-version":"0.2.0",`, "", 1),
		"old-version":     strings.Replace(analyticalArchiveRequest, `"0.2.0"`, `"0.1.0"`, 1),
		"unknown-version": strings.Replace(analyticalArchiveRequest, `"0.2.0"`, `"2.0.0"`, 1),
		"missing-query":   `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0"}`,
		"null-query":      `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":null}`,
		"generic-query":   strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`, `"kind":"query"`, 1),
		"run-selector":    strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`, `"kind":"session-query","runId":"123"`, 1),
		"null-run-selector": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"session-query","runId":null`, 1),
		"attempt-selector": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"session-query","attempt":1`, 1),
		"null-attempt-selector": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"session-query","attempt":null`, 1),
		"envelope-limit": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`, `"kind":"session-query","limit":1`, 1),
		"null-envelope-limit": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"session-query","limit":null`, 1),
		"query-on-summary": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"summary"`, 1),
		"query-on-timeline": strings.Replace(analyticalArchiveRequest, `"kind":"session-query"`,
			`"kind":"timeline","runId":"123","attempt":1`, 1),
		"version-on-summary": `{"kind":"summary","repository":"octo/one","language-version":"0.2.0"}`,
		"payload-field":      strings.Replace(analyticalArchiveRequest, `"Sequence"`, `"PayloadJSON"`, 1),
		"file-source":        strings.Replace(analyticalArchiveRequest, `"archive-events"`, `"/private/data.parquet"`, 1),
		"unknown-key":        strings.Replace(analyticalArchiveRequest, `"aggregate":`, `"joins":[],"aggregate":`, 1),
		"malformed-filter":   strings.Replace(analyticalArchiveRequest, `"aggregate":`, `"filter":{"predicates":[]},"aggregate":`, 1),
		"null-filter":        strings.Replace(analyticalArchiveRequest, `"aggregate":`, `"filter":null,"aggregate":`, 1),
		"body-budget":        strings.Replace(analyticalArchiveRequest, `"subject":`, `"description":"`+strings.Repeat("x", 4096)+`","subject":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			response := archiveRequest(t, app, body, true)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid analytical request passed: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if response := archiveRequest(t, app, strings.Replace(analyticalArchiveRequest, "octo/one", "octo/two", 1), true); response.Code != http.StatusForbidden {
		t.Fatalf("analytical query escaped repository scope: %d", response.Code)
	}
	probe.kinds = (&hostedSessionArchive{}).QueryKinds()
	if response := archiveRequest(t, app, analyticalArchiveRequest, true); response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "unavailable on this archive backend") {
		t.Fatalf("hosted unsupported profile did not fail explicitly: %d %s", response.Code, response.Body.String())
	}
	if probe.called != 0 {
		t.Fatal("invalid or unsupported query reached execution")
	}
}

func TestSessionArchiveAnalyticalHTTPResponseBudgets(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true},
	}
	for _, rows := range [][]map[string]any{
		make([]map[string]any, 10001),
		{{"Type": strings.Repeat("x", 8<<20)}},
	} {
		probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
			return sessionArchiveResult{Generation: 1, Kind: "session-query", Rows: &rows}, nil
		}
		if response := archiveRequest(t, app, analyticalArchiveRequest, true); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("analytical response exceeded wire budgets: %d", response.Code)
		}
	}
	probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
		rows := []map[string]any{}
		return sessionArchiveResult{
			Generation: 1, Kind: "session-query", Rows: &rows,
			Columns:  []sessionarchive.Column{{Name: "Type", Type: "string"}},
			Metadata: &sessionarchive.QueryMetadata{UpstreamCoverage: "unknown"},
		}, nil
	}
	response := archiveRequest(t, app, analyticalArchiveRequest, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"rows":[]`) {
		t.Fatalf("empty analytical result lost its row/schema envelope: %d %s", response.Code, response.Body.String())
	}
}
