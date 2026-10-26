package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

type sessionArchiveProbe struct {
	query     func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error)
	flush     func(context.Context) error
	called    int
	closed    int
	withdrawn int
	kinds     []string
}

type sessionArchiveReadProbe struct {
	deliver func(func() error) error
	closed  int
}

func (p *sessionArchiveReadProbe) Deliver(deliver func() error) error {
	if p.deliver != nil {
		return p.deliver(deliver)
	}
	return deliver()
}

func (p *sessionArchiveReadProbe) Close() error { p.closed++; return nil }

func (*sessionArchiveProbe) PrepareCache(context.Context, string, string, string) error { return nil }
func (*sessionArchiveProbe) Capture(context.Context, string, string, string) error      { return nil }
func (p *sessionArchiveProbe) WithdrawRepository(context.Context, string) error {
	p.withdrawn++
	return sessionarchive.ErrWithdrawn
}
func (p *sessionArchiveProbe) Flush(ctx context.Context) error {
	if p.flush != nil {
		return p.flush(ctx)
	}
	return nil
}
func (*sessionArchiveProbe) Maintain(context.Context) error { return nil }
func (*sessionArchiveProbe) Status() sessionarchive.Status {
	return sessionarchive.Status{Enabled: true}
}
func (p *sessionArchiveProbe) QueryKinds() []string {
	if p.kinds != nil {
		return p.kinds
	}
	return []string{"timeline", "summary", "session-query"}
}
func (p *sessionArchiveProbe) Close() error { p.closed++; return nil }
func (p *sessionArchiveProbe) Query(ctx context.Context, query sessionArchiveQuery) (sessionArchiveResult, error) {
	p.called++
	result := sessionArchiveResult{Generation: 7, Kind: query.Kind}
	var err error
	if p.query != nil {
		result, err = p.query(ctx, query)
	}
	if err == nil && result.reader == nil {
		result.reader = &sessionArchiveReadProbe{}
	}
	return result, err
}

