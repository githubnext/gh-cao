package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestOAuthCallbackUnknownExpiryCannotAbandonNoRefreshCredentials(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{
		membershipState: "active", omitExpiryMetadata: true, omitRefreshToken: true,
		rejectRevocation: true,
	})
	app := newAzureTestApp(t, github.URL)
	cookie, _ := callbackSession(t, app)
	session, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !session.ProviderAccessExpires.IsZero() || !session.ProviderRefreshExpires.IsZero() ||
		time.Until(session.AccessExpires) < 59*time.Minute {
		t.Fatal("callback confused a local session deadline with provider expiry")
	}
	session.AccessExpires = time.Now().Add(-time.Minute)
	if err := app.oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || firstCookie(t, response.Result(), sessionCookieName).MaxAge >= 0 {
		t.Fatal("expired local authority was not denied and cleared")
	}
	if _, err := app.oauth.loadSession(t.Context(), cookie.Value); err == nil {
		t.Fatal("cleanup staging retained active authority")
	}
	pending, err := app.oauth.revocations.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
	if err != nil || pending.Value == "" {
		t.Fatalf("no-refresh expiry abandoned cleanup: %v", err)
	}
	app.oauth.retryPendingRevocations(t.Context(), 1)
	stillPending, err := app.oauth.revocations.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
	if err != nil || stillPending.Value != pending.Value {
		t.Fatal("provider outage discarded unknown-expiry cleanup")
	}
	github.rejectRevocation = false
	app.oauth.retryPendingRevocations(t.Context(), 1)
	if !github.sawRevocation("access-old") {
		t.Fatal("unknown-expiry credential was not revoked after recovery")
	}
}

func TestOAuthDeniedCallbackQueuesUnknownExpiryBeyondLocalDeadlines(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{
		membershipState: "inactive", omitExpiryMetadata: true, rejectRevocation: true,
	})
	app := newAzureTestApp(t, github.URL)
	stateCookie, state := loginState(t, app)
	if response := oauthCallbackResponse(t, app, stateCookie, state); response.Code != http.StatusForbidden {
		t.Fatalf("callback status = %d", response.Code)
	}
	pending, err := app.oauth.revocations.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
	if err != nil || pending.Value == "" {
		t.Fatalf("callback credentials not retained: %v", err)
	}
	plain, err := app.oauth.open(pending.Value)
	if err != nil {
		t.Fatal(err)
	}
	var session oauthSession
	if err := json.Unmarshal(plain, &session); err != nil {
		t.Fatal(err)
	}
	if !session.ProviderAccessExpires.IsZero() || !session.ProviderRefreshExpires.IsZero() {
		t.Fatal("missing provider metadata became confirmed expiry")
	}
	session.AccessExpires, session.RefreshExpires = time.Now().Add(-sessionTTL), time.Now().Add(-time.Hour)
	if err := app.oauth.completeRevocation(t.Context(), session.ID, pending.Value); err != nil {
		t.Fatal(err)
	}
	if err := app.oauth.queueRevocation(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	github.rejectRevocation = false
	app.oauth.retryPendingRevocations(t.Context(), 1)
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatal("local expiry was mistaken for provider expiry in queued callback cleanup")
	}
}

func TestOAuthCredentialExpiryRequiresEvidenceForEveryPresentToken(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, test := range []struct {
		name    string
		session oauthSession
		expired bool
	}{
		{"legacy deadlines are not evidence", oauthSession{AccessToken: "a", AccessExpires: past, RefreshExpires: past}, false},
		{"unknown access", oauthSession{AccessToken: "a", RefreshToken: "r", ProviderRefreshExpires: past}, false},
		{"unknown refresh", oauthSession{AccessToken: "a", RefreshToken: "r", ProviderAccessExpires: past}, false},
		{"access remains valid", oauthSession{AccessToken: "a", RefreshToken: "r", ProviderAccessExpires: future, ProviderRefreshExpires: past}, false},
		{"refresh remains valid", oauthSession{AccessToken: "a", RefreshToken: "r", ProviderAccessExpires: past, ProviderRefreshExpires: future}, false},
		{"both confirmed expired", oauthSession{AccessToken: "a", RefreshToken: "r", ProviderAccessExpires: past, ProviderRefreshExpires: past}, true},
		{"absent refresh ignored", oauthSession{AccessToken: "a", ProviderAccessExpires: past}, true},
		{"absent access ignored", oauthSession{RefreshToken: "r", ProviderRefreshExpires: past}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := credentialsExpired(test.session); got != test.expired {
				t.Fatalf("expired = %t, want %t", got, test.expired)
			}
		})
	}
}

