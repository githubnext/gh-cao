package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/tokenbroker"
)

type deniedBrokerIdentity struct{}

type brokerDeadlineWriter struct {
	*httptest.ResponseRecorder
	read, write  []time.Time
	boundedFlush bool
}

func (w *brokerDeadlineWriter) Flush() {
	w.boundedFlush = len(w.write) > 0 && !w.write[len(w.write)-1].IsZero()
	w.ResponseRecorder.Flush()
}

func (w *brokerDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.read = append(w.read, deadline)
	return nil
}

func (w *brokerDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.write = append(w.write, deadline)
	return nil
}

func (deniedBrokerIdentity) Verify(context.Context, string) (admission.Identity, error) {
	return admission.Identity{}, admission.ErrDenied
}

func (deniedBrokerIdentity) PrivateKey(context.Context) ([]byte, error) {
	return nil, admission.ErrDenied
}

func TestTokenBrokerEnvironmentOptIn(t *testing.T) {
	for _, name := range []string{
		"ENABLED", "APP_ID", "INSTALLATION_ID", "OWNER_ID", "URL",
		"KEY_VAULT_SECRET_URL", "MANAGED_IDENTITY_CLIENT_ID",
	} {
		t.Setenv("CAO_TOKEN_BROKER_"+name, "")
	}
	config, err := tokenBrokerConfigFromEnv()
	if err != nil || config != nil {
		t.Fatal("unset broker must remain disabled")
	}
	t.Setenv("CAO_TOKEN_BROKER_ENABLED", "yes")
	if _, err := tokenBrokerConfigFromEnv(); err == nil {
		t.Fatal("invalid opt-in was accepted")
	}
	t.Setenv("CAO_TOKEN_BROKER_ENABLED", "true")
	if _, err := tokenBrokerConfigFromEnv(); err == nil {
		t.Fatal("missing identifiers were accepted")
	}
	t.Setenv("CAO_TOKEN_BROKER_APP_ID", "123")
	t.Setenv("CAO_TOKEN_BROKER_INSTALLATION_ID", "456")
	t.Setenv("CAO_TOKEN_BROKER_OWNER_ID", "789")
	t.Setenv("CAO_TOKEN_BROKER_URL", "https://broker.example/api/token-broker")
	t.Setenv("CAO_TOKEN_BROKER_KEY_VAULT_SECRET_URL", "https://cao-vault.vault.azure.net/secrets/copilot-app")
	config, err = tokenBrokerConfigFromEnv()
	if err != nil || config == nil || config.AppID != 123 || config.InstallationID != 456 ||
		config.OwnerID != "789" || config.KeyVaultSecretURL == "" {
		t.Fatal("explicit broker configuration was not loaded")
	}
}

func TestTokenBrokerRejectsUnsafeHosting(t *testing.T) {
	base := Config{
		TokenBroker: &tokenbroker.Config{
			Audience: "https://broker.example/api/token-broker", PolicyFile: "absent-cao.json",
			AppID: 1, InstallationID: 2, OwnerID: "3",
			Verifier: deniedBrokerIdentity{}, KeyReader: deniedBrokerIdentity{},
		},
		HostProfile: hostedHostProfile(), SingleReplicaConfirmed: true,
		TokenBrokerSingleProcessConfirmed: true,
		Proxy:                             ProxyPolicy{RequireHTTPS: true, AllowedHosts: []string{"broker.example"}},
	}
	if _, err := newTokenBroker(base); err != nil {
		t.Fatal("safe explicit broker configuration was rejected")
	}
	for _, test := range []struct {
		name   string
		change func(*Config)
	}{
		{"local bearer", func(c *Config) { c.HostProfile = localHostProfile() }},
		{"plaintext", func(c *Config) { c.Proxy.RequireHTTPS = false }},
		{"external listener", func(c *Config) { c.HostProfile.Listener = HostListenerExternal }},
		{"platform listener", func(c *Config) { c.HostProfile.Listener = HostListenerPlatform }},
		{"unconfirmed replica", func(c *Config) { c.SingleReplicaConfirmed = false }},
		{"unconfirmed process", func(c *Config) { c.TokenBrokerSingleProcessConfirmed = false }},
		{"unreviewed host", func(c *Config) { c.Proxy.AllowedHosts = []string{"other.example"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.change(&config)
			if _, err := newTokenBroker(config); err == nil {
				t.Fatal("unsafe hosting was accepted")
			}
		})
	}
	for _, endpoint := range []string{
		"http://broker.example/api/token-broker",
		"https://broker.example/other",
		"https://broker.example/api/token-broker/",
		"https://broker.example/api/token-broker?x=1",
		"https://broker.example/api/token-broker?",
		"https://broker.example/api/token-broker#x",
		"https://user@broker.example/api/token-broker",
		"https://broker.example:443/api/token-broker",
	} {
		config := base
		brokerConfig := *base.TokenBroker
		brokerConfig.Audience = endpoint
		config.TokenBroker = &brokerConfig
		if _, err := newTokenBroker(config); err == nil {
			t.Fatal("ambiguous broker endpoint was accepted")
		}
	}
}

func TestTokenBrokerDisabledRouteDoesNotUseDashboardCredentials(t *testing.T) {
	app := &App{}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		request := httptest.NewRequestWithContext(t.Context(), method, "https://broker.example/api/token-broker",
			strings.NewReader(`{"token_type":"copilot-request","provider":"github","oidc_token":"invalid"}`))
		request.Header.Set("Authorization", "******")
		request.Header.Set("Cookie", "cao_session=dashboard-session")
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || response.Body.Len() != 0 ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("disabled broker did not return an empty no-store denial")
		}
	}
}

func TestTokenBrokerUsesOIDCInsteadOfDashboardAuthentication(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	broker, err := tokenbroker.New(tokenbroker.Config{
		Audience: "https://broker.example/api/token-broker", PolicyFile: "absent-cao.json",
		AppID: 1, InstallationID: 2, OwnerID: "3", Verifier: deniedBrokerIdentity{},
		KeyReader: deniedBrokerIdentity{},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		services: store.OperationalServices(), tokenBroker: broker,
		config: Config{HostProfile: hostedHostProfile(), Proxy: ProxyPolicy{
			AllowedHosts: []string{"broker.example"}, RequireHTTPS: true,
		}},
	}
	for _, endpoint := range []string{
		"https://broker.example/api/token-broker",
		"http://broker.example/api/token-broker",
		"https://other.example/api/token-broker",
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint,
			strings.NewReader(`{"token_type":"copilot-request","provider":"github","oidc_token":"invalid"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "******")
		request.Header.Set("Cookie", "cao_session=dashboard-session")
		response := &brokerDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		app.Handler().ServeHTTP(response, request)
		want := http.StatusForbidden
		if endpoint == "https://broker.example/api/token-broker" {
			want = http.StatusUnauthorized
			if !response.boundedFlush {
				t.Fatal("broker cleared its transport deadline before flushing the response")
			}
			for _, deadlines := range [][]time.Time{response.read, response.write} {
				if len(deadlines) != 2 || deadlines[0].IsZero() ||
					!deadlines[0].Before(time.Now().Add(9*time.Second)) || !deadlines[1].IsZero() {
					t.Fatal("broker did not bound and reset both transport deadlines")
				}
			}
		}
		if response.Code != want || response.Body.Len() != 0 ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("broker authentication boundary failed: status=%d, want=%d", response.Code, want)
		}
	}
}