func archiveRequest(t *testing.T, app *App, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://localhost/api/v1/session-archive/query", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		authorize(request)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}

func TestSessionArchiveDefaultsOffWithoutTouchingConfiguration(t *testing.T) {
	t.Setenv("CAO_SESSION_ARCHIVE_ENABLED", "false")
	t.Setenv("CAO_SESSION_ARCHIVE_BLOB_ENABLED", "false")
	t.Setenv("CAO_SESSION_ARCHIVE_BLOB_ACCOUNT", "invalid-disabled-setting")
	t.Setenv("CAO_SESSION_ARCHIVE_DIRECTORY", "/must-not-be-created")
	t.Setenv("CAO_SESSION_ARCHIVE_POLICY", "/must-not-be-read")
	t.Setenv("CAO_SESSION_ARCHIVE_READ_APPROVED", "invalid-disabled-setting")
	app, _ := newSQLiteTestApp(t, false)
	if app.sessionArchive != nil {
		t.Fatal("disabled archive constructed a service")
	}
	response := archiveRequest(t, app, `{"kind":"summary","repository":"octo/one"}`, true)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled query returned %d: %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://localhost/api/v1/session-archive/status", nil)
	authorize(request)
	status := httptest.NewRecorder()
	app.Handler().ServeHTTP(status, request)
	if status.Code != http.StatusOK || strings.TrimSpace(status.Body.String()) != `{"enabled":false,"stage":"disabled"}` {
		t.Fatalf("disabled availability leaked configuration: %d %s", status.Code, status.Body.String())
	}
}

func TestSessionArchiveReadGrantAndTrustedScope(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{},
	}
	valid := `{"kind":"timeline","repository":"octo/one","runId":"123","attempt":1}`
	if response := archiveRequest(t, app, valid, true); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary dashboard token implied full-content permission: %d", response.Code)
	}
	app.sessionArchive.read.Approved = true
	if response := archiveRequest(t, app, valid, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("read approval bypassed authentication: %d", response.Code)
	}
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"scope", `{"kind":"summary","repository":"octo/two"}`, http.StatusForbidden},
		{"wildcard", `{"kind":"summary","repository":"octo/*"}`, http.StatusBadRequest},
		{"case", `{"kind":"summary","repository":"Octo/one"}`, http.StatusBadRequest},
		{"sql", `{"kind":"summary","repository":"octo/one","sql":"SELECT * FROM '/etc/passwd'"}`, http.StatusBadRequest},
		{"kql", `{"kind":"summary","repository":"octo/one","kql":"externaldata(x:string)"}`, http.StatusBadRequest},
		{"path", `{"kind":"summary","repository":"octo/one","path":"/private/archive"}`, http.StatusBadRequest},
		{"kind", `{"kind":"raw","repository":"octo/one"}`, http.StatusBadRequest},
		{"attempt", `{"kind":"timeline","repository":"octo/one","runId":"123"}`, http.StatusBadRequest},
		{"id", `{"kind":"timeline","repository":"octo/one","runId":"run:123","attempt":1}`, http.StatusBadRequest},
		{"noncanonical-id", `{"kind":"timeline","repository":"octo/one","runId":"00123","attempt":1}`, http.StatusBadRequest},
		{"summary-identity", `{"kind":"summary","repository":"octo/one","attempt":1}`, http.StatusBadRequest},
		{"row-limit", `{"kind":"summary","repository":"octo/one","limit":10001}`, http.StatusBadRequest},
		{"trailing-json", `{"kind":"summary","repository":"octo/one"} {}`, http.StatusBadRequest},
		{"body-limit", `{"kind":"summary","repository":"octo/one","padding":"` + strings.Repeat("x", 4096) + `"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := archiveRequest(t, app, test.body, true)
			if response.Code != test.status {
				t.Fatalf("returned %d, expected %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
	if probe.called != 0 {
		t.Fatal("invalid or unauthorized request reached archive execution")
	}
	if response := archiveRequest(t, app, valid, true); response.Code != http.StatusOK {
		t.Fatalf("trusted query failed: %d %s", response.Code, response.Body.String())
	}
}

func TestSessionArchiveResultBudgetsErrorsAndRevocation(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true},
	}
	body := `{"kind":"timeline","repository":"octo/one","runId":"123","attempt":1,"limit":1}`
	for _, test := range []struct {
		err    error
		status int
	}{
		{sessionarchive.ErrLimit, http.StatusUnprocessableEntity},
		{sessionarchive.ErrCapacity, http.StatusUnprocessableEntity},
		{sessionarchive.ErrNotFound, http.StatusNotFound},
		{sessionarchive.ErrWithdrawn, http.StatusForbidden},
		{errors.New("private /artifact/path credentials"), http.StatusServiceUnavailable},
	} {
		probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
			return sessionArchiveResult{}, test.err
		}
		response := archiveRequest(t, app, body, true)
		if response.Code != test.status || strings.Contains(response.Body.String(), "/artifact/path") {
			t.Fatalf("unsafe failure response: %d %s", response.Code, response.Body.String())
		}
	}
	for _, events := range [][]sessionarchive.Event{
		{{Sequence: 0}, {Sequence: 1}},
		{{PayloadJSON: strings.Repeat("x", 8<<20)}},
	} {
		probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
			return sessionArchiveResult{Generation: 1, Kind: "timeline", Events: events}, nil
		}
		if response := archiveRequest(t, app, body, true); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("oversized complete result passed: %d", response.Code)
		}
	}
	probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
		// Simulate capability replacement during a long-running query.
		app.accessToken = strings.Repeat("new", 16)
		return sessionArchiveResult{Generation: 1, Kind: "timeline"}, nil
	}
	if response := archiveRequest(t, app, body, true); response.Code != http.StatusForbidden {
		t.Fatalf("revoked capability received a completed result: %d", response.Code)
	}
}

func TestSessionArchiveResponseClosesReader(t *testing.T) {
	for _, outcome := range []string{"success", "withdrawn", "oversize", "authorization-revoked", "authorization-revoked-at-delivery", "query-error"} {
		t.Run(outcome, func(t *testing.T) {
			app, _ := newSQLiteTestApp(t, false)
			reader := &sessionArchiveReadProbe{}
			delivered := false
			reader.deliver = func(deliver func() error) error {
				if reader.closed != 0 {
					t.Error("response reader was closed before delivery")
				}
				if outcome == "withdrawn" {
					return sessionarchive.ErrWithdrawn
				}
				if outcome == "authorization-revoked-at-delivery" {
					app.accessToken = strings.Repeat("new", 16)
				}
				delivered = true
				return deliver()
			}
			expected := http.StatusOK
			probe := &sessionArchiveProbe{query: func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
				result := sessionArchiveResult{Generation: 1, Kind: "timeline", reader: reader}
				switch outcome {
				case "oversize":
					result.Events = []sessionarchive.Event{{}, {}}
					expected = http.StatusUnprocessableEntity
				case "authorization-revoked":
					app.accessToken = strings.Repeat("new", 16)
					expected = http.StatusForbidden
				case "withdrawn":
					expected = http.StatusForbidden
				case "authorization-revoked-at-delivery":
					expected = http.StatusForbidden
				case "query-error":
					expected = http.StatusServiceUnavailable
					return result, errors.New("synthetic query failure")
				}
				return result, nil
			}}
			app.sessionArchive = &sessionArchiveService{
				backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true},
			}
			response := archiveRequest(t, app, `{"kind":"timeline","repository":"octo/one","runId":"123","attempt":1,"limit":1}`, true)
			if response.Code != expected || reader.closed != 1 ||
				delivered != (outcome == "success" || outcome == "authorization-revoked" || outcome == "authorization-revoked-at-delivery") {
				t.Fatalf("unexpected reader lifecycle: status=%d closed=%d delivered=%t", response.Code, reader.closed, delivered)
			}
		})
	}
}

func TestSessionArchiveHostedReadIsSeparateFromAdministrator(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.config.AdminUsers = []string{"octocat"}
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{
		backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true},
	}
	cookie, csrf := callbackSession(t, app)
	query := func() *httptest.ResponseRecorder {
		request := azureRequest(t, http.MethodPost, "/api/v1/session-archive/query")
		request.Body = io.NopCloser(strings.NewReader(`{"kind":"summary","repository":"octo/one"}`))
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf.Value)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		return response
	}
	if response := query(); response.Code != http.StatusForbidden {
		t.Fatalf("administrator acquired implicit trace-read authority: %d", response.Code)
	}
	app.sessionArchive.read.Users = []string{"octocat"}
	if response := query(); response.Code != http.StatusOK {
		t.Fatalf("approved current reader failed: %d %s", response.Code, response.Body.String())
	}
	probe.query = func(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
		if err := app.oauth.deleteSession(ctx, cookie.Value); err != nil {
			t.Fatal(err)
		}
		return sessionArchiveResult{Generation: 2, Kind: input.Kind}, nil
	}
	if response := query(); response.Code != http.StatusForbidden {
		t.Fatalf("revoked OAuth reader received completed result: %d", response.Code)
	}
}

func TestSessionArchiveStatusWireContract(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	app.sessionArchive = &sessionArchiveService{
		backend: &sessionArchiveProbe{}, read: SessionArchiveReadAccess{Approved: true}, retrying: true,
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://localhost/api/v1/session-archive/status", nil)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	var status struct {
		Enabled bool           `json:"enabled"`
		Stage   string         `json:"stage"`
		Archive map[string]any `json:"archive"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Stage != "retrying" ||
		status.Archive["enabled"] != true || status.Archive["gaps"] != float64(0) {
		t.Fatalf("status does not match TypeSpec: %s %v", response.Body.String(), err)
	}
}

