package adxingest

import (
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Enabled: true, Endpoint: "https://synthetic.northcentralus.kusto.windows.net",
		Database: "CAO", TenantID: "11111111-1111-1111-1111-111111111111",
		ClientID: "22222222-2222-2222-2222-222222222222", ClientSecret: "test-only",
		RepositoryIDs: []string{"repo"}, NonPersonalData: true, SingleReplica: true}
}

func TestConfigDefaultOff(t *testing.T) {
	config, err := FromEnv(func(name string) string {
		if name == "CAO_ADX_ENABLED" {
			return ""
		}
		t.Fatalf("disabled mode read %s", name)
		return ""
	})
	if err != nil || config.Enabled {
		t.Fatalf("disabled config: %+v %v", config, err)
	}
	if _, err := NewClient(config); err == nil {
		t.Fatal("disabled client created")
	}
}

func TestConfigRejectsUnsafeDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"http://cluster.kusto.windows.net", "https://cluster.kusto.windows.net.evil.test",
		"https://user:secret@cluster.kusto.windows.net", "https://cluster.kusto.windows.net/path",
		"https://cluster.kusto.windows.net/?credential=secret", "https://cluster.kusto.windows.net:443",
		"https://ingest-cluster.kusto.windows.net", "https://127.0.0.1",
	} {
		config := testConfig()
		config.Endpoint = endpoint
		if err := config.Validate(); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe endpoint accepted or exposed: %v", err)
		}
	}
}

func TestConfigRequiresScopeClassificationAndSingleReplica(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.NonPersonalData = false },
		func(c *Config) { c.SingleReplica = false },
		func(c *Config) { c.RepositoryIDs = nil },
		func(c *Config) { c.RepositoryIDs = []string{""} },
		func(c *Config) { c.RepositoryIDs = []string{"repo", "repo"} },
		func(c *Config) { c.Database = "CAO']; .drop database X" },
		func(c *Config) { c.ClientSecret = "" },
		func(c *Config) { c.MaxStorageBytes = 1<<40 + 1 },
		func(c *Config) { c.Interval = -1 },
	} {
		config := testConfig()
		mutate(&config)
		if err := config.Validate(); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	config := testConfig()
	if err := config.Validate(); err != nil || config.MaxStorageBytes != 50_000_000_000 {
		t.Fatalf("valid config rejected: %v", err)
	}
}
