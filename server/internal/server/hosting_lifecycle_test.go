package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExternalHTTPServerDrainLifecycle(t *testing.T) {
	fixture := loadBlackboxFixtures(t).Hosting

	profile := hostedHostProfile()
	profile.Listener = HostListenerExternal
	app, err := New(t.Context(), newTestOperationalStore(t), Config{
		Database:      integrationDatabase(t),
		HostProfile:   profile,
		SiteDirectory: hostedTestSite(t),
		Proxy: ProxyPolicy{
			AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true,
			TrustForwarded: true, TrustedProxyPrefixes: loopbackProxyPrefixes(),
		},
		GitHubOAuth: validOAuthConfig("https://github.test"),
		CORS: CORSPolicy{
			AllowedOrigins: []string{"https://tools.example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })
	handler := app.Handler()
	httpServer := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r)
			if r.Header.Get("X-Test-Slow") == "1" {
				close(slowStarted)
				<-releaseSlow
			}
		}),
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- httpServer.Serve(listener) }()
	defer func() {
		_ = httpServer.Close()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	}()

	baseURL := "http://" + listener.Addr().String()
	remote := &http.Client{Timeout: 3 * time.Second}
	request := func(path string) *http.Request {
		t.Helper()
		req, requestErr := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+path, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Host = "127.0.0.1"
		req.Header.Set("X-Forwarded-Host", "dashboard.example.com")
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
	}
	check := func(req *http.Request, want int) *http.Response {
		t.Helper()
		resp, requestErr := remote.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if resp.StatusCode != want {
			defer func() { _ = resp.Body.Close() }()
			t.Fatalf("%s returned %d, want %d", req.URL.Path, resp.StatusCode, want)
		}
		return resp
	}
	checkAndClose := func(req *http.Request, want int) {
		t.Helper()
		resp, requestErr := remote.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != want {
			t.Fatalf("%s returned %d, want %d", req.URL.Path, resp.StatusCode, want)
		}
	}

	checkAndClose(request("/auth/logged-out"), fixture.BeforeStartStatus)
	preflight := request("/api/v1/events")
	preflight.Method = http.MethodOptions
	preflight.Header.Set("Origin", "https://tools.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	checkAndClose(preflight, http.StatusServiceUnavailable)
	canceled, cancelStartup := context.WithCancel(t.Context())
	cancelStartup()
	if err := app.Start(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup returned %v", err)
	}
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := app.Start(runCtx); err != nil {
		t.Fatal(err)
	}

	checkAndClose(request("/auth/logged-out"), fixture.ActiveStatus)
	invalidHost := request("/auth/logged-out")
	invalidHost.Header.Set("X-Forwarded-Host", "attacker.example.com")
	checkAndClose(invalidHost, http.StatusMisdirectedRequest)
	invalidProtocol := request("/auth/logged-out")
	invalidProtocol.Header.Set("X-Forwarded-Proto", "http")
	checkAndClose(invalidProtocol, http.StatusMisdirectedRequest)
	invalidPreflight := request("/api/v1/events")
	invalidPreflight.Method = http.MethodOptions
	invalidPreflight.Header.Set("Origin", "https://tools.example.com")
	invalidPreflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	invalidPreflight.Header.Set("X-Forwarded-Host", "attacker.example.com")
	invalid := check(invalidPreflight, http.StatusMisdirectedRequest)
	if invalid.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("invalid host received CORS access")
	}
	if err := invalid.Body.Close(); err != nil {
		t.Fatal(err)
	}
	allowed := check(preflight, http.StatusNoContent)
	if allowed.Header.Get("Access-Control-Allow-Origin") != "https://tools.example.com" ||
		allowed.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("incorrect CORS policy: %v", allowed.Header)
	}
	if err := allowed.Body.Close(); err != nil {
		t.Fatal(err)
	}
	denied := request("/api/v1/events")
	denied.Method = http.MethodOptions
	denied.Header.Set("Origin", "https://attacker.example.com")
	denied.Header.Set("Access-Control-Request-Method", http.MethodGet)
	deniedResponse := check(denied, http.StatusUnauthorized)
	if deniedResponse.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin received CORS access")
	}
	if err := deniedResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}

	session := oauthSession{
		ID: "external-lifecycle-session", Login: "reader", AccessToken: "test-token",
		AuthorizedAt: time.Now(), AccessExpires: time.Now().Add(time.Hour),
	}
	if err := app.oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	streamRequest := request("/api/v1/events")
	streamRequest.AddCookie(&http.Cookie{
		Name: sessionCookieName, Value: session.ID, Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	stream := check(streamRequest, http.StatusOK)
	defer func() { _ = stream.Body.Close() }()
	if stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("event stream content type = %q", stream.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(stream.Body)
	if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "data:") {
		t.Fatalf("initial event = %q, %v", line, err)
	}

	slowRequest := request("/auth/logged-out")
	slowRequest.Header.Set("X-Test-Slow", "1")
	slowResult := make(chan int, 1)
	slowError := make(chan error, 1)
	go func() {
		resp, requestErr := remote.Do(slowRequest)
		if requestErr == nil {
			defer func() { _ = resp.Body.Close() }()
			slowResult <- resp.StatusCode
		}
		slowError <- requestErr
	}()
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary request did not start")
	}
	taskCanceled := make(chan struct{})
	taskFinished := make(chan struct{})
	releaseTask := make(chan struct{})
	var taskOnce sync.Once
	defer taskOnce.Do(func() { close(releaseTask) })
	taskCtx, taskCancel := app.operationContext(t.Context())
	defer taskCancel()
	if !app.launchTask(func() {
		defer close(taskFinished)
		<-taskCtx.Done()
		close(taskCanceled)
		<-releaseTask
	}) {
		t.Fatal("running service refused background task")
	}

	app.Drain()
	postDrainTask := make(chan struct{}, 1)
	if app.launchTask(func() { postDrainTask <- struct{}{} }) {
		t.Fatal("draining service admitted a background task")
	}
	select {
	case <-postDrainTask:
		t.Fatal("background task ran after drain")
	default:
	}
	checkAndClose(request("/auth/logged-out"), fixture.AfterDrainStatus)
	checkAndClose(preflight, http.StatusServiceUnavailable)
	streamDone := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(io.Discard, reader)
		streamDone <- readErr
	}()
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("stream ended with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event stream outlived drain")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- httpServer.Shutdown(shutdownCtx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("HTTP shutdown did not wait for ordinary request: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseSlow) })
	if err := <-slowError; err != nil {
		t.Fatal(err)
	}
	if status := <-slowResult; status != http.StatusOK {
		t.Fatalf("ordinary request returned %d", status)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("HTTP shutdown failed: %v", err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve returned %v", err)
	}
	select {
	case <-taskCanceled:
		t.Fatal("background task canceled before HTTP drain")
	default:
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopCancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- app.Stop(stopCtx) }()
	select {
	case <-taskCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel background task")
	}
	select {
	case err := <-stopDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop returned %v before background task exited", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not honor its cancellation context")
	}
	select {
	case <-taskFinished:
		t.Fatal("background task exited before it was released")
	default:
	}
	taskOnce.Do(func() { close(releaseTask) })
	select {
	case <-taskFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("background task did not exit")
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatalf("Stop failed after background task exited: %v", err)
	}
}

func TestProcessDrainKeepsTasksAliveUntilHTTPShutdown(t *testing.T) {
	app := &App{config: Config{HostProfile: localHostProfile()}, drain: make(chan struct{})}
	startupCtx, cancelStartup := context.WithCancel(t.Context())
	if err := app.start(startupCtx, context.WithoutCancel(startupCtx)); err != nil {
		t.Fatal(err)
	}

	cancelStartup()
	app.Drain()
	if err := app.startContext.Err(); err != nil {
		t.Fatalf("background tasks canceled before HTTP shutdown: %v", err)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(app.startContext.Err(), context.Canceled) {
		t.Fatal("Stop did not cancel background tasks after drain")
	}
}

func TestStopClosesOnlyOwnedDatabaseAfterTasksFinish(t *testing.T) {
	database := integrationDatabase(t)
	app := &App{database: database, ownedDatabase: database, drain: make(chan struct{})}
	releaseTask := make(chan struct{})
	defer func() {
		select {
		case <-releaseTask:
		default:
			close(releaseTask)
		}
	}()
	app.startTask(func() {
		<-app.drain
		<-releaseTask
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := app.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop while task runs = %v", err)
	}
	if _, err := database.State(t.Context()); err != nil {
		t.Fatalf("pool closed while task was running: %v", err)
	}
	close(releaseTask)
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.State(t.Context()); err == nil {
		t.Fatal("owned pool remained open after Stop")
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatalf("repeated Stop failed: %v", err)
	}
	callerDatabase := integrationDatabase(t)
	caller := &App{database: callerDatabase, drain: make(chan struct{})}
	if err := caller.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := callerDatabase.State(t.Context()); err != nil {
		t.Fatalf("caller-owned pool closed: %v", err)
	}
}

func TestHostedServeStartupFailureClosesOwnedDatabase(t *testing.T) {
	database := integrationDatabase(t)
	app := &App{
		database: database, ownedDatabase: database,
		config: Config{HostProfile: localHostProfile()},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := app.Serve(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve with canceled startup = %v", err)
	}
	if _, err := database.State(t.Context()); err == nil {
		t.Fatal("Serve left hosted pool open after startup failure")
	}
}