func TestSessionArchiveShutdownJoinsPublisherBeforeClosing(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	started, finished := make(chan struct{}), make(chan struct{})
	probe := &sessionArchiveProbe{flush: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}}
	app.sessionArchive = &sessionArchiveService{backend: probe}
	app.startTask(func() { app.sessionArchive.run(app.startContext) })
	<-started
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("archive owner closed before its publisher exited")
	}
	if probe.closed != 1 {
		t.Fatal("archive owner was not closed")
	}
	if err := app.Stop(t.Context()); err != nil || probe.closed != 1 {
		t.Fatal("repeated shutdown closed an archive owner twice")
	}
}

func TestSessionArchiveStandaloneRolesRejectCapture(t *testing.T) {
	for _, enabled := range []string{"CAO_SESSION_ARCHIVE_ENABLED", "CAO_SESSION_ARCHIVE_BLOB_ENABLED"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("CAO_SESSION_ARCHIVE_ENABLED", "false")
			t.Setenv("CAO_SESSION_ARCHIVE_BLOB_ENABLED", "false")
			t.Setenv(enabled, "true")
			if _, err := NewCollectorFromEnv(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "owning persistent server") {
				t.Fatalf("standalone role silently ignored archive capture: %v", err)
			}
		})
	}
}

func TestSessionArchiveNarrowingPreservesUncapturedRepositoryCollection(t *testing.T) {
	probe := &sessionArchiveProbe{}
	archive := &sessionArchiveService{backend: probe, repositories: []string{"octo/one"}}
	if err := archive.WithdrawRepository(t.Context(), "octo/not-captured"); err != nil || probe.withdrawn != 0 {
		t.Fatalf("narrowed archive blocked unrelated collection: %v", err)
	}
	if err := archive.WithdrawRepository(t.Context(), "octo/one"); !errors.Is(err, sessionarchive.ErrWithdrawn) || probe.withdrawn != 1 {
		t.Fatalf("configured withdrawal was dropped: %v", err)
	}
}
