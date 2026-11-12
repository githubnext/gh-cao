package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

type hostedCLITestQueue struct {
	mutex    sync.Mutex
	audit    *memoryAuditLog
	commands map[int64]*operational.CLICommand
	output   map[int64][]operational.CLICommandOutput
	next     int64
}

var _ operational.CLICommandQueue = (*hostedCLITestQueue)(nil)

func newHostedCLITestQueue() *hostedCLITestQueue {
	return &hostedCLITestQueue{commands: map[int64]*operational.CLICommand{}, output: map[int64][]operational.CLICommandOutput{}}
}

func (q *hostedCLITestQueue) Acquire(context.Context, string, time.Duration) (operational.CLICommandLease, error) {
	return operational.CLICommandLease{}, nil
}
func (q *hostedCLITestQueue) Renew(_ context.Context, lease operational.CLICommandLease, _ time.Duration) (operational.CLICommandLease, error) {
	return lease, nil
}
func (q *hostedCLITestQueue) Release(context.Context, operational.CLICommandLease) error { return nil }
func (q *hostedCLITestQueue) AcknowledgeRecovery(context.Context, operational.CLICommandLease) error {
	return nil
}
func (q *hostedCLITestQueue) Enqueue(_ context.Context, invocation operational.CLICommandInvocation, key string, retryOf int64) (operational.CLICommand, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	q.next++
	command := &operational.CLICommand{
		CLICommandInvocation: invocation, ID: q.next, Status: operational.CLICommandQueued,
		CreatedAt: time.Unix(1700000000, 0), IdempotencyKey: key, RetryOf: retryOf,
	}
	q.commands[command.ID] = command
	return *command, nil
}
func (q *hostedCLITestQueue) Claim(context.Context, operational.CLICommandLease) (*operational.CLICommand, error) {
	return nil, nil //nolint:nilnil // CLICommandQueue.Claim reports no runnable command as nil, nil.
}
func (q *hostedCLITestQueue) Finish(context.Context, operational.CLICommandLease, int64, operational.CLICommandResult) error {
	return nil
}
func (q *hostedCLITestQueue) AppendOutput(context.Context, operational.CLICommandLease, int64, string, string) (operational.CLICommandOutput, error) {
	return operational.CLICommandOutput{}, nil
}
func (q *hostedCLITestQueue) Settle(context.Context, operational.CLICommandLease, int64, *int) error {
	return nil
}
func (q *hostedCLITestQueue) Cancel(_ context.Context, id int64) (operational.CLICommand, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	command, ok := q.commands[id]
	if !ok {
		return operational.CLICommand{}, operational.ErrCLICommandNotFound
	}
	if !command.Status.Terminal() {
		command.Status = operational.CLICommandCanceled
	}
	return *command, nil
}
func (q *hostedCLITestQueue) Detail(_ context.Context, id int64) (operational.CLICommand, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	command, ok := q.commands[id]
	if !ok {
		return operational.CLICommand{}, operational.ErrCLICommandNotFound
	}
	return *command, nil
}
func (q *hostedCLITestQueue) List(context.Context, operational.CLICommandFilter) (operational.CLICommandPage, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	page := operational.CLICommandPage{}
	for id := int64(1); id <= q.next; id++ {
		if command, ok := q.commands[id]; ok {
			page.Commands = append(page.Commands, *command)
		}
	}
	page.Total = len(page.Commands)
	return page, nil
}
func (q *hostedCLITestQueue) ListBefore(context.Context, operational.CLICommandCursorFilter) (operational.CLICommandCursorPage, error) {
	return operational.CLICommandCursorPage{Commands: []operational.CLICommand{}}, nil
}
func (q *hostedCLITestQueue) Output(_ context.Context, id, after int64, limit int) ([]operational.CLICommandOutput, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	var chunks []operational.CLICommandOutput
	for _, chunk := range q.output[id] {
		if chunk.Sequence > after && len(chunks) < limit {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}
func (q *hostedCLITestQueue) Retain(context.Context) (int64, error) { return 0, nil }

func (q *hostedCLITestQueue) EnqueueAudited(ctx context.Context, invocation operational.CLICommandInvocation, key string, retryOf int64, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	command := operational.CLICommand{
		CLICommandInvocation: invocation, ID: q.next + 1, Status: operational.CLICommandQueued,
		CreatedAt: time.Unix(1700000000, 0), IdempotencyKey: key, RetryOf: retryOf,
	}
	event, err := operational.CLICommandAuditEvent(event, command)
	if err == nil {
		event, err = q.audit.AppendAudit(ctx, event)
	}
	if err != nil {
		return operational.CLICommand{}, operational.AuditEvent{}, err
	}
	q.next++
	q.commands[command.ID] = &command
	return command, event, nil
}

func (q *hostedCLITestQueue) CancelAudited(ctx context.Context, id int64, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	existing, ok := q.commands[id]
	if !ok {
		return operational.CLICommand{}, operational.AuditEvent{}, operational.ErrCLICommandNotFound
	}
	command := *existing
	if !command.Status.Terminal() {
		command.Status = operational.CLICommandCanceled
	}
	event, err := operational.CLICommandAuditEvent(event, command)
	if err == nil {
		event, err = q.audit.AppendAudit(ctx, event)
	}
	if err != nil {
		return operational.CLICommand{}, operational.AuditEvent{}, err
	}
	q.commands[id] = &command
	return command, event, nil
}

// memoryAuditLog is an in-process stand-in for the postgres audit table.
type memoryAuditLog struct {
	mutex  sync.Mutex
	events []operational.AuditEvent
	fail   bool
}

func (m *memoryAuditLog) AppendAudit(_ context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.fail {
		return operational.AuditEvent{}, errors.New("audit table unavailable")
	}
	event.Sequence = int64(len(m.events) + 1)
	event.OccurredAt = time.Now()
	m.events = append(m.events, event)
	return event, nil
}

func (m *memoryAuditLog) ListAudit(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return append([]operational.AuditEvent(nil), m.events...), nil
}

func (m *memoryAuditLog) VerifyAudit(context.Context, ...operational.AuditAnchor) (operational.AuditVerification, error) {
	return operational.AuditVerification{}, nil
}

func (m *memoryAuditLog) setFail(fail bool) {
	m.mutex.Lock()
	m.fail = fail
	m.mutex.Unlock()
}

type hostedCLIFixture struct {
	app     *App
	audit   *memoryAuditLog
	queue   *hostedCLITestQueue
	session *http.Cookie
	csrf    *http.Cookie
	push    bool
	mutex   sync.Mutex
	probes  []string
	github  *fakeGitHubServer
}

type hostedCLITestOAuth struct {
	clientID            string
	grantedScopes       []string
	privateRepository   bool
	appRepositoryAccess bool
	omitPermissions     bool
	sso                 bool
	consent             bool
}

// newHostedCLIFixture serves the OAuth fake and answers repository
// permission lookups made with the requester's OAuth token.
func newHostedCLIFixture(t *testing.T, admins []string) *hostedCLIFixture {
	t.Helper()
	return newHostedCLIFixtureWithOAuth(t, admins, hostedCLITestOAuth{})
}

func newHostedCLIFixtureWithOAuth(t *testing.T, admins []string, options hostedCLITestOAuth) *hostedCLIFixture {
	t.Helper()
	fixture := &hostedCLIFixture{queue: newHostedCLITestQueue(), push: true}
	oauthServer := fakeGitHub(t, fakeGitHubOptions{
		membershipState: "active", accessExpiresIn: 3600, grantedScopes: options.grantedScopes,
	})
	fixture.github = oauthServer
	target, err := url.Parse(oauthServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	github := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/repos/") {
			fixture.mutex.Lock()
			fixture.probes = append(fixture.probes, request.URL.Path+" "+request.Header.Get("Authorization"))
			push := fixture.push
			fixture.mutex.Unlock()
			grant := oauthServer.tokenGrant(request.Header.Get("Authorization"))
			if (grant.isGitHubApp() && !options.appRepositoryAccess) ||
				(!grant.isGitHubApp() && options.privateRepository && !grant.hasScope("repo")) {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			if options.omitPermissions {
				_, _ = response.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"permissions": map[string]bool{"push": push}})
			return
		}
		proxy.ServeHTTP(response, request)
	}))
	t.Cleanup(github.Close)
	fixture.app = memoryOAuthApp(t, github.URL)
	fixture.app.config.AdminUsers = admins
	if options.clientID != "" {
		fixture.app.oauth.config.ClientID = options.clientID
	}
	if options.sso {
		fixture.app.oauth.config.SSOOrganizations = []string{"example"}
	}
	fixture.audit = &memoryAuditLog{}
	fixture.queue.audit = fixture.audit
	fixture.app.config.Audit = fixture.app.config.Audit.WithStore(fixture.audit)
	catalog, err := hostedcli.LoadCatalog(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.EnableHostedCLIActions(t.Context(), HostedCLIConfig{
		Catalog: catalog, ControlRepository: "octo/cao", GitHubURL: "https://github.com",
		DashboardURL: "https://dashboard.example.com/", Queue: fixture.queue,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.session, fixture.csrf = callbackSession(t, fixture.app)
	if options.consent {
		stateCookie, state := hostedConsentState(t, fixture.app, fixture.session)
		response := oauthCallbackResponse(t, fixture.app, stateCookie, state, fixture.session)
		if response.Code != http.StatusFound {
			t.Fatalf("hosted consent callback = %d: %s", response.Code, response.Body.String())
		}
		fixture.session = firstCookie(t, response.Result(), sessionCookieName)
		fixture.csrf = firstCookie(t, response.Result(), csrfCookieName)
	}
	return fixture
}

func (f *hostedCLIFixture) do(t *testing.T, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	request := azureRequest(t, method, path)
	if body != "" {
		request = httptest.NewRequestWithContext(t.Context(), method, "https://dashboard.example.com"+path, strings.NewReader(body))
		request.Host = "dashboard.example.com"
		request.Header.Set("X-Forwarded-Host", "dashboard.example.com")
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(f.session)
	request.AddCookie(f.csrf)
	if csrf {
		request.Header.Set("X-CSRF-Token", f.csrf.Value)
	}
	response := httptest.NewRecorder()
	f.app.Handler().ServeHTTP(response, request)
	return response
}

const hostedUpgradeRequest = `{"id":"upgrade-target-repository","values":{"repository":"octo/app"},"arguments":{}}`

func TestHostedCLIActionsAreDisabledByDefault(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := memoryOAuthApp(t, github.URL)
	app.config.AdminUsers = []string{"octocat"}
	session, csrf := callbackSession(t, app)
	request := azureRequest(t, http.MethodPost, "/__cli_commands")
	request.AddCookie(session)
	request.AddCookie(csrf)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusAccepted || response.Code == http.StatusOK {
		t.Fatalf("disabled hosted CLI accepted a command: %d", response.Code)
	}
}

func TestHostedCLIEnableRequiresOAuthAndControlRepository(t *testing.T) {
	catalog, err := hostedcli.ParseCatalog([]byte(`{"dashboard":{"cli-actions":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := (&App{}).EnableHostedCLIActions(t.Context(), HostedCLIConfig{Catalog: catalog, ControlRepository: "octo/cao"}); err == nil {
		t.Fatal("hosted CLI actions enabled without OAuth")
	}
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := memoryOAuthApp(t, github.URL)
	if err := app.EnableHostedCLIActions(t.Context(), HostedCLIConfig{Catalog: catalog, ControlRepository: "../x", Queue: newHostedCLITestQueue()}); err == nil {
		t.Fatal("hosted CLI actions accepted an invalid control repository")
	}
	if app.oauth.repositoryConsentAllowed != nil {
		t.Fatal("failed hosted CLI enablement broadened requested OAuth scopes")
	}
}

func TestHostedCLIEnqueueRecordsActorAndRepository(t *testing.T) {
	fixture := newHostedCLIFixture(t, []string{"OctoCat"})
	response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["actor"] != "octocat" || payload["repository"] != "octo/app" || payload["status"] != "queued" {
		t.Fatalf("unexpected command %v", payload)
	}
	session := storedSession(t, fixture.app, fixture.session)
	command, err := fixture.queue.Detail(t.Context(), 1)
	if err != nil || command.ActorID <= 0 || command.ActorID != session.GitHubID {
		t.Fatalf("queue lost the admitted numeric identity: %+v, %v", command, err)
	}
	if len(fixture.probes) != 1 || !strings.HasPrefix(fixture.probes[0], "/repos/octo/app ") ||
		!strings.Contains(fixture.probes[0], "Bearer ") {
		t.Fatalf("requester permission was not checked with the OAuth token: %v", fixture.probes)
	}
	if strings.Contains(response.Body.String(), "access-token") {
		t.Fatal("response exposed an OAuth token")
	}
}

func TestHostedCLIAdmissionRequiresAndRebindsAccountIdentity(t *testing.T) {
	fixture := newHostedCLIFixture(t, []string{"octocat"})
	session := storedSession(t, fixture.app, fixture.session)
	var request hostedcli.Request
	if err := json.Unmarshal([]byte(hostedUpgradeRequest), &request); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{0, -1} {
		legacy := session
		legacy.GitHubID = id
		_, err := fixture.app.admit(t.Context(), legacy, request)
		var failure *hostedCLIError
		if !errors.As(err, &failure) || failure.status != http.StatusForbidden || len(fixture.probes) != 0 {
			t.Fatal("unverifiable session identity must fail before probing or enqueueing")
		}
	}
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d: %s", response.Code, response.Body.String())
	}
	// Simulate a failed pre-upgrade command. A retry is a fresh admission,
	// never a backfill of the original immutable identity.
	fixture.queue.commands[1].Actor = "previous"
	fixture.queue.commands[1].ActorID = 0
	fixture.queue.commands[1].Status = operational.CLICommandFailed
	response := fixture.do(t, http.MethodPost, "/__cli_commands/1/retry", `{}`, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("retry = %d: %s", response.Code, response.Body.String())
	}
	retried, err := fixture.queue.Detail(t.Context(), 2)
	if err != nil || retried.ActorID != session.GitHubID || retried.Actor != session.Login || retried.RetryOf != 1 {
		t.Fatalf("retry identity = %+v, %v", retried, err)
	}
	original, err := fixture.queue.Detail(t.Context(), 1)
	if err != nil || original.ActorID != 0 || original.Actor != "previous" {
		t.Fatal("retry must not rewrite legacy attribution")
	}
}

func TestReadHostedCLIJSONRequiresBoundedSingleDocument(t *testing.T) {
	valid := `{"value":"accepted"}`
	for _, test := range []struct {
		name, body, contentType string
		status                  int
	}{
		{name: "valid", body: valid},
		{name: "valid-charset", body: valid, contentType: "application/json; charset=utf-8"},
		{name: "trailing-whitespace", body: valid + " \r\n\t"},
		{name: "exact-limit", body: valid + strings.Repeat(" ", maxHostedCLIRequestBytes-len(valid))},
		{name: "trailing-array-close", body: valid + "]", status: http.StatusBadRequest},
		{name: "trailing-object-close", body: valid + "}", status: http.StatusBadRequest},
		{name: "second-object", body: valid + `{}`, status: http.StatusBadRequest},
		{name: "second-null", body: valid + `null`, status: http.StatusBadRequest},
		{name: "trailing-garbage", body: valid + "garbage", status: http.StatusBadRequest},
		{name: "unknown-field", body: `{"unknown":true}`, status: http.StatusBadRequest},
		{name: "oversized-whitespace", body: valid + strings.Repeat(" ", maxHostedCLIRequestBytes-len(valid)+1), status: http.StatusBadRequest},
		{name: "oversized-value", body: `{"value":"` + strings.Repeat("a", maxHostedCLIRequestBytes) + `"}`, status: http.StatusBadRequest},
		{name: "media-type-prefix", body: valid, contentType: "application/jsonp", status: http.StatusUnsupportedMediaType},
		{name: "malformed-media-type", body: valid, contentType: "application/json; charset", status: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/__cli_commands", strings.NewReader(test.body))
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			request.Header.Set("Content-Type", contentType)
			var payload struct {
				Value string `json:"value"`
			}
			err := readHostedCLIJSON(request, &payload)
			if test.status == 0 {
				if err != nil || payload.Value != "accepted" {
					t.Fatalf("valid request: value=%q, err=%v", payload.Value, err)
				}
				return
			}
			var failure *hostedCLIError
			if !errors.As(err, &failure) || failure.status != test.status {
				t.Fatalf("invalid request: got %v, want status %d", err, test.status)
			}
		})
	}
}

func TestHostedCLIRepositoryAccessOAuthContract(t *testing.T) {
	for _, test := range []struct {
		name    string
		oauth   hostedCLITestOAuth
		noWrite bool
		status  int
	}{
		{name: "classic private repository with consent", oauth: hostedCLITestOAuth{privateRepository: true, consent: true}, status: http.StatusAccepted},
		{name: "classic private repository before consent", oauth: hostedCLITestOAuth{privateRepository: true}, status: http.StatusForbidden},
		{name: "classic declined repo scope", oauth: hostedCLITestOAuth{privateRepository: true, consent: true, grantedScopes: []string{"read:org", "read:user"}}, status: http.StatusForbidden},
		{name: "classic public repository without repo scope", oauth: hostedCLITestOAuth{grantedScopes: []string{"read:org", "read:user"}}, status: http.StatusAccepted},
		{name: "classic private reader", oauth: hostedCLITestOAuth{privateRepository: true, consent: true}, noWrite: true, status: http.StatusForbidden},
		{name: "GitHub App private writer with SSO", oauth: hostedCLITestOAuth{clientID: "Iv23liGitHubApp", privateRepository: true, appRepositoryAccess: true, sso: true}, status: http.StatusAccepted},
		{name: "GitHub App private reader with SSO", oauth: hostedCLITestOAuth{clientID: "Iv23liGitHubApp", privateRepository: true, appRepositoryAccess: true, sso: true}, noWrite: true, status: http.StatusForbidden},
		{name: "GitHub App missing installation access", oauth: hostedCLITestOAuth{clientID: "Iv23liGitHubApp", privateRepository: true, sso: true}, status: http.StatusForbidden},
		{name: "GitHub App missing writer evidence", oauth: hostedCLITestOAuth{clientID: "Iv23liGitHubApp", privateRepository: true, appRepositoryAccess: true, omitPermissions: true, sso: true}, status: http.StatusForbidden},
		{name: "GitHub App missing assurance", oauth: hostedCLITestOAuth{clientID: "Iv23liGitHubApp", privateRepository: true, appRepositoryAccess: true}, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHostedCLIFixtureWithOAuth(t, []string{"octocat"}, test.oauth)
			fixture.mutex.Lock()
			fixture.push = !test.noWrite
			fixture.mutex.Unlock()
			response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true)
			if response.Code != test.status {
				t.Fatalf("enqueue = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			page, err := fixture.queue.List(t.Context(), operational.CLICommandFilter{})
			if err != nil {
				t.Fatal(err)
			}
			wantCommands := 0
			if test.status == http.StatusAccepted {
				wantCommands = 1
			}
			if len(page.Commands) != wantCommands {
				t.Fatalf("queued %d commands, want %d", len(page.Commands), wantCommands)
			}
		})
	}
}

func TestHostedCLIAdmissionFailsClosed(t *testing.T) {
	t.Run("non-admin", func(t *testing.T) {
		fixture := newHostedCLIFixture(t, []string{"someone-else"})
		if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusForbidden {
			t.Fatalf("non-admin enqueue = %d", response.Code)
		}
		if response := fixture.do(t, http.MethodGet, "/__cli_commands", "", false); response.Code != http.StatusForbidden {
			t.Fatalf("non-admin list = %d", response.Code)
		}
	})
	t.Run("missing CSRF", func(t *testing.T) {
		fixture := newHostedCLIFixture(t, []string{"octocat"})
		if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, false); response.Code != http.StatusForbidden {
			t.Fatalf("enqueue without CSRF = %d", response.Code)
		}
	})
	t.Run("no write permission", func(t *testing.T) {
		fixture := newHostedCLIFixture(t, []string{"octocat"})
		fixture.push = false
		if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusForbidden {
			t.Fatalf("enqueue without push = %d", response.Code)
		}
		if len(fixture.queue.commands) != 0 {
			t.Fatal("unauthorized command was queued")
		}
	})
	t.Run("undeclared action", func(t *testing.T) {
		fixture := newHostedCLIFixture(t, []string{"octocat"})
		response := fixture.do(t, http.MethodPost, "/__cli_commands", `{"id":"no-such-action"}`, true)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("undeclared action = %d", response.Code)
		}
	})
	t.Run("unknown fields", func(t *testing.T) {
		fixture := newHostedCLIFixture(t, []string{"octocat"})
		for _, payload := range []string{
			`{"id":"upgrade-target-repository","command":"rm -rf /"}`,
			`{"id":"upgrade-target-repository","actorId":202}`,
			`{"id":"upgrade-target-repository","actor":"different"}`,
		} {
			response := fixture.do(t, http.MethodPost, "/__cli_commands", payload, true)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("unknown field = %d", response.Code)
			}
		}
		if len(fixture.queue.commands) != 0 {
			t.Fatal("client-supplied execution or requester fields must not enqueue work")
		}
	})
}

func TestHostedCLIListDetailCancelAndOutput(t *testing.T) {
	fixture := newHostedCLIFixture(t, []string{"octocat"})
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d: %s", response.Code, response.Body.String())
	}
	list := fixture.do(t, http.MethodGet, "/__cli_commands?limit=10", "", false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"repository":"octo/app"`) {
		t.Fatalf("list = %d: %s", list.Code, list.Body.String())
	}
	if response := fixture.do(t, http.MethodGet, "/__cli_commands/1", "", false); response.Code != http.StatusOK {
		t.Fatalf("detail = %d", response.Code)
	}
	if response := fixture.do(t, http.MethodGet, "/__cli_commands/99", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("missing detail = %d", response.Code)
	}
	if response := fixture.do(t, http.MethodPost, "/__cli_commands/1/retry", `{}`, true); response.Code != http.StatusConflict {
		t.Fatalf("retry of queued command = %d", response.Code)
	}
	fixture.push = false
	if response := fixture.do(t, http.MethodPost, "/__cli_commands/1/cancel", `{}`, true); response.Code != http.StatusForbidden {
		t.Fatalf("cancel without write access = %d", response.Code)
	}
	fixture.push = true
	cancel := fixture.do(t, http.MethodPost, "/__cli_commands/1/cancel", `{}`, true)
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), `"status":"canceled"`) {
		t.Fatalf("cancel = %d: %s", cancel.Code, cancel.Body.String())
	}
	fixture.queue.output[1] = []operational.CLICommandOutput{{Sequence: 1, Stream: "stdout", Data: "hello\n"}}
	output := fixture.do(t, http.MethodGet, "/__cli_commands/1/output", "", false)
	if output.Code != http.StatusOK {
		t.Fatalf("output = %d", output.Code)
	}
	var types []string
	scanner := bufio.NewScanner(strings.NewReader(output.Body.String()))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		types = append(types, event["type"].(string))
	}
	if strings.Join(types, ",") != "output,complete" {
		t.Fatalf("output events = %v", types)
	}
}

func TestHostedCLIIndexAdvertisesQueueOnlyWhenEnabled(t *testing.T) {
	fixture := newHostedCLIFixture(t, []string{"octocat"})
	if err := os.WriteFile(filepath.Join(fixture.app.config.SiteDirectory, "index.html"), []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := fixture.do(t, http.MethodGet, "/", "", false)
	if !strings.Contains(response.Body.String(), "__CLI_QUEUE__ = true") {
		t.Fatalf("enabled index does not advertise the queue: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "__DASHBOARD_ADMIN__ = true") {
		t.Fatal("authorized administrator index does not advertise admin access")
	}
	fixture.app.hostedCLI = nil
	response = fixture.do(t, http.MethodGet, "/", "", false)
	if strings.Contains(response.Body.String(), "__CLI_QUEUE__") {
		t.Fatal("disabled index advertised the queue")
	}
	if !strings.Contains(response.Body.String(), "__DASHBOARD_ADMIN__ = true") {
		t.Fatal("administrator visibility must not depend on queue availability")
	}
	fixture.app.config.AdminUsers = nil
	response = fixture.do(t, http.MethodGet, "/", "", false)
	if strings.Contains(response.Body.String(), "__DASHBOARD_ADMIN__") {
		t.Fatal("non-administrator index advertised admin access")
	}
}

func TestHostedCLIConfigEnvironment(t *testing.T) {
	t.Setenv("CAO_HOSTED_CLI_CONTROL_REPOSITORY", "")
	t.Setenv("CAO_COLLECT_CONTROL_REPOSITORY", "octo/cao")
	t.Setenv("CAO_HOSTED_CLI_DASHBOARD_URL", "")
	t.Setenv("CAO_GITHUB_REDIRECT_URL", "https://dashboard.example.com/auth/callback")
	config, err := HostedCLIConfigFromEnv(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if config.ControlRepository != "octo/cao" || config.DashboardURL != "https://dashboard.example.com/" {
		t.Fatalf("unexpected config %+v", config)
	}
	for apiURL, want := range map[string]string{
		"": "github.com", "https://api.github.com": "github.com",
		"https://api.octo.ghe.com": "octo.ghe.com", "https://ghes.example.com/api/v3": "ghes.example.com",
	} {
		if got, err := gitHubHost(apiURL); err != nil || got != want {
			t.Fatalf("gitHubHost(%q) = %q, %v", apiURL, got, err)
		}
	}
	if _, err := gitHubHost("http://ghes.example.com"); err == nil {
		t.Fatal("plaintext GitHub API accepted")
	}
}

func TestHostedCLIExecutorRequiresDedicatedApp(t *testing.T) {
	t.Setenv("CAO_HOSTED_CLI_APP_ID", "")
	if _, err := NewHostedCLIExecutorFromEnv(t.Context(), HostedCLIExecutorOptions{}); err == nil ||
		!strings.Contains(err.Error(), "CAO_HOSTED_CLI_APP_ID") {
		t.Fatalf("executor started without a dedicated App: %v", err)
	}
}

func TestHostedCLIAuditsAdminDecisionsWithoutCredentials(t *testing.T) {
	var buffer bytes.Buffer
	fixture := newHostedCLIFixture(t, []string{"octocat"})
	fixture.app.config.Audit = audit.New(&buffer).WithStore(fixture.audit)
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d", response.Code)
	}
	fixture.push = false
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusForbidden {
		t.Fatalf("enqueue without push = %d", response.Code)
	}
	fixture.app.config.AdminUsers = []string{"someone-else"}
	if response := fixture.do(t, http.MethodGet, "/__cli_commands", "", false); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin list = %d", response.Code)
	}
	events := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("audit line is not JSON: %q", line)
		}
		events[event["event"].(string)] = event
	}
	admitted := events["cli_command.admitted"]
	if admitted == nil || admitted["actor"] != "octocat" || admitted["repository"] != "octo/app" ||
		admitted["action"] != "upgrade-target-repository" || admitted["authenticated_at"] == nil {
		t.Fatalf("admission was not audited: %v", events)
	}
	if rejected := events["cli_command.rejected"]; rejected == nil || rejected["http_status"] != float64(http.StatusForbidden) {
		t.Fatalf("rejection was not audited: %v", events)
	}
	if denied := events["admin.denied"]; denied == nil || denied["reason"] != "not_admin" || denied["path"] != "/__cli_commands" {
		t.Fatalf("admin denial was not audited: %v", events)
	}
	if strings.Contains(buffer.String(), "access-token") || strings.Contains(buffer.String(), "refresh-token") {
		t.Fatal("audit events must not contain OAuth credentials")
	}
	if admitted["stored"] != true {
		t.Fatalf("mirrored event must report durable storage: %v", admitted)
	}
	stored, _ := fixture.audit.ListAudit(t.Context(), operational.AuditFilter{})
	names := make([]string, 0, len(stored))
	for _, event := range stored {
		names = append(names, event.Event)
	}
	if len(stored) != 3 || stored[0].Event != "cli_command.admitted" || stored[0].Actor != "octocat" ||
		stored[0].Attributes["repository"] != "octo/app" || stored[2].Event != "admin.denied" {
		t.Fatalf("audit table = %v", names)
	}
}

