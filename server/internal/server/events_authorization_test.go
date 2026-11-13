package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

type eventTestDatabase struct {
	dashboardstore.Store
	revision atomic.Int64
}

func (database *eventTestDatabase) State(context.Context) (dashboardstore.State, error) {
	return dashboardstore.State{Revision: database.revision.Load(), Ready: true}, nil
}

type eventTestSessions struct {
	operational.SessionStore
	reads   atomic.Int64
	failure atomic.Bool
	block   atomic.Bool
}

func (store *eventTestSessions) SessionRecord(ctx context.Context, id string) (string, error) {
	store.reads.Add(1)
	if store.block.Load() {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if store.failure.Load() {
		return "", errors.New("session store unavailable")
	}
	return store.SessionStore.SessionRecord(ctx, id)
}

type eventStreamRecorder struct {
	*flushNotifyingRecorder
	mu sync.Mutex
}

func newEventStreamRecorder() *eventStreamRecorder {
	return &eventStreamRecorder{flushNotifyingRecorder: &flushNotifyingRecorder{
		ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 1),
	}}
}

func (recorder *eventStreamRecorder) Write(data []byte) (int, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.ResponseRecorder.Write(data)
}

func (recorder *eventStreamRecorder) Flush() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.flushNotifyingRecorder.Flush()
}

func (recorder *eventStreamRecorder) text() string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.Body.String()
}

func eventRequestCookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

