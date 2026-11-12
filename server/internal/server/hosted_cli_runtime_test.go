package server

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestHostedCLIContainerRuntimeSelection(t *testing.T) {
	for _, provider := range []string{"", "docker", "podman", "apple", "windows-sandbox", "remote"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME", provider)
			t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "")
			t.Setenv("CAO_HOSTED_CLI_EGRESS_POLICY_FILE", "")
			t.Setenv("CAO_HOSTED_CLI_EGRESS_RELAY", "")
			t.Setenv("CAO_HOSTED_CLI_EGRESS_RELAY_SHA256", "")
			_, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{})
			if err == nil {
				t.Fatal("production accepted missing egress configuration or a native runtime")
			}
			switch provider {
			case "", "podman":
				if !strings.Contains(err.Error(), "CAO_HOSTED_CLI_EGRESS_POLICY_FILE") {
					t.Fatalf("production Podman did not require egress configuration: %v", err)
				}
			case "docker", "remote":
				if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true}); err == nil ||
					!strings.Contains(err.Error(), "must be podman, apple, or windows-sandbox") {
					t.Fatalf("unsupported runtime %q was not rejected: %v", provider, err)
				}
			}
		})
	}
}

func TestHostedCLINativeRuntimesRequireMatchingPlatform(t *testing.T) {
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "")
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME", "apple")
	runner, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true})
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && os.Getuid() != 0 {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := runner.Lifecycle.(*hostedcli.AppleContainerLifecycle); !ok {
			t.Fatal("Apple lifecycle was not selected")
		}
	} else if err == nil {
		t.Fatal("Apple runtime accepted on an unsupported host")
	}
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME", "windows-sandbox")
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", hostedcli.DefaultContainerImage)
	if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true}); err == nil || !strings.Contains(err.Error(), "Windows tools bundle") {
		t.Fatalf("OCI image silently replaced by Windows tools: %v", err)
	}
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "")
	t.Setenv("CAO_HOSTED_CLI_WINDOWS_BUNDLE", "")
	t.Setenv("CAO_HOSTED_CLI_WINDOWS_BUNDLE_SHA256", "")
	if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true}); err == nil {
		t.Fatal("Windows Sandbox accepted without its pinned tools")
	}
}

func TestHostedCLINativeQueueConfigurationMatchesAdmission(t *testing.T) {
	defaults, err := (operational.CLICommandQueueOptions{}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"podman", "apple", "windows-sandbox"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME", provider)
			catalog, err := hostedcli.ParseCatalog([]byte(`{"dashboard":{"cli-actions":[]}}`))
			if err != nil {
				t.Fatal(err)
			}

			store := hostedCLIPostgresTestStore(t)
			github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
			app := memoryOAuthApp(t, github.URL)
			app.config.Listen = "127.0.0.1:8443"
			app.services.CLICommands = store
			app.config.Audit = app.config.Audit.WithStore(store)
			if err := app.EnableHostedCLIActions(t.Context(), HostedCLIConfig{
				Catalog: catalog, ControlRepository: "octo/cao", LocalDevelopment: true,
			}); err != nil {
				t.Fatal(err)
			}
			options, err := hostedCLIQueueOptions(true)
			if err != nil {
				t.Fatal(err)
			}
			expected := defaults.MaxConcurrency
			if provider == "windows-sandbox" {
				expected = 1
			}
			if options.MaxConcurrency != expected {
				t.Fatalf("runtime concurrency = %d, want %d", options.MaxConcurrency, expected)
			}
			if _, err := store.OpenCLICommandQueue(t.Context(), hostedcli.QueueName, options); err != nil {
				t.Fatalf("executor cannot open admission's persisted queue: %v", err)
			}
			production, err := hostedCLIQueueOptions(false)
			if err != nil || production.MaxConcurrency != defaults.MaxConcurrency {
				t.Fatal("local runtime selection altered production queue concurrency")
			}
		})
	}
}

func TestHostedCLIPodmanSelection(t *testing.T) {
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME", "")
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "")
	for _, name := range []string{
		"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "PODMAN_CONNECTIONS_CONF",
		"CONTAINERS_CONF", "CONTAINERS_CONF_OVERRIDE", "CONTAINERS_CONF_MODULES",
		"CONTAINERS_STORAGE_CONF", "CAO_HOSTED_CLI_EGRESS_POLICY_FILE",
		"CAO_HOSTED_CLI_EGRESS_RELAY", "CAO_HOSTED_CLI_EGRESS_RELAY_SHA256",
	} {
		t.Setenv(name, "")
	}
	if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{}); err == nil {
		t.Fatal("production accepted Podman without its egress boundary")
	}
	runner, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true})
	if runtime.GOOS != "linux" {
		if err == nil {
			t.Fatal("Podman accepted on unsupported platform")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runner.Lifecycle.(*hostedcli.PodmanContainerLifecycle); !ok {
		t.Fatal("default selection did not choose Podman")
	}
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "ghcr.io/github/gh-aw:latest")
	if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true}); err == nil {
		t.Fatal("Podman accepted an unpinned image")
	}
	t.Setenv("CAO_HOSTED_CLI_CONTAINER_IMAGE", "")
	t.Setenv("CAO_HOSTED_CLI_EGRESS_POLICY_FILE", "unavailable.json")
	if _, err := newHostedCLIRunnerFromEnv(HostedCLIExecutorOptions{LocalDevelopment: true}); err == nil {
		t.Fatal("partial egress configuration fell back to unrestricted development")
	}
}
