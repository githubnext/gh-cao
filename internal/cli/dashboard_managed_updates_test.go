package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDashboardManagedUpdatesUseFreshPolicyNotLastValidDisplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/managed")
	initial, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &dashboardConfiguration{path: path, owner: "octo", builtins: []string{"octo/current"}}
	configuration.current.Store(initial)
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, configuration)
	scope, err := dashboardUpdateRepositories(ctx, []string{"octo/current"})
	if err != nil || !slices.Contains(scope, "octo/managed") || !slices.Contains(scope, "octo/current") ||
		slices.Contains(scope, "octo/unmanaged") {
		t.Fatalf("managed update scope: %v %v", scope, err)
	}
	dashboardConfigurationTestPolicy(t, path, "octo/other")
	scope, err = dashboardUpdateRepositories(ctx, []string{"octo/current"})
	if err != nil || slices.Contains(scope, "octo/managed") || !slices.Contains(scope, "octo/other") {
		t.Fatalf("revoked repository stayed authorized: %v %v", scope, err)
	}
	if configuration.current.Load() != initial {
		t.Fatal("authorization should not mutate the last-valid display snapshot")
	}
	if err := os.WriteFile(path, []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dashboardUpdateRepositories(ctx, []string{"octo/current"}); err == nil {
		t.Fatal("invalid current policy fell back to the last-valid display grant")
	}
}

func TestDashboardManagedUpdatesRespectOwnerBoundsAndExplicitTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, &dashboardConfiguration{
		path: path, owner: "octo", builtins: []string{"octo/current"},
	})
	for _, test := range []struct {
		name, control, allowed string
	}{
		{"owner-only scope grants no discovery", `{"scope":{"allowed-owners":["octo"]}}`, ""},
		{"default control owner", `{"scope":{"allowed-repositories":["other/repo"]}}`, ""},
		{"owner intersection", `{"scope":{"allowed-owners":["octo"],"allowed-repositories":["other/repo"]}}`, ""},
		{"empty owners", `{"scope":{"allowed-owners":[],"allowed-repositories":["octo/managed"]}}`, ""},
		{"explicit campaign target", `{"scope":{"allowed-owners":["octo"]},"campaigns":{"campaign-a":{"targets":{"octo/targeted":{"mode":"review"}}}}}`, "octo/targeted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(`{"version":1,"gh-aw-version":"v1.2.3","control-plane":`+test.control+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			scope, err := dashboardUpdateRepositories(ctx, nil)
			want := []string{"octo/current"}
			if test.allowed != "" {
				want = append(want, test.allowed)
			}
			if err != nil || !slices.Equal(scope, want) {
				t.Fatalf("scope %v, want %v: %v", scope, want, err)
			}
		})
	}
}

func TestDashboardAdapterExecutesManagedUpdatesAndRechecksRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/managed")
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, &dashboardConfiguration{
		path: path, owner: "octo", builtins: []string{"octo/current"},
	})
	filename, cleanup, err := startDashboardActionAdapterWithExecutable(ctx, []string{"octo/current"}, t.TempDir(), dashboardTestActionExecutable(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AdapterURL     string
		ManagedUpdates bool
	}
	if err := json.Unmarshal(data, &config); err != nil || !config.ManagedUpdates {
		t.Fatalf("native adapter did not advertise managed updates: %v", err)
	}
	access := func(want bool, status int) {
		t.Helper()
		response, err := http.Get(config.AdapterURL + "/repositories/octo/managed/update-access")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != status {
			t.Fatalf("native update access returned %d, want %d", response.StatusCode, status)
		}
		if status == http.StatusOK {
			var result struct{ Allowed bool }
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Allowed != want {
				t.Fatalf("native update access is stale: %+v %v", result, err)
			}
		}
	}
	access(true, 200)
	execute := func(id, body string, status int) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.AdapterURL+"/execute", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Command-ID", id)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		content, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("native dispatch status %d, want %d: %s %v", response.StatusCode, status, content, err)
		}
		if status == http.StatusOK && !bytes.Contains(content, []byte(`"complete":true,"exitCode":0`)) {
			t.Fatalf("managed update did not execute: %s", content)
		}
	}
	execute("1", `["update","--repo","octo/managed","--create-pull-request"]`, 200)
	execute("2", `["disable","--repo","octo/managed"]`, 403)
	dashboardConfigurationTestPolicy(t, path, "octo/other")
	access(false, 200)
	execute("3", `["update","--repo","octo/managed","--create-pull-request"]`, 403)
	if err := os.WriteFile(path, []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	access(false, 503)
	execute("4", `["update","--repo","octo/managed","--create-pull-request"]`, 403)
}
