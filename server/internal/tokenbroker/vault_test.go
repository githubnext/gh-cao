package tokenbroker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type vaultTestTransport func(*http.Request) (*http.Response, error)

func (f vaultTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func vaultTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func vaultTestToken() string {
	return fmt.Sprintf(`{"access_token":"identity-token","token_type":"Bearer","expires_on":"%d"}`, time.Now().Add(time.Hour).Unix())
}

func TestKeyVaultPrivateKey(t *testing.T) {
	for _, appService := range []bool{false, true} {
		t.Run(fmt.Sprint(appService), func(t *testing.T) {
			// A missing endpoint selects IMDS, whereas an explicitly empty one
			// is an invalid App Service configuration.
			unsetVaultIdentityEnvironment(t)
			if appService {
				t.Setenv("IDENTITY_ENDPOINT", "http://127.0.0.1:8081/identity/token")
				t.Setenv("IDENTITY_HEADER", "deployment-header")
			}
			calls := 0
			client := &http.Client{Transport: vaultTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls%2 == 1 {
					if r.URL.Query().Get("resource") != "https://vault.azure.net" || r.URL.Query().Get("client_id") != "selected-client" {
						t.Fatalf("incorrect identity query: %v", r.URL)
					}
					if appService {
						if r.URL.Host != "127.0.0.1:8081" || r.Header.Get("X-IDENTITY-HEADER") != "deployment-header" || r.URL.Query().Get("api-version") != "2019-08-01" {
							t.Fatal("incorrect App Service request")
						}
					} else if r.URL.Host != "169.254.169.254" || r.URL.Path != "/metadata/identity/oauth2/token" || r.Header.Get("Metadata") != "true" || r.URL.Query().Get("api-version") != "2018-02-01" {
						t.Fatal("incorrect IMDS request")
					}
					return vaultTestResponse(200, vaultTestToken()), nil
				}
				if r.URL.String() != "https://example.vault.azure.net/secrets/signing-key/version-1?api-version=7.4" || r.Header.Get("Authorization") != "Bearer "+"identity-token" || r.Header.Get("X-IDENTITY-HEADER") != "" {
					t.Fatal("incorrect secret request")
				}
				return vaultTestResponse(200, fmt.Sprintf(`{"value":"key-%d\n"}`, calls)), nil
			})}
			vault := KeyVault{SecretURL: "https://example.vault.azure.net/secrets/signing-key/version-1", ClientID: "selected-client", HTTPClient: client} // #nosec G101 -- Offline fixture, not a credential.
			for _, expected := range []string{"key-2\n", "key-4\n"} {
				key, err := vault.PrivateKey(context.Background())
				if err != nil || string(key) != expected {
					t.Fatalf("PrivateKey = %q, %v", key, err)
				}
			}
			if calls != 4 {
				t.Fatalf("expected fresh identity and secret reads: %d", calls)
			}
			if client.CheckRedirect != nil || client.Timeout != 0 {
				t.Fatal("caller client was mutated")
			}
		})
	}
}

