package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestManagedRepositoryUpdateAdmissionAndExecutionRecheckScope(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	var executions atomic.Int32
	var nativeReject atomic.Bool
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repositories/octo/managed/update-access" {
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed.Load()})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/execute" {
			t.Errorf("unexpected native request: %s %s", r.Method, r.URL.Path)
		}
		var args []string
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil ||
			!slices.Equal(args, []string{"upgrade", "--repo", "octo/managed", "--create-pull-request"}) {
			t.Errorf("repository Update did not run upgrade: %v %v", args, err)
		}
		if nativeReject.Load() {
			http.Error(w, "scope changed before native execution", http.StatusForbidden)
			return
		}
		executions.Add(1)
		_, _ = w.Write([]byte("{\"complete\":true,\"exitCode\":0}\n"))
	}))
	defer adapter.Close()
	catalog, err := hostedcli.LoadCatalog(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	selected := &LocalOperatorActionsConfig{Repositories: []string{"octo/control", "github/gh-aw"}}
	app := &App{
		config: Config{OperatorActions: &LocalOperatorConfig{
			AdapterURL: adapter.URL, OperatorActions: selected, ManagedUpdates: true,
		}},
		hostedCLI: &hostedCLI{catalog: catalog, local: selected},
	}
	request := hostedcli.Request{ID: "update-repository-sources", Values: map[string]string{"repository": "octo/managed"}}
	for _, id := range []string{"update-repository-sources", "disable-repository-workflows", "add-repository"} {
		field := "repository"
		if id == "add-repository" {
			field = "repository-pattern"
		}
		if _, err := app.admitOperatorAction(t.Context(), hostedcli.Request{
			ID: id, Values: map[string]string{field: "GitHub/GH-AW"},
		}); err == nil {
			t.Fatalf("upstream exclusion did not reject %s", id)
		}
	}
	if code, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
		ID: 99, CLICommandInvocation: operational.CLICommandInvocation{
			Command: "gh aw upgrade --repo github/gh-aw --create-pull-request", Repository: "github/gh-aw",
		},
	}, func(string, []byte) {}); err == nil || code == nil || *code == 0 || executions.Load() != 0 {
		t.Fatal("persisted upstream operation bypassed the exclusion")
	}
	invocation, err := app.admitOperatorAction(t.Context(), request)
	if err != nil || invocation.Command != "gh aw upgrade --repo octo/managed --create-pull-request" {
		t.Fatalf("managed repository Update was rejected or used the wrong command: %+v %v", invocation, err)
	}
	command := operational.CLICommand{ID: 1, CLICommandInvocation: invocation}
	code, err := app.callOperatorAdapter(t.Context(), command, func(string, []byte) {})
	if err != nil || code == nil || *code != 0 || executions.Load() != 1 {
		t.Fatalf("managed upgrade did not execute: %v %v", code, err)
	}
	if _, err := app.admitOperatorAction(t.Context(), hostedcli.Request{
		ID: "disable-repository-workflows", Values: map[string]string{"repository": "octo/managed"},
	}); err == nil {
		t.Fatal("managed update access broadened destructive disable scope")
	}
	allowed.Store(false)
	if _, err := app.admitOperatorAction(t.Context(), request); err == nil {
		t.Fatal("revoked repository was admitted")
	}
	code, err = app.callOperatorAdapter(t.Context(), command, func(string, []byte) {})
	if err == nil || code == nil || *code == 0 || executions.Load() != 1 {
		t.Fatal("revoked queued command reached execution or left an uncertain claim")
	}
	allowed.Store(true)
	nativeReject.Store(true)
	code, err = app.callOperatorAdapter(t.Context(), command, func(string, []byte) {})
	if err == nil || code == nil || *code == 0 || executions.Load() != 1 {
		t.Fatal("native pre-execution revocation was treated as successful or uncertain execution")
	}
}

func TestManagedRepositoryScopeFailsClosed(t *testing.T) {
	for _, body := range []string{`{}`, `{"allowed":null}`, `{"allowed":true,"extra":true}`, `{"allowed":true}{}`, `not JSON`} {
		t.Run(body, func(t *testing.T) {
			adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer adapter.Close()
			app := &App{config: Config{OperatorActions: &LocalOperatorConfig{AdapterURL: adapter.URL, ManagedUpdates: true}}}
			allowed, err := app.operatorAllows(t.Context(), &LocalOperatorActionsConfig{}, hostedcli.Plan{
				Kind: hostedcli.KindGhAwUpgrade, Repository: "octo/managed",
			})
			if allowed || err == nil {
				t.Fatal("invalid native scope response granted update access")
			}
		})
	}
}

func TestRevokedManagedUpgradeSettlesWithoutBlockingOtherQueuedCommands(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	prefix := "/" + strings.Repeat("b", 48)
	var managed atomic.Bool
	managed.Store(true)
	var executions atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			path := strings.TrimPrefix(r.URL.Path, prefix)
			allowed := path == "/repositories/octo/control/update-access" ||
				path == "/repositories/octo/managed/update-access" && managed.Load()
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
			return
		}
		executions.Add(1)
		_, _ = w.Write([]byte("{\"complete\":true,\"exitCode\":0}\n"))
	}))
	defer adapter.Close()
	app.config.OperatorActions = &LocalOperatorConfig{
		AdapterURL: adapter.URL + prefix, ManagedUpdates: true,
		OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}},
	}
	catalog, err := os.ReadFile(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, "dashboard.json"), catalog, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.enableOperatorActions(t.Context()); err != nil {
		t.Fatal(err)
	}
	queue := app.hostedCLI.queue
	for _, repository := range []string{"octo/managed", "octo/control"} {
		invocation, err := app.admitOperatorAction(t.Context(), hostedcli.Request{
			ID: "update-repository-sources", Values: map[string]string{"repository": repository},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := queue.Enqueue(t.Context(), invocation, strings.ReplaceAll(repository, "/", "-"), 0); err != nil {
			t.Fatal(err)
		}
	}
	lease, err := queue.Acquire(t.Context(), "managed-test", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = queue.Release(context.WithoutCancel(t.Context()), lease) }()
	managed.Store(false)
	for _, expected := range []operational.CLICommandStatus{operational.CLICommandFailed, operational.CLICommandCompleted} {
		command, err := queue.Claim(t.Context(), lease)
		if err != nil || command == nil {
			t.Fatalf("queue stopped after a confirmed pre-dispatch rejection: %v", err)
		}
		if !app.executeOperatorCommand(t.Context(), lease, *command) {
			t.Fatal("confirmed rejection unexpectedly required queue recovery")
		}
		settled, err := queue.Detail(t.Context(), command.ID)
		if err != nil || settled.Status != expected || settled.ExecutionClaimed {
			t.Fatalf("command did not settle: %+v %v", settled, err)
		}
	}
	if executions.Load() != 1 {
		t.Fatal("revoked command executed or allowed command did not execute")
	}
}
