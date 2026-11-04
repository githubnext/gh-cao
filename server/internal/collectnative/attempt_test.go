package collectnative

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubNativeExactHistoricalAttempt(t *testing.T) {
	files := fixtureFiles(t)
	files["aw_info.json"] = []byte(`{"repository":"octo/api","run_id":303,"run_attempt":"2"}`)
	archive := zipArtifact(t, files)
	for _, response := range []struct {
		name       string
		repository string
		run        int
		attempt    int
		path       string
		valid      bool
	}{
		{"exact", "octo/api", 303, 2, ".github/workflows/test.lock.yml", true},
		{"latest-attempt", "octo/api", 303, 3, ".github/workflows/test.lock.yml", false},
		{"other-run", "octo/api", 304, 2, ".github/workflows/test.lock.yml", false},
		{"other-repository", "elsewhere/api", 303, 2, ".github/workflows/test.lock.yml", false},
		{"ordinary-workflow", "octo/api", 303, 2, ".github/workflows/test.yml", false},
	} {
		t.Run(response.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-installation-token" {
					t.Error("attempt lookup lost installation-scoped credentials")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4900")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
				switch r.URL.Path {
				case "/repos/octo/api/actions/runs/303/attempts/2":
					_, _ = fmt.Fprintf(w, `{"id":%d,"run_attempt":%d,"path":%q,"repository":{"full_name":%q}}`,
						response.run, response.attempt, response.path, response.repository)
				case "/repos/octo/api/actions/runs/303/artifacts":
					_, _ = w.Write([]byte(`{"artifacts":[{"id":100,"name":"usage","workflow_run":{"id":303}}]}`))
				case "/repos/octo/api/actions/artifacts/100/zip":
					_, _ = w.Write(archive)
				default:
					t.Errorf("exact-attempt task expanded into another acquisition endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
			store := &testEvidenceStore{}
			collector := Collector{Provider: provider, Store: store}
			identity := testIdentity()
			identity.Attempt = 2
			t.Setenv("PATH", "/nonexistent")
			err := collector.CollectAttempt(t.Context(), identity)
			if !response.valid {
				if !errors.Is(err, ErrIdentity) || len(store.published) != 0 || store.released != 0 {
					t.Fatalf("mismatched attempt reached safe publication: %v", err)
				}
				return
			}
			if err != nil || len(store.published) != 1 || store.published[0].Identity != identity || store.released != 1 {
				t.Fatalf("exact historical attempt was not acquired without executables: %v", err)
			}
			if store.published[0].Run["cao_unified_session"] == nil {
				t.Fatal("attempt identity was not propagated through session extraction")
			}
		})
	}
}

func TestNativeExactAttemptRejectsUnsupportedProviderAndCancellation(t *testing.T) {
	store := &testEvidenceStore{}
	collector := Collector{Provider: staticProvider{}, Store: store}
	if err := collector.CollectAttempt(t.Context(), testIdentity()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing exact-attempt lookup silently fell back to repository discovery")
	}
	provider := GitHub{Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.Attempt(ctx, testIdentity()); err == nil {
		t.Fatal("canceled exact-attempt lookup reached the provider")
	}
	if len(store.published) != 0 || store.released != 0 {
		t.Fatal("unsupported exact-attempt lookup reached claim or publication")
	}
}

func TestGitHubNativeExactAttemptDoesNotExposeRawFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("PRIVATE_PROVIDER_DETAIL"))
	}))
	defer server.Close()
	provider := GitHub{BaseURL: server.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	if _, err := provider.Attempt(t.Context(), testIdentity()); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("provider failure was hidden or exposed raw upstream content: %v", err)
	}
}