func TestHostedCLIRequiresDurableAuditTable(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := memoryOAuthApp(t, github.URL)
	catalog, err := hostedcli.ParseCatalog([]byte(`{"dashboard":{"cli-actions":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	err = app.EnableHostedCLIActions(t.Context(), HostedCLIConfig{Catalog: catalog, ControlRepository: "octo/cao", Queue: newHostedCLITestQueue()})
	if err == nil || !strings.Contains(err.Error(), "audit table") {
		t.Fatalf("hosted CLI enabled without a durable audit table: %v", err)
	}
}

func TestHostedCLIMutationsFailClosedWithoutAudit(t *testing.T) {
	fixture := newHostedCLIFixture(t, []string{"octocat"})
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d", response.Code)
	}
	fixture.audit.setFail(true)
	if response := fixture.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unaudited enqueue = %d: %s", response.Code, response.Body.String())
	}
	if response := fixture.do(t, http.MethodPost, "/__cli_commands/1/cancel", `{}`, true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unaudited cancel = %d: %s", response.Code, response.Body.String())
	}
	fixture.queue.mutex.Lock()
	defer fixture.queue.mutex.Unlock()
	if len(fixture.queue.commands) != 1 || fixture.queue.commands[1].Status != operational.CLICommandQueued {
		t.Fatal("audit failure must roll back admission and cancellation")
	}
}

func TestHostedCLILocalDevelopmentRequiresLoopback(t *testing.T) {
	catalog, err := hostedcli.ParseCatalog([]byte(`{"dashboard":{"cli-actions":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	store := hostedCLIPostgresTestStore(t)
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	public := memoryOAuthApp(t, github.URL)
	err = public.EnableHostedCLIActions(t.Context(), HostedCLIConfig{Catalog: catalog, ControlRepository: "octo/cao", LocalDevelopment: true})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("local development accepted on a public listener: %v", err)
	}
	local := memoryOAuthApp(t, github.URL)
	local.config.Listen = "127.0.0.1:8443"
	local.services.CLICommands = store
	local.config.Audit = local.config.Audit.WithStore(store)
	if err := local.EnableHostedCLIActions(t.Context(), HostedCLIConfig{Catalog: catalog, ControlRepository: "octo/cao", LocalDevelopment: true}); err != nil {
		t.Fatal(err)
	}
	if !local.config.Audit.Durable() || local.hostedCLI == nil {
		t.Fatal("local development did not retain PostgreSQL queue and audit services")
	}
}
