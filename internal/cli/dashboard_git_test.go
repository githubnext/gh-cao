package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func dashboardTestGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native dashboard commands are unavailable on Windows")
	}
	directory := t.TempDir()
	t.Chdir(directory)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "EMAIL"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	gh := `#!/bin/sh
case "$*" in
  "auth status --active --hostname github.com") exit "${CAO_TEST_AUTH_EXIT:-0}" ;;
  "api --hostname github.com user") printf '%s\n' "${CAO_TEST_GH_USER}" ;;
  "auth git-credential get") printf 'username=readiness-test-user\npassword=readiness-test-token\n' ;;
  *) exit 99 ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAO_TEST_GH_USER", `{"id":42,"login":"octocat","name":null,"email":null}`)
	t.Setenv("CAO_TEST_AUTH_EXIT", "0")
}

func dashboardGitOutput(t *testing.T, environment []string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Env = environment
	content, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, content, err)
	}
	return strings.TrimSpace(string(content))
}

func TestDashboardGitReadinessResolvesIdentityAndAuthenticatesGit(t *testing.T) {
	for _, test := range []struct {
		name, profile, gitName, gitEmail, author, wantName, wantEmail string
	}{
		{"private email", `{"id":42,"login":"octocat"}`, "", "", "", "octocat", "42+octocat@users.noreply.github.com"},
		{"public profile", `{"id":42,"login":"octocat","name":"Octo Cat","email":"octo@example.test"}`, "", "", "", "Octo Cat", "octo@example.test"},
		{"configured identity", `{}`, "Existing User", "existing@example.test", "", "Existing User", "existing@example.test"},
		{"partial configuration", `{"id":42,"login":"octocat"}`, "Existing User", "", "", "Existing User", "42+octocat@users.noreply.github.com"},
		{"author override", `{}`, "Existing User", "existing@example.test", "Existing Author", "Existing Author", "existing@example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dashboardTestGit(t)
			t.Setenv("CAO_TEST_GH_USER", test.profile)
			t.Setenv("GIT_AUTHOR_NAME", test.author)
			dashboardGitOutput(t, os.Environ(), "init", "--quiet")
			for field, value := range map[string]string{"name": test.gitName, "email": test.gitEmail} {
				if value != "" {
					dashboardGitOutput(t, os.Environ(), "config", "user."+field, value)
				}
			}
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
			t.Setenv("GIT_CONFIG_VALUE_0", "true")
			if test.gitName == "" {
				command := exec.CommandContext(t.Context(), "git", "var", "GIT_AUTHOR_IDENT")
				if err := command.Run(); err == nil {
					t.Fatal("regression fixture unexpectedly has a usable Git identity before readiness")
				}
			}
			environment, err := dashboardGitEnvironment(t.Context(), os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			if got := dashboardGitOutput(t, environment, "var", "GIT_AUTHOR_IDENT"); !strings.HasPrefix(got, test.wantName+" <"+test.wantEmail+"> ") {
				t.Fatalf("unexpected author identity: %s", got)
			}
			dashboardGitOutput(t, environment, "var", "GIT_COMMITTER_IDENT")
			if got := dashboardGitOutput(t, environment, "config", "--get-all", "credential.https://github.com.helper"); got != "!gh auth git-credential" {
				t.Fatalf("GitHub helper not scoped to the child: %q", got)
			}
			if got := dashboardGitOutput(t, environment, "config", "--get", "user.useConfigOnly"); got != "true" {
				t.Fatal("existing injected configuration was lost")
			}
			for _, address := range []string{"git@github.com:octo/repo.git", "ssh://git@github.com/octo/repo.git"} {
				if got := dashboardGitOutput(t, environment, "ls-remote", "--get-url", address); got != "https://github.com/octo/repo.git" {
					t.Fatalf("GitHub SSH preference bypassed gh authentication: %s", got)
				}
			}
			if got := dashboardGitOutput(t, environment, "ls-remote", "--get-url", "git@other.example:octo/repo.git"); got != "git@other.example:octo/repo.git" {
				t.Fatal("Git readiness rewrote an unrelated host")
			}
			credential := exec.CommandContext(t.Context(), "git", "credential", "fill")
			credential.Env = environment
			credential.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
			authentication, err := credential.Output()
			if err != nil || !strings.Contains(string(authentication), "password=readiness-test-token") {
				t.Fatalf("Git did not authenticate through gh: %v", err)
			}
			dashboardGitOutput(t, environment, "commit", "--allow-empty", "--no-gpg-sign", "-m", "Readiness regression")
			if got := dashboardGitOutput(t, os.Environ(), "log", "-1", "--format=%an <%ae>"); got != test.wantName+" <"+test.wantEmail+">" {
				t.Fatalf("commit did not use resolved identity: %s", got)
			}
			command := exec.CommandContext(t.Context(), "git", "config", "--local", "--get", "credential.https://github.com.helper")
			if err := command.Run(); err == nil {
				t.Fatal("readiness persisted a repository credential helper")
			}
		})
	}
}

func TestDashboardGitReadinessUsesFreshExplicitIdentity(t *testing.T) {
	dashboardTestGit(t)
	path := filepath.Join(t.TempDir(), "cao.json")
	write := func(name string) {
		t.Helper()
		policy := initialPolicy("octo/control", "v1.2.3")
		policy["git"] = map[string]any{"name": name, "email": "automation@example.test"}
		content, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Original")
	snapshot, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &dashboardConfiguration{path: path}
	configuration.current.Store(snapshot)
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, configuration)
	write("Current Automation")
	t.Setenv("GIT_AUTHOR_NAME", "Ambient Author")
	t.Setenv("CAO_TEST_GH_USER", `{}`)
	environment, err := dashboardGitEnvironment(ctx, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"AUTHOR", "COMMITTER"} {
		if got := dashboardGitOutput(t, environment, "var", "GIT_"+role+"_IDENT"); !strings.HasPrefix(got, "Current Automation <automation@example.test> ") {
			t.Fatalf("stale or ambient identity used: %s", got)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dashboardGitEnvironment(ctx, os.Environ()); err == nil {
		t.Fatal("invalid current configuration fell back to the last-valid identity")
	}
}

func TestDashboardGitReadinessFailures(t *testing.T) {
	for _, test := range []struct{ name, auth, profile, count, want string }{
		{"authentication", "1", `{}`, "", "gh auth login"},
		{"unavailable user", "0", `{}`, "", "git.name and git.email"},
		{"invalid profile", "0", `{"id":42,"login":"octocat","name":"Bad\nName"}`, "", "invalid commit identity"},
		{"invalid injected configuration", "0", `{"id":42,"login":"octocat"}`, "-1", "unable to read Git identity configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dashboardTestGit(t)
			t.Setenv("CAO_TEST_AUTH_EXIT", test.auth)
			t.Setenv("CAO_TEST_GH_USER", test.profile)
			t.Setenv("GIT_CONFIG_COUNT", test.count)
			if _, err := dashboardGitEnvironment(t.Context(), os.Environ()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readiness did not fail actionably: %v", err)
			}
		})
	}
}

func TestDashboardGitReadinessBlocksMaintenanceBeforeExecution(t *testing.T) {
	executable := dashboardTestActionExecutable(t)
	t.Setenv("CAO_TEST_AUTH_EXIT", "1")
	filename, cleanup, err := startDashboardActionAdapterWithExecutable(t.Context(), []string{"octo/control"}, t.TempDir(), executable, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ AdapterURL string }
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.AdapterURL+"/execute",
		strings.NewReader(`["update","--repo","octo/control","--create-pull-request"]`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Command-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var output strings.Builder
	completed := false
	decoder := json.NewDecoder(response.Body)
	for decoder.More() {
		var event dashboardActionEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		output.WriteString(event.Data)
		if event.Complete {
			completed = true
			if event.ExitCode == nil || *event.ExitCode != 1 {
				t.Fatalf("readiness failure did not settle as failed: %+v", event)
			}
		}
	}
	if !completed || !strings.Contains(output.String(), "gh auth login") || strings.Contains(output.String(), "Native command executed") {
		t.Fatalf("maintenance started despite missing authentication: %s", output.String())
	}
}