func TestOAuthRefreshExpiryTracksCredentialIdentity(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name         string
		refreshToken string
		retains      bool
	}{
		{"omitted refresh token", "", true},
		{"same refresh token", "old-refresh", true},
		{"rotated unknown refresh token", "new-refresh", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := oauthSession{
				RefreshToken: "old-refresh", RefreshExpires: now.Add(time.Hour),
				ProviderRefreshExpires: now.Add(time.Hour), ProviderAccessExpires: now.Add(time.Hour),
			}
			session.applyTokens(tokenResponse{AccessToken: "new-access", RefreshToken: test.refreshToken}, now)
			if !session.ProviderAccessExpires.IsZero() || session.AccessExpires != now.Add(time.Hour) {
				t.Fatal("new unknown-expiry access token inherited provider expiry")
			}
			if test.retains {
				if session.ProviderRefreshExpires != now.Add(time.Hour) || session.RefreshToken != "old-refresh" {
					t.Fatal("reused refresh credential lost its confirmed expiry")
				}
			} else if !session.ProviderRefreshExpires.IsZero() || session.RefreshExpires != now.Add(sessionTTL) {
				t.Fatal("rotated unknown-expiry credential inherited its predecessor's expiry")
			}
		})
	}
	session := oauthSession{}
	session.applyTokens(tokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 3600, RefreshTokenExpiresIn: 7200}, now)
	if session.ProviderAccessExpires != now.Add(time.Hour) || session.ProviderRefreshExpires != now.Add(2*time.Hour) {
		t.Fatal("valid provider expiry was not recorded")
	}
	for _, seconds := range []int64{0, -1, 1<<63 - 1} {
		if !providerTokenExpiry(now, seconds).IsZero() {
			t.Fatal("invalid or overflowing metadata proved provider expiry")
		}
	}
}

func TestOAuthRevocationRestartKeyRotationAndConfirmedExpiry(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{})
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config := validOAuthConfig(github.URL)
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	old := newGitHubOAuth(*config, store, store, store)
	past := time.Now().Add(-sessionTTL)
	if err := old.queueRevocation(t.Context(), oauthSession{
		AccessToken: "unknown-access", RefreshToken: "unknown-refresh", AccessExpires: past, RefreshExpires: past,
	}); err != nil {
		t.Fatal(err)
	}
	config.PreviousSessionSecret = config.SessionSecret
	config.SessionSecret = strings.Repeat("new-key", 8)
	withoutPrevious := *config
	withoutPrevious.PreviousSessionSecret = ""
	newGitHubOAuth(withoutPrevious, store, store, store).retryPendingRevocations(t.Context(), 1)
	if pending, err := store.PendingRevocation(t.Context(), ""); err != nil || pending.Value == "" {
		t.Fatal("missing decryption key discarded cleanup")
	}
	restarted := newGitHubOAuth(*config, store, store, store)
	restarted.retryPendingRevocations(t.Context(), 1)
	if !github.sawRevocation("unknown-access") || !github.sawRevocation("unknown-refresh") {
		t.Fatal("restarted worker could not revoke previous-key unknown-expiry credentials")
	}
	if err := restarted.queueRevocation(t.Context(), oauthSession{
		AccessToken: "expired-access", RefreshToken: "expired-refresh",
		ProviderAccessExpires: past, ProviderRefreshExpires: past,
	}); err != nil {
		t.Fatal(err)
	}
	restarted.retryPendingRevocations(t.Context(), 1)
	if pending, err := store.PendingRevocation(t.Context(), ""); err != nil || pending.Value != "" {
		t.Fatal("confirmed provider expiry did not release cleanup capacity")
	}
	if github.sawRevocation("expired-access") || github.sawRevocation("expired-refresh") {
		t.Fatal("confirmed expired credentials required a provider call")
	}
}

type oauthInvalidationHook struct {
	operational.SessionInvalidator
	before func()
	err    error
}

