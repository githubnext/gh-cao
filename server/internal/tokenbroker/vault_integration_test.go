package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/simulator"
)

// The injected transport maps only these fixture authorities to a real local
// HTTP server. Production HTTPS validation and redirect rejection still run.
func TestAzureKeyVaultHTTPIntegration(t *testing.T) {
	for _, identity := range []string{"App Service", "IMDS"} {
		t.Run(identity, func(t *testing.T) {
			unsetVaultIdentityEnvironment(t)
			const secretURL = "https://example.vault.azure.net/secrets/signing-key" // #nosec G101 -- synthetic secret reference, not a credential.
			const appEndpoint = "http://127.0.0.1:8081/identity/token"
			const imdsEndpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
			endpoint := imdsEndpoint
			if identity == "App Service" {
				endpoint = appEndpoint
				t.Setenv("IDENTITY_ENDPOINT", endpoint)
				t.Setenv("IDENTITY_HEADER", "synthetic-deployment-header")
			}
			var mu sync.Mutex
			value, identityStatus, secretStatus := "synthetic-key-v1", http.StatusOK, http.StatusOK
			expiry := time.Now().Add(time.Hour).Unix()
			identityReads, secretReads := 0, 0
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet {
					http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Host == "example.vault.azure.net" && r.URL.Path == "/secrets/signing-key" {
					secretReads++
					if r.Header.Get("Authorization") != "Bearer synthetic-identity-token" ||
						r.Header.Get("X-IDENTITY-HEADER") != "" || r.Header.Get("Metadata") != "" ||
						r.URL.RawQuery != "api-version=7.4" {
						t.Error("secret request violated the vault contract")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.Header().Set("Location", "https://untrusted.invalid/secret")
					w.WriteHeader(secretStatus)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
					return
				}
				if r.Host+r.URL.Path != strings.TrimPrefix(endpoint, "http://") {
					t.Error("unexpected identity authority or path")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				identityReads++
				version := "2018-02-01"
				if identity == "App Service" {
					version = "2019-08-01"
					if r.Header.Get("X-IDENTITY-HEADER") != "synthetic-deployment-header" {
						t.Error("missing App Service identity header")
					}
				} else if r.Header.Get("Metadata") != "true" {
					t.Error("missing IMDS metadata header")
				}
				if r.Header.Get("Authorization") != "" || r.URL.Query().Get("resource") != "https://vault.azure.net" ||
					r.URL.Query().Get("client_id") != "synthetic-client" || r.URL.Query().Get("api-version") != version {
					t.Error("identity request violated its audience/client contract")
				}
				w.Header().Set("Location", "https://untrusted.invalid/identity")
				w.WriteHeader(identityStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "synthetic-identity-token", "token_type": "Bearer", "expires_on": expiry,
				})
			}))
			t.Cleanup(local.Close)
			transport, err := simulator.NewLocalTransport(local.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: vaultTestTransport(func(r *http.Request) (*http.Response, error) {
				authority := r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
				if authority != endpoint && authority != secretURL {
					return nil, errors.New("fixture blocked unknown authority")
				}
				mapped := r.Clone(r.Context())
				url := *r.URL
				mapped.URL = &url
				mapped.URL.Scheme = "http"
				mapped.URL.Host = strings.TrimPrefix(local.URL, "http://")
				mapped.Host = r.URL.Host
				return transport.RoundTrip(mapped)
			})}
			var identitySource ManagedIdentityTokenSource = ManagedIdentity{ClientID: "synthetic-client", HTTPClient: client}
			var vault PrivateKeyReader = KeyVault{SecretURL: secretURL, HTTPClient: client, Identity: identitySource}
			for _, version := range []string{"synthetic-key-v1", "synthetic-key-v2"} {
				mu.Lock()
				value = version
				mu.Unlock()
				key, err := vault.PrivateKey(t.Context())
				if err != nil || string(key) != version {
					t.Fatalf("fresh secret read failed: %v", err)
				}
			}
			mu.Lock()
			if identityReads != 2 || secretReads != 2 {
				t.Error("rotation must reread both identity and secret")
			}
			mu.Unlock()
			for _, failure := range []struct {
				name                   string
				identityStatus, status int
				expired                bool
				empty                  bool
				secretRequests         int
			}{
				{"identity denied", 403, 200, false, false, 0},
				{"identity unavailable", 503, 200, false, false, 0},
				{"identity redirect", 307, 200, false, false, 0},
				{"expired identity", 200, 200, true, false, 0},
				{"secret denied", 200, 403, false, false, 1},
				{"secret missing", 200, 404, false, false, 1},
				{"secret unavailable", 200, 503, false, false, 1},
				{"secret redirect", 200, 307, false, false, 1},
				{"empty secret", 200, 200, false, true, 1},
			} {
				t.Run(failure.name, func(t *testing.T) {
					mu.Lock()
					identityStatus, secretStatus = failure.identityStatus, failure.status
					value, expiry = "synthetic-sensitive-upstream", time.Now().Add(time.Hour).Unix()
					if failure.expired {
						expiry = 1
					}
					if failure.empty {
						value = ""
					}
					beforeIdentity, beforeSecret := identityReads, secretReads
					mu.Unlock()
					key, err := vault.PrivateKey(t.Context())
					if err == nil || key != nil || strings.Contains(err.Error(), "synthetic-") {
						t.Fatal("upstream failure must return a sanitized error without a key")
					}
					mu.Lock()
					defer mu.Unlock()
					if identityReads != beforeIdentity+1 || secretReads != beforeSecret+failure.secretRequests {
						t.Error("failure followed a redirect or continued after identity rejection")
					}
				})
			}
			if client.Timeout != 0 || client.CheckRedirect != nil {
				t.Fatal("KeyVault mutated the injected client")
			}
			t.Run("cancelled read", func(t *testing.T) {
				ctx := t.Context()
				// An already expired context must not make a network request.
				ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				if key, err := vault.PrivateKey(ctx); err == nil || key != nil {
					t.Fatalf("cancelled read returned a key: %v", err)
				}
			})
		})
	}
}