func TestKeyVaultRejectsUnsafeSecretURLs(t *testing.T) {
	for _, secretURL := range []string{
		"", "http://example.vault.azure.net/secrets/key", "https://evil.example/secrets/key",
		"https://example.vault.azure.net.evil.example/secrets/key",
		"https://" + "user" + "@" + "example.vault.azure.net/secrets/key",
		"https://example.vault.azure.net:443/secrets/key", "https://example.vault.azure.net:/secrets/key",
		"https://example.vault.azure.net/secrets/key?x=y", "https://example.vault.azure.net/secrets/key?",
		"https://example.vault.azure.net/secrets/key#fragment", "https://example.vault.azure.net/keys/key",
		"https://example.vault.azure.net/secrets/key#",
		"https://example.vault.azure.net/secrets/key/", "https://example.vault.azure.net/secrets/../key",
		"https://example.vault.azure.net/secrets/%6bey", "https://example.vault.azure.net/secrets/key/version/extra",
		"https://bad--vault.vault.azure.net/secrets/key", "https://127.0.0.1/secrets/key",
	} {
		t.Run(secretURL, func(t *testing.T) {
			calls := 0
			vault := KeyVault{SecretURL: secretURL, HTTPClient: &http.Client{Transport: vaultTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("must not request")
			})}}
			if key, err := vault.PrivateKey(context.Background()); err == nil || key != nil || calls != 0 {
				t.Fatalf("unsafe URL accepted: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestKeyVaultRejectsUnsafeIdentityEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"", "https://127.0.0.1/token", "http://localhost/token", "http://169.254.169.254/token",
		"http://evil.example/token", "http://127.0.0.1.evil.example/token", "http://user@127.0.0.1/token",
		"http://127.0.0.1/token?x=y", "http://127.0.0.1/token?", "http://127.0.0.1/token#x",
		"http://127.0.0.1/token#",
		"http://127.0.0.1:0/token", "http://127.0.0.1:65536/token", "http://127.0.0.1:/token",
		"http://169.254.255.3:8081/msi/token", "http://169.254.255.2:8081/other",
		"http://10.0.0.1:8081/msi/token", "http://192.168.1.1:8081/msi/token",
		"http://8.8.8.8:8081/msi/token", "http://[::ffff:169.254.255.2]:8081/msi/token",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("IDENTITY_ENDPOINT", endpoint)
			t.Setenv("IDENTITY_HEADER", "deployment-header")
			calls := 0
			vault := KeyVault{SecretURL: "https://example.vault.azure.net/secrets/key", HTTPClient: &http.Client{Transport: vaultTestTransport(func(*http.Request) (*http.Response, error) { // #nosec G101 -- Offline fixture, not a credential.
				calls++
				return nil, errors.New("must not request")
			})}}
			if _, err := vault.PrivateKey(context.Background()); err == nil || calls != 0 {
				t.Fatalf("unsafe identity endpoint accepted: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestKeyVaultIdentityConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		endpoint string
		header   string
		valid    bool
	}{
		{name: "IPv6 loopback", endpoint: "http://[::1]:8080/identity/token", header: "header", valid: true},
		{name: "loopback without port", endpoint: "http://127.0.0.1/token", header: "header", valid: true},
		{name: "App Service platform endpoint", endpoint: "http://169.254.255.2:8081/msi/token", header: "deployment-header", valid: true},
		{name: "missing header", endpoint: "http://127.0.0.1/token"},
		{name: "missing endpoint", header: "header"},
		{name: "invalid header", endpoint: "http://127.0.0.1/token", header: "header\r\nbad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			unsetVaultIdentityEnvironment(t)
			if test.endpoint != "" {
				t.Setenv("IDENTITY_ENDPOINT", test.endpoint)
			}
			t.Setenv("IDENTITY_HEADER", test.header)
			calls := 0
			client := &http.Client{Transport: vaultTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if r.URL.Query().Has("client_id") {
						t.Fatal("unexpected client selection")
					}
					if r.URL.Scheme+"://"+r.URL.Host+r.URL.Path != test.endpoint ||
						r.Header.Get("X-IDENTITY-HEADER") != test.header ||
						r.URL.Query().Get("resource") != "https://vault.azure.net" ||
						r.URL.Query().Get("api-version") != "2019-08-01" {
						t.Fatal("incorrect App Service endpoint or authentication")
					}
					return vaultTestResponse(200, fmt.Sprintf(`{"access_token":"token","expires_on":%d}`, time.Now().Add(time.Hour).Unix())), nil
				}
				return vaultTestResponse(200, `{"value":"key"}`), nil
			})}
			v := KeyVault{SecretURL: "https://example.vault.azure.net/secrets/key", HTTPClient: client} // #nosec G101 -- Offline fixture, not a credential.
			key, err := v.PrivateKey(context.Background())
			if test.valid {
				if err != nil || string(key) != "key" || calls != 2 {
					t.Fatalf("valid configuration failed: key=%q err=%v calls=%d", key, err, calls)
				}
			} else if err == nil || key != nil || calls != 0 {
				t.Fatalf("invalid configuration accepted: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestKeyVaultFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name        string
		identity    string
		secret      string
		identityErr bool
		secretErr   bool
		status      int
		secretStage bool
	}{
		{name: "malformed identity JSON", identity: "sensitive upstream message"},
		{name: "empty token", identity: `{"access_token":"","expires_on":9999999999}`},
		{name: "whitespace token", identity: `{"access_token":"token value","expires_on":9999999999}`},
		{name: "header injection token", identity: `{"access_token":"token\r\nInjected: true","expires_on":9999999999}`},
		{name: "expired token", identity: `{"access_token":"token","expires_on":1}`},
		{name: "missing expiry", identity: `{"access_token":"token"}`},
		{name: "malformed expiry", identity: `{"access_token":"token","expires_on":"tomorrow"}`},
		{name: "wrong token type", identity: `{"access_token":"token","token_type":"Basic","expires_on":9999999999}`},
		{name: "oversized identity", identity: strings.Repeat("x", vaultResponseLimit+1)},
		{name: "identity transport error", identityErr: true},
		{name: "secret transport error", secretErr: true, secretStage: true},
		{name: "malformed secret JSON", secret: "sensitive upstream message", secretStage: true},
		{name: "empty secret", secret: `{"value":""}`, secretStage: true}, // #nosec G101 -- Empty response fixture, not a credential.
		{name: "missing secret", secret: `{}`, secretStage: true},
		{name: "oversized secret", secret: `{"value":"` + strings.Repeat("x", vaultResponseLimit) + `"}`, secretStage: true},
		{name: "identity denied", status: 403},
		{name: "secret denied", status: 403, secretStage: true},
		{name: "identity redirect", status: 302},
		{name: "secret redirect", status: 307, secretStage: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			unsetVaultIdentityEnvironment(t)
			calls := 0
			client := &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error {
					t.Fatal("injected redirect policy must not run")
					return nil
				},
				Transport: vaultTestTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls > 2 || r.URL.Host == "evil.example" {
						t.Fatal("unexpected request or followed redirect")
					}
					isSecret := calls == 2
					if !isSecret && test.secretStage {
						return vaultTestResponse(200, vaultTestToken()), nil
					}
					if test.identityErr || test.secretErr {
						return nil, errors.New("sensitive upstream message")
					}
					body := test.identity
					if isSecret {
						body = test.secret
					}
					status := test.status
					if status == 0 {
						status = 200
					}
					response := vaultTestResponse(status, body)
					response.Header.Set("Location", "https://evil.example/steal")
					return response, nil
				}),
			}
			vault := KeyVault{SecretURL: "https://example.vault.azure.net/secrets/key", HTTPClient: client} // #nosec G101 -- Offline fixture, not a credential.
			key, err := vault.PrivateKey(context.Background())
			if err == nil || key != nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "identity-token") {
				t.Fatalf("expected sanitized failure, got %q, %v", key, err)
			}
			expectedCalls := 1
			if test.secretStage {
				expectedCalls = 2
			}
			if calls != expectedCalls {
				t.Fatalf("requests = %d, want %d", calls, expectedCalls)
			}
		})
	}
}

func unsetVaultIdentityEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("IDENTITY_ENDPOINT", "")
	t.Setenv("IDENTITY_HEADER", "")
	// t.Setenv restores the original values even though this test needs the
	// endpoint absent, rather than present with an empty value.
	if err := os.Unsetenv("IDENTITY_ENDPOINT"); err != nil {
		t.Fatal(err)
	}
}
