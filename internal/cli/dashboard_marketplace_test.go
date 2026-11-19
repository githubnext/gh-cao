package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDashboardMarketplaceResolvesOutsideServerAndRefreshesPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test resolver uses a shell executable")
	}
	directory := t.TempDir()
	policy := filepath.Join(directory, "cao.json")
	executable := filepath.Join(directory, "resolver")
	calls := filepath.Join(directory, "calls")
	t.Setenv("GH_TOKEN", "test-registry-secret")
	t.Setenv("MARKETPLACE_TEST_CALLS", calls)
	writeTestFile(t, policy, `{"control-plane":{"marketplace":{"cache-ttl-seconds":60}}}`)
	script := `#!/bin/sh
test "$1" = __marketplace && test "$2" = --policy && test -f "$3" || exit 1
test "$GH_TOKEN" = test-registry-secret || exit 1
printf 'call\n' >> "$MARKETPLACE_TEST_CALLS"
printf '{"Packages":[{"name":"Demo"}],"Diagnostics":[]}\n'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	handler := dashboardMarketplaceHandler(executable, policy)
	read := func() {
		t.Helper()
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/marketplace", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Demo") ||
			strings.Contains(response.Body.String(), "test-registry-secret") {
			t.Fatalf("unexpected marketplace result: %d %s", response.Code, response.Body)
		}
	}
	read()
	read()
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "call\n" {
		t.Fatalf("catalog did not reuse its resolution: %q %v", data, err)
	}
	writeTestFile(t, policy, `{"control-plane":{"marketplace":{"registries":[]}}}`)
	read()
	data, err = os.ReadFile(calls)
	if err != nil || string(data) != "call\ncall\n" {
		t.Fatalf("policy change did not invalidate catalog: %q %v", data, err)
	}
	for _, entry := range dashboardServerEnvironment() {
		if strings.HasPrefix(entry, "GH_TOKEN=") {
			t.Fatal("registry credential reached the web server")
		}
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/marketplace", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "Demo") {
		t.Fatal("removed policy retained a usable marketplace")
	}
}

func TestDashboardMarketplaceResolverFailureIsUnavailable(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "cao.json")
	writeTestFile(t, policy, `{}`)
	handler := dashboardMarketplaceHandler(filepath.Join(t.TempDir(), "missing"), policy)
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/marketplace", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("resolver failure returned %d", response.Code)
	}
}
