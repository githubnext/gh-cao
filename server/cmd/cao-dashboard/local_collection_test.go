package main

import (
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/server"
)

func localCollectionEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("CAO_COLLECT_APP_ID", "42")
	t.Setenv("CAO_COLLECT_NATIVE", "true")
	t.Setenv("CAO_COLLECT_NATIVE_VALUES", "")
	t.Setenv("CAO_COLLECT_NATIVE_INVENTORY_DIGEST", strings.Repeat("a", 64))
	t.Setenv("CAO_COLLECT_CONTROL_REPOSITORY", "octo/api")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "synthetic-key")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", "")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "")
	t.Setenv("CAO_COLLECT_ADMIT_ONLY", "")
	t.Setenv("CAO_COLLECT_WORKERS", "")
	t.Setenv("CAO_COLLECT_RECOVER_DELIVERIES", "")
	t.Setenv("CAO_SOURCE_DIRECTORY", "")
	t.Setenv("CAO_GITHUB_WEBHOOK_SECRET", strings.Repeat("s", 32))
	t.Setenv("CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", "2h")
}

func TestLocalServeSelectsNativeCollection(t *testing.T) {
	localCollectionEnvironment(t)
	config := server.Config{}
	if err := configureLocalCollection(&config); err != nil {
		t.Fatal(err)
	}
	if config.Collector == nil || !config.Collector.NativeAcquisition || config.Collector.Workers != 1 {
		t.Fatalf("local serve omitted co-resident native acquisition: %+v", config.Collector)
	}
	if config.Collector.ControlRepository != "octo/api" ||
		config.Collector.NativeInventoryDigest != strings.Repeat("a", 64) ||
		config.Collector.RepositoryRefreshInterval != 2*time.Hour ||
		config.WebhookSecret != strings.Repeat("s", 32) {
		t.Fatal("local serve did not preserve explicit scope, inventory, cadence and webhook settings")
	}
}

func TestLocalServeCollectionWorkerSelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		workers   string
		admitOnly string
		want      int
		wantError bool
	}{
		{name: "default", want: 1},
		{name: "explicit workers", workers: "3", want: 3},
		{name: "separate worker role", workers: "0"},
		{name: "admission only", admitOnly: "true"},
		{name: "admission rejects workers", workers: "1", admitOnly: "true", wantError: true},
		{name: "malformed workers", workers: "invalid", wantError: true},
		{name: "negative workers", workers: "-1", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			localCollectionEnvironment(t)
			t.Setenv("CAO_COLLECT_WORKERS", test.workers)
			t.Setenv("CAO_COLLECT_ADMIT_ONLY", test.admitOnly)
			config := server.Config{}
			err := configureLocalCollection(&config)
			if (err != nil) != test.wantError {
				t.Fatalf("worker selection error = %v", err)
			}
			if err == nil && config.Collector.Workers != test.want {
				t.Fatalf("workers = %d, want %d", config.Collector.Workers, test.want)
			}
			if err == nil && test.admitOnly == "true" && len(config.Collector.PrivateKeyPEM) != 0 {
				t.Fatal("admission-only local server received collection credentials")
			}
		})
	}
}

func TestLocalServeCollectionFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		env    string
		value  string
		source string
	}{
		{name: "flag snapshot", source: "snapshot"},
		{name: "environment snapshot", env: "CAO_SOURCE_DIRECTORY", value: "snapshot"},
		{name: "missing native selection", env: "CAO_COLLECT_NATIVE", value: "false"},
		{name: "missing credentials", env: "CAO_COLLECT_PRIVATE_KEY"},
		{name: "missing inventory", env: "CAO_COLLECT_NATIVE_INVENTORY_DIGEST"},
		{name: "missing control repository", env: "CAO_COLLECT_CONTROL_REPOSITORY"},
		{name: "missing webhook secret", env: "CAO_GITHUB_WEBHOOK_SECRET"},
		{name: "short webhook secret", env: "CAO_GITHUB_WEBHOOK_SECRET", value: "short"},
		{name: "invalid cadence", env: "CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", value: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			localCollectionEnvironment(t)
			if test.env != "" {
				t.Setenv(test.env, test.value)
			}
			config := server.Config{SourceDirectory: test.source}
			if err := configureLocalCollection(&config); err == nil || config.Collector != nil {
				t.Fatalf("invalid collection configuration was accepted: %v", err)
			}
		})
	}
}

func TestLocalServePreservesWebhookSecretBytes(t *testing.T) {
	localCollectionEnvironment(t)
	secret := " " + strings.Repeat("s", 32) + "\n"
	t.Setenv("CAO_GITHUB_WEBHOOK_SECRET", secret)
	config := server.Config{}
	if err := configureLocalCollection(&config); err != nil {
		t.Fatal(err)
	}
	if config.WebhookSecret != secret {
		t.Fatal("local serving changed the webhook signing secret")
	}
}

func TestLocalServeWithoutCollectorPreservesSnapshotProfile(t *testing.T) {
	localCollectionEnvironment(t)
	t.Setenv("CAO_COLLECT_APP_ID", "")
	t.Setenv("CAO_COLLECT_NATIVE", "")
	t.Setenv("CAO_COLLECT_NATIVE_INVENTORY_DIGEST", "")
	config := server.Config{SourceDirectory: "snapshot"}
	if err := configureLocalCollection(&config); err != nil {
		t.Fatal(err)
	}
	if config.Collector != nil || config.SourceDirectory != "snapshot" || config.WebhookSecret != "" {
		t.Fatal("snapshot-only local serving unexpectedly selected collection")
	}
}