func eventAuthorizationApp(t *testing.T, membershipState string) (*App, *memory.Store, *eventTestSessions, oauthSession) {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sessions := &eventTestSessions{SessionStore: store}
	config := validOAuthConfig("https://github.example")
	config.HTTPClient = &http.Client{Transport: resourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := `{"id":1,"login":"octocat"}`
		if strings.Contains(request.URL.Path, "/memberships/") {
			body = `{"state":"` + membershipState + `"}`
		}
		if request.URL.Path == "/login/oauth/access_token" {
			body = `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"refresh_token_expires_in":7200}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	database := &eventTestDatabase{}
	database.revision.Store(1)
	app := &App{
		services: store.OperationalServices(), database: database, hub: newEventHub(),
		config: Config{HostProfile: platformHostProfile(),
			Proxy: ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true}},
		oauth: newGitHubOAuth(*config, sessions, store, store), drain: make(chan struct{}),
	}
	session := oauthSession{
		ID: "stream-session", Login: "octocat", GitHubID: 1,
		AccessToken: "access", RefreshToken: "refresh", CSRFToken: "csrf",
		AccessExpires: time.Now().Add(time.Hour), RefreshExpires: time.Now().Add(2 * time.Hour),
		AuthorizedAt: time.Now(), AuthenticatedAt: time.Now(), Assurance: []string{assuranceTwoFactor},
	}
	return app, store, sessions, session
}

func startAuthorizedEventStream(t *testing.T, app *App, session oauthSession) (*eventStreamRecorder, <-chan struct{}, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	request := azureRequest(t, http.MethodGet, "/api/v1/events").WithContext(ctx)
	request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
	request.AddCookie(eventRequestCookie(csrfCookieName, session.CSRFToken))
	response := newEventStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Handler().ServeHTTP(response, request)
	}()
	synctest.Wait()
	select {
	case <-response.flushed:
	default:
		cancel()
		t.Fatalf("stream did not flush: status=%d body=%s", response.Code, response.text())
	}
	return response, done, cancel
}

func requireEventStreamClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()
	synctest.Wait()
	select {
	case <-done:
	default:
		t.Fatal("event stream retained authorization")
	}
}

func requireNoLaterEvent(t *testing.T, app *App, response *eventStreamRecorder) {
	t.Helper()
	before := response.text()
	app.hub.Broadcast(99)
	time.Sleep(time.Second)
	synctest.Wait()
	if response.text() != before {
		t.Fatal("closed stream delivered another event")
	}
	app.hub.mu.Lock()
	defer app.hub.mu.Unlock()
	if len(app.hub.clients) != 0 {
		t.Fatal("closed stream retained hub entitlement")
	}
}

func TestOAuthEventStreamRevocation(t *testing.T) {
	for _, scenario := range []string{"logout", "store-outage", "store-timeout"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app, _, sessions, session := eventAuthorizationApp(t, "active")
				defer app.hub.shutdown()
				if err := app.oauth.saveSession(t.Context(), session); err != nil {
					t.Fatal(err)
				}
				response, done, cancel := startAuthorizedEventStream(t, app, session)
				defer cancel()
				switch scenario {
				case "logout":
					request := azureRequest(t, http.MethodPost, "/auth/logout")
					request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
					request.Header.Set("X-CSRF-Token", session.CSRFToken)
					logout := httptest.NewRecorder()
					app.Handler().ServeHTTP(logout, request)
					if logout.Code != http.StatusNoContent {
						t.Fatalf("logout returned %d", logout.Code)
					}
					denied := httptest.NewRecorder()
					request = azureRequest(t, http.MethodGet, "/api/v1/events")
					request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
					app.Handler().ServeHTTP(denied, request)
					if denied.Code != http.StatusUnauthorized {
						t.Fatalf("revoked reconnect returned %d", denied.Code)
					}
				case "store-outage":
					sessions.failure.Store(true)
				case "store-timeout":
					sessions.block.Store(true)
				}
				time.Sleep(25 * time.Second)
				requireEventStreamClosed(t, done)
				requireNoLaterEvent(t, app, response)
			})
		})
	}
}

func TestOAuthEventStreamAuthorizationDeadlines(t *testing.T) {
	for _, scenario := range []string{"membership", "admin-assurance", "token-refresh"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				membershipState := "active"
				if scenario == "membership" {
					membershipState = "inactive"
				}
				app, store, _, session := eventAuthorizationApp(t, membershipState)
				defer app.hub.shutdown()
				switch scenario {
				case "membership":
					session.AuthorizedAt = time.Now().Add(-authorizationRecheckInterval + 30*time.Second)
				case "admin-assurance":
					app.config.AdminUsers = []string{session.Login}
					session.AuthenticatedAt = time.Now().Add(-defaultAdminReauthenticationMaxAge + 30*time.Second)
					if err := store.RecordIngestionHealthEvent(t.Context(), "success", "", time.Now()); err != nil {
						t.Fatal(err)
					}
				case "token-refresh":
					session.AccessExpires = time.Now().Add(tokenRefreshSkew + 30*time.Second)
				}
				if err := app.oauth.saveSession(t.Context(), session); err != nil {
					t.Fatal(err)
				}
				response, done, cancel := startAuthorizedEventStream(t, app, session)
				defer cancel()
				if scenario == "admin-assurance" && !strings.Contains(response.text(), `"healthRevision":1`) {
					t.Fatal("assured administrator did not receive health revision")
				}
				time.Sleep(29 * time.Second)
				select {
				case <-done:
					t.Fatal("stream closed before its authorization deadline")
				default:
				}
				time.Sleep(time.Second)
				requireEventStreamClosed(t, done)
				requireNoLaterEvent(t, app, response)
				if scenario == "membership" {
					request := azureRequest(t, http.MethodGet, "/api/v1/events")
					request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
					denied := httptest.NewRecorder()
					app.Handler().ServeHTTP(denied, request)
					if denied.Code != http.StatusUnauthorized {
						t.Fatalf("removed member reconnected: status=%d", denied.Code)
					}
					return
				}
				reconnected, reconnectedDone, stop := startAuthorizedEventStream(t, app, session)
				if scenario == "admin-assurance" && !strings.Contains(reconnected.text(), `"healthRevision":0`) {
					t.Fatal("expired administrator retained health revision on reconnect")
				}
				if scenario == "token-refresh" && len(reconnected.Header().Values("Set-Cookie")) == 0 {
					t.Fatal("reconnect did not renew cookies during token refresh")
				}
				stop()
				requireEventStreamClosed(t, reconnectedDone)
			})
		})
	}
}

func TestOAuthEventStreamInterruptsBlockedSessionCheck(t *testing.T) {
	for _, scenario := range []string{"cancel", "drain"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app, _, sessions, session := eventAuthorizationApp(t, "active")
				defer app.hub.shutdown()
				if err := app.oauth.saveSession(t.Context(), session); err != nil {
					t.Fatal(err)
				}
				response, done, cancel := startAuthorizedEventStream(t, app, session)
				defer cancel()
				sessions.block.Store(true)
				time.Sleep(20 * time.Second)
				synctest.Wait()
				if sessions.reads.Load() != 2 {
					t.Fatal("heartbeat session read did not start")
				}
				switch scenario {
				case "cancel":
					cancel()
				case "drain":
					close(app.drain)
				}
				requireEventStreamClosed(t, done)
				requireNoLaterEvent(t, app, response)
			})
		})
	}
}

func TestOAuthEventStreamBoundedLoadAndLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, sessions, session := eventAuthorizationApp(t, "active")
		defer app.hub.shutdown()
		if err := app.oauth.saveSession(t.Context(), session); err != nil {
			t.Fatal(err)
		}
		response, done, cancel := startAuthorizedEventStream(t, app, session)
		defer cancel()
		app.database.(*eventTestDatabase).revision.Store(2)
		for range 100 {
			app.hub.Broadcast(2)
			synctest.Wait()
		}
		if !strings.Contains(response.text(), `"revision":2`) {
			t.Fatal("active stream did not deliver a revision update")
		}
		if sessions.reads.Load() != 1 {
			t.Fatalf("event traffic caused session reads: %d", sessions.reads.Load())
		}
		time.Sleep(authorizationRecheckInterval)
		requireEventStreamClosed(t, done)
		if sessions.reads.Load() > 16 {
			t.Fatalf("session checks exceeded one per heartbeat: %d", sessions.reads.Load())
		}
		requireNoLaterEvent(t, app, response)
		reconnected, stopped, stop := startAuthorizedEventStream(t, app, session)
		if !strings.Contains(reconnected.text(), `"revision":2`) {
			t.Fatal("authorized member could not reconnect after the membership window")
		}
		stop()
		requireEventStreamClosed(t, stopped)
	})
}

func TestOAuthEventStreamHTTPWriteDeadline(t *testing.T) {
	app, _, _, session := eventAuthorizationApp(t, "active")
	defer app.hub.shutdown()
	session.AuthorizedAt = time.Now().Add(-authorizationRecheckInterval + 500*time.Millisecond)
	if err := app.oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(app.Handler())
	server.Config.WriteTimeout = 25 * time.Millisecond
	app.config.Proxy.TrustForwarded = true
	app.config.Proxy.TrustedProxyPrefixes = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	server.Start()
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "dashboard.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream returned %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	time.Sleep(75 * time.Millisecond)
	app.database.(*eventTestDatabase).revision.Store(2)
	app.hub.Broadcast(2)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("authorized stream ended at the ordinary HTTP write timeout: %v", err)
		}
		if strings.Contains(line, `"revision":2`) {
			break
		}
	}
	_, err = io.ReadAll(reader)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("stream did not end at its authorization deadline: %v", err)
	}
	if time.Now().Before(session.AuthorizedAt.Add(authorizationRecheckInterval)) {
		t.Fatal("stream ended before its authorization deadline")
	}
}

func TestEventStreamDrainAndLocalCapabilityLifetime(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "oauth", true: "local"}[local], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app, _, _, session := eventAuthorizationApp(t, "active")
				defer app.hub.shutdown()
				var done <-chan struct{}
				if local {
					app.oauth = nil
					app.config.HostProfile = localHostProfile()
					app.accessToken = testAccessToken
					request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/events", nil)
					authorize(request)
					response := newEventStreamRecorder()
					stopped := make(chan struct{})
					done = stopped
					go func() {
						defer close(stopped)
						app.Handler().ServeHTTP(response, request)
					}()
					synctest.Wait()
					time.Sleep(authorizationRecheckInterval + time.Second)
					select {
					case <-done:
						t.Fatal("OAuth lifetime was applied to a local capability stream")
					default:
					}
					if !strings.Contains(response.text(), ": keepalive") {
						t.Fatal("local capability stream did not remain active")
					}
				} else {
					if err := app.oauth.saveSession(t.Context(), session); err != nil {
						t.Fatal(err)
					}
					_, stopped, cancel := startAuthorizedEventStream(t, app, session)
					defer cancel()
					done = stopped
				}
				close(app.drain)
				requireEventStreamClosed(t, done)
			})
		})
	}
}
