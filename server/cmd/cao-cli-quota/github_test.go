package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func quotaTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	t.Setenv("GH_TOKEN", "quota-test-token")
	t.Setenv("GH_ENTERPRISE_TOKEN", "quota-test-token")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	t.Setenv("GH_PATH", "/not-a-gh-executable")
}

type quotaTestRoundTripper func(*http.Request) (*http.Response, error)

func (f quotaTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func quotaTestTransport(t *testing.T, handler http.HandlerFunc) http.RoundTripper {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return quotaTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		request := r.Clone(r.Context())
		request.Host = r.URL.Host
		request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
		return server.Client().Transport.RoundTrip(request)
	})
}

func TestGhSnapshotResolvesHostAndCredentialsWithGoGH(t *testing.T) {
	quotaTestEnvironment(t)
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("GH_TOKEN", "public-token")
	t.Setenv("GH_ENTERPRISE_TOKEN", "enterprise-token")
	for _, tc := range []struct {
		host, apiHost, prefix, token string
	}{
		{"github.com", "api.github.com", "", "public-token"},
		{"octo.ghe.com", "api.octo.ghe.com", "", "public-token"},
		{"github.example.test", "github.example.test", "/api/v3", "enterprise-token"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			calls := 0
			transport := quotaTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Host != tc.apiHost || r.Method != http.MethodGet ||
					r.Header.Get("Authorization") != "token "+tc.token {
					t.Error("request did not preserve the gh host and credential context")
				}
				switch r.URL.Path {
				case tc.prefix + "/user":
					_, _ = w.Write([]byte(`{"id":123}`))
				case tc.prefix + "/rate_limit":
					_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":4900,"reset":1792070000}}}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			current, err := ghSnapshot(t.Context(), tc.host, transport, "core")
			if err != nil || len(current.observations) != 1 || calls != 2 {
				t.Fatalf("direct API snapshot failed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestBridgeReconcilesGoGHSnapshots(t *testing.T) {
	quotaTestEnvironment(t)
	var core, graphql atomic.Int64
	core.Store(5000)
	graphql.Store(5000)
	transport := quotaTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"id":123}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": map[string]any{
			"core":    map[string]int64{"limit": 5000, "remaining": core.Load(), "reset": 1792070000},
			"graphql": map[string]int64{"limit": 5000, "remaining": graphql.Load(), "reset": 1792070000},
		}})
	})
	b, _ := testBridge(t, 5000)
	b.read = func(ctx context.Context, resources []string) (snapshot, error) {
		return ghSnapshot(ctx, "github.com", transport, resources...)
	}
	first := b.handle(t.Context(), request{Operation: "admit"})
	if first.Capacity != 4 || first.ReservationID == "" {
		t.Fatalf("initial admission: %+v", first)
	}
	core.Store(175)
	completed := b.handle(t.Context(), request{Operation: "complete", ReservationID: first.ReservationID})
	if completed.Capacity != 1 {
		t.Fatalf("fresh observation did not reduce capacity: %+v", completed)
	}
	graphql.Store(0)
	if mixed := b.handle(t.Context(), request{Operation: "admit"}); mixed.Capacity != 0 {
		t.Fatalf("ignored exhausted GraphQL: %+v", mixed)
	}
	dispatch := b.handle(t.Context(), request{Operation: "admit", Resources: []string{"core"}})
	if dispatch.Capacity != 1 || dispatch.ReservationID == "" {
		t.Fatalf("GraphQL blocked core-only dispatch: %+v", dispatch)
	}
	core.Store(0)
	if exhausted := b.handle(t.Context(), request{Operation: "admit", Resources: []string{"core"}}); exhausted.Reason != "exhausted" {
		t.Fatalf("ignored core exhaustion: %+v", exhausted)
	}
}

func TestGhSnapshotRefreshesCredentialSelection(t *testing.T) {
	quotaTestEnvironment(t)
	expected := "quota-test-token"
	transport := quotaTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token "+expected {
			t.Error("snapshot used stale credentials")
		}
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"id":123}`))
		} else {
			_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":4900,"reset":1792070000}}}`))
		}
	})
	if _, err := ghSnapshot(t.Context(), "github.com", transport, "core"); err != nil {
		t.Fatal(err)
	}
	expected = "changed-token"
	t.Setenv("GH_TOKEN", expected)
	if _, err := ghSnapshot(t.Context(), "github.com", transport, "core"); err != nil {
		t.Fatal(err)
	}
}

func TestGhSnapshotFailsClosedWithoutExposingUpstreamErrors(t *testing.T) {
	quotaTestEnvironment(t)
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"unauthorized", "/user", "private upstream error", http.StatusUnauthorized},
		{"rate limited", "/rate_limit", "private upstream error", http.StatusTooManyRequests},
		{"invalid user JSON", "/user", "{private upstream error", http.StatusOK},
		{"missing user ID", "/user", `{}`, http.StatusOK},
		{"oversized response", "/rate_limit", strings.Repeat("x", (1<<20)+1), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := quotaTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				} else {
					_, _ = w.Write([]byte(`{"id":123}`))
				}
			})
			if _, err := ghSnapshot(t.Context(), "github.com", transport, "core"); err == nil ||
				strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "quota-test-token") {
				t.Fatalf("expected sanitized snapshot failure, got %v", err)
			}
		})
	}
}

func TestGhSnapshotRejectsRedirectsAndHonorsCancellation(t *testing.T) {
	quotaTestEnvironment(t)
	calls := 0
	transport := quotaTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/unexpected", http.StatusFound)
	})
	if _, err := ghSnapshot(t.Context(), "github.com", transport); err == nil || calls != 1 {
		t.Fatalf("followed redirect: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ghSnapshot(ctx, "github.com", transport); err == nil || calls != 1 {
		t.Fatalf("ignored cancellation: calls=%d err=%v", calls, err)
	}
}
