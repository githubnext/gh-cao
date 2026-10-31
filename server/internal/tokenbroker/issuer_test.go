package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInstallationMustMatchTrustedTenant(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"wrong-app", `{"app_id":8,"account":{"id":42,"type":"Organization"},"suspended_at":null,"permissions":{"copilot_requests":"read"}}`},
		{"wrong-owner", `{"app_id":7,"account":{"id":99,"type":"Organization"},"suspended_at":null,"permissions":{"copilot_requests":"read"}}`},
		{"personal-account", `{"app_id":7,"account":{"id":42,"type":"User"},"suspended_at":null,"permissions":{"copilot_requests":"read"}}`},
		{"suspended", `{"app_id":7,"account":{"id":42,"type":"Organization"},"suspended_at":"2026-10-30T00:00:00Z","permissions":{"copilot_requests":"read"}}`},
		{"missing-suspension-evidence", `{"app_id":7,"account":{"id":42,"type":"Organization"},"permissions":{"copilot_requests":"read"}}`},
		{"missing-permission", `{"app_id":7,"account":{"id":42,"type":"Organization"},"suspended_at":null,"permissions":{"issues":"write"}}`},
		{"write-not-read", `{"app_id":7,"account":{"id":42,"type":"Organization"},"suspended_at":null,"permissions":{"copilot_requests":"write"}}`},
		{"duplicate-security-field", `{"app_id":7,"app_id":8,"account":{"id":42,"type":"Organization"},"suspended_at":null,"permissions":{"copilot_requests":"read"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.installation = tt.body
			if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 || f.mints != 0 {
				t.Fatal("installation evidence failed open")
			}
		})
	}
}

func TestIssuedTokenValidationRevokesRejectedResults(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any, *fixture)
	}{
		{"scope", func(result map[string]any, _ *fixture) {
			result["permissions"] = map[string]string{"copilot_requests": "read", "issues": "write"}
		}},
		{"missing-permission", func(result map[string]any, _ *fixture) {
			result["permissions"] = map[string]string{"metadata": "read"}
		}},
		{"write", func(result map[string]any, _ *fixture) {
			result["permissions"] = map[string]string{"copilot_requests": "write"}
		}},
		{"near-expiry", func(result map[string]any, f *fixture) {
			result["expires_at"] = f.now.Add(time.Minute).Format(time.RFC3339)
		}},
		{"too-long", func(result map[string]any, f *fixture) {
			result["expires_at"] = f.now.Add(time.Hour + time.Second).Format(time.RFC3339)
		}},
		{"unknown-expiry", func(result map[string]any, _ *fixture) { delete(result, "expires_at") }},
		{"bad-expiry", func(result map[string]any, _ *fixture) { result["expires_at"] = "not-a-time" }},
		{"missing-repository", func(result map[string]any, _ *fixture) { delete(result, "repositories") }},
		{"other-repository", func(result map[string]any, _ *fixture) {
			result["repositories"] = []any{map[string]any{"id": 1002, "owner": map[string]any{"id": 42}}}
		}},
		{"other-owner", func(result map[string]any, _ *fixture) {
			result["repositories"] = []any{map[string]any{"id": 1001, "owner": map[string]any{"id": 99}}}
		}},
		{"too-many-repositories", func(result map[string]any, _ *fixture) {
			result["repositories"] = []any{
				map[string]any{"id": 1001, "owner": map[string]any{"id": 42}},
				map[string]any{"id": 1002, "owner": map[string]any{"id": 42}},
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			original := f.result
			f.result = func() string {
				var result map[string]any
				if err := json.Unmarshal([]byte(original()), &result); err != nil {
					t.Fatal(err)
				}
				tt.edit(result, f)
				data, _ := json.Marshal(result)
				return string(data)
			}
			if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 || f.mints != 1 || len(f.revoked) != 1 {
				t.Fatal("invalid upstream token was returned or orphaned")
			}
		})
	}
}

func TestRejectedCredentialRetainedOnCleanupFailure(t *testing.T) {
	f := newFixture(t)
	f.result = func() string {
		return `{"token":"test-rejected","expires_at":"bad","permissions":{"copilot_requests":"write"}}`
	}
	f.revokeFails = true
	if w := f.call(exchangeBody); w.Code != 503 {
		t.Fatal(w.Code)
	}
	state := f.service.contexts[executionKey(f.identity)]
	if len(state.tokens) != 1 {
		t.Fatal("failed cleanup lost rejected token")
	}
	f.revokeFails = false
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
		t.Fatal("rejected credential cleanup was not retried")
	}
}

func TestUpstreamTransportFailsClosed(t *testing.T) {
	for _, status := range []int{301, 302, 307, 400, 401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFixture(t)
			calls := 0
			f.service.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.github.com" {
					t.Error("followed attacker redirect")
				}
				res := testResponse(status, "untrusted upstream response")
				res.Header.Set("Location", "https://evil.example/steal")
				return res, nil
			})
			if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 || calls != 1 {
				t.Fatal("upstream failure leaked or redirected")
			}
		})
	}
	f := newFixture(t)
	f.service.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(200, strings.Repeat("x", githubAPIBodyLimit+1)), nil
	})
	if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 {
		t.Fatal("oversized upstream response accepted")
	}
	f.service.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private upstream error")
	})
	if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 {
		t.Fatal("transport error leaked")
	}
}

func TestUpstreamUsesRequestDeadline(t *testing.T) {
	f := newFixture(t)
	f.service.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > requestTimeout {
			t.Error("issuer not bounded by request deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if _, err := f.service.issue(ctx, f.identity); err == nil {
		t.Fatal("canceled context minted credential")
	}
}

func TestCompleteEscapedCredentialResponseIsBounded(t *testing.T) {
	for _, character := range []string{`"`, `\`, `<`} {
		for _, boundary := range []string{"fits", "exceeds", "token-limit"} {
			t.Run(character+"/"+boundary, func(t *testing.T) {
				f := newFixture(t)
				empty, err := json.Marshal(credential{ExpiresAt: f.now.Add(50 * time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				unit, err := json.Marshal(character)
				if err != nil {
					t.Fatal(err)
				}
				length := (bodyLimit - len(empty)) / (len(unit) - 2)
				switch boundary {
				case "exceeds":
					length++
				case "token-limit":
					length = bodyLimit / 2
				}
				token := strings.Repeat(character, length)
				original := f.result
				f.result = func() string {
					var result map[string]any
					if err := json.Unmarshal([]byte(original()), &result); err != nil {
						t.Fatal(err)
					}
					result["token"] = token
					body, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					return string(body)
				}
				w := f.call(exchangeBody)
				if boundary == "fits" {
					if w.Code != 200 || w.Body.Len() > bodyLimit {
						t.Fatalf("bounded escaped credential failed: %d bytes=%d", w.Code, w.Body.Len())
					}
					return
				}
				if w.Code != 503 || w.Body.Len() != 0 || len(f.revoked) != 1 || f.revoked[0] != token {
					t.Fatal("oversized encoded credential was returned or orphaned")
				}
			})
		}
	}
}

func TestUnauthorizedHeldCredentialCompletesIdempotentRevocation(t *testing.T) {
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	transport := f.service.client.Transport
	revocations := 0
	f.service.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			revocations++
			if r.URL.String() != githubAPI+"/installation/token" || r.Header.Get("Authorization") == "" {
				t.Fatal("cleanup did not authenticate the exact held credential")
			}
			return testResponse(http.StatusUnauthorized, `{"message":"Bad credentials"}`), nil
		}
		return transport.RoundTrip(r)
	})
	if w := f.call(invalidateBody); w.Code != 204 {
		t.Fatal("already-invalid held token prevented cleanup")
	}
	if len(f.service.contexts[executionKey(f.identity)].tokens) != 0 {
		t.Fatal("already-invalid held token remained tracked")
	}
	if w := f.call(invalidateBody); w.Code != 204 || revocations != 1 {
		t.Fatal("already-invalid token cleanup was not idempotent")
	}
}