func (hook oauthInvalidationHook) InvalidateSession(ctx context.Context, id, expected, namespace string) (string, error) {
	if hook.before != nil {
		hook.before()
	}
	if hook.err != nil {
		return "", hook.err
	}
	return hook.SessionInvalidator.InvalidateSession(ctx, id, expected, namespace)
}

type oauthQueueOutage struct {
	operational.RevocationQueue
	attempts int
}

func (queue *oauthQueueOutage) QueueRevocation(ctx context.Context, id, value, namespace string) error {
	queue.attempts++
	if queue.attempts <= 2 {
		return operational.ErrCapacity
	}
	return queue.RevocationQueue.QueueRevocation(ctx, id, value, namespace)
}

func TestOAuthCanceledCallbackRetainsOwnerUntilQueueRecovery(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{
		membershipState: "inactive", omitExpiryMetadata: true, rejectRevocation: true,
	})
	app := newAzureTestApp(t, github.URL)
	queue := &oauthQueueOutage{RevocationQueue: app.oauth.revocations}
	app.oauth.revocations = queue
	var branches []string
	app.oauth.log = func(_ context.Context, branch string) { branches = append(branches, branch) }
	stateCookie, state := loginState(t, app)
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code&state="+state)
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	request.AddCookie(stateCookie)
	github.onUserRequest = cancel
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || queue.attempts != 3 ||
		!strings.Contains(strings.Join(branches, "\n"), "credential_cleanup.retained") {
		t.Fatal("callback released its owner before persistence recovered")
	}
	pending, err := queue.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
	if err != nil || pending.Value == "" {
		t.Fatal("cancellation or protected-capacity failure abandoned unknown-expiry cleanup")
	}
	if len(app.oauth.exchanges) != 0 {
		t.Fatal("durable cleanup did not release the exchange reservation")
	}
}

func TestOAuthCleanupBackpressurePrecedesTokenExchange(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active"})
	app := newAzureTestApp(t, github.URL)
	for range cap(app.oauth.exchanges) {
		if !app.oauth.acquireExchange(t.Context()) {
			t.Fatal("could not fill exchange reservations")
		}
	}
	stateCookie, state := loginState(t, app)
	response := oauthCallbackResponse(t, app, stateCookie, state)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("cleanup capacity did not backpressure new token exchange")
	}
	for range cap(app.oauth.exchanges) {
		app.oauth.releaseExchange()
	}
}

func TestOAuthNoRefreshInvalidationPreservesConcurrentRefreshAndStorageFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"concurrent refresh", nil},
		{"capacity", operational.ErrCapacity},
		{"outage", operational.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := fakeGitHub(t, fakeGitHubOptions{})
			store, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			config := validOAuthConfig(github.URL)
			oauth := newGitHubOAuth(*config, store, store, store)
			var branches []string
			oauth.log = func(_ context.Context, branch string) { branches = append(branches, branch) }
			session := oauthSession{ID: "s", AccessToken: "old", AccessExpires: time.Now().Add(-time.Hour)}
			if err := oauth.saveSession(t.Context(), session); err != nil {
				t.Fatal(err)
			}
			hook := oauthInvalidationHook{SessionInvalidator: store, err: test.err}
			if test.err == nil {
				hook.before = func() {
					session.AccessToken, session.RefreshToken = "new", "new-refresh"
					session.AccessExpires = time.Now().Add(time.Hour)
					if err := oauth.saveSession(t.Context(), session); err != nil {
						t.Fatal(err)
					}
				}
			}
			oauth.invalidator = hook
			request := azureRequest(t, http.MethodGet, "/api/auth/session")
			request.AddCookie(&http.Cookie{
				Name: sessionCookieName, Value: session.ID, Secure: true,
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			if _, ok := oauth.session(httptest.NewRecorder(), request); ok {
				t.Fatal("failed or superseded invalidation restored expired request authority")
			}
			retained, err := oauth.loadSession(t.Context(), session.ID)
			if err != nil || retained.AccessToken != session.AccessToken {
				t.Fatal("failed staging or stale request removed the current cleanup owner")
			}
			pending, err := store.PendingRevocation(t.Context(), "")
			if err != nil || pending.Value != "" {
				t.Fatal("failed staging or superseded invalidation queued the wrong credential")
			}
			if test.err != nil && !strings.Contains(strings.Join(branches, "\n"), "invalidation.revocation_staging_failed") {
				t.Fatal("storage failure was not reported")
			}
		})
	}
}
