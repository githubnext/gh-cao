package adxingest

import (
	"strings"
	"testing"
	"time"
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

func TestConfigValidateAppliesDefaultsAndNormalizes(t *testing.T) {
	config := testConfig()
	config.Endpoint += "/"
	given := []string{"b", "a"}
	config.RepositoryIDs = given
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.Endpoint != "https://synthetic.northcentralus.kusto.windows.net" {
		t.Fatalf("endpoint not trimmed: %q", config.Endpoint)
	}
	if config.Interval != 30*time.Second || config.MaxStorageBytes != 50_000_000_000 {
		t.Fatalf("defaults: %v %d", config.Interval, config.MaxStorageBytes)
	}
	if config.RepositoryIDs[0] != "a" || given[0] != "b" {
		t.Fatalf("scope not sorted on a copy: %v %v", config.RepositoryIDs, given)
	}
}

func TestConfigValidateBoundaries(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Config)
		ok     bool
	}{
		"min interval":   {func(c *Config) { c.Interval = time.Second }, true},
		"max interval":   {func(c *Config) { c.Interval = time.Hour }, true},
		"over interval":  {func(c *Config) { c.Interval = time.Hour + 1 }, false},
		"min storage":    {func(c *Config) { c.MaxStorageBytes = 1 << 20 }, true},
		"under storage":  {func(c *Config) { c.MaxStorageBytes = 1<<20 - 1 }, false},
		"approved data":  {func(c *Config) { c.NonPersonalData, c.DataApproved = false, true }, true},
		"long repo id":   {func(c *Config) { c.RepositoryIDs = []string{strings.Repeat("r", 513)} }, false},
		"too many repos": {func(c *Config) { c.RepositoryIDs = make([]string, 1001) }, false},
	} {
		config := testConfig()
		tc.mutate(&config)
		if err := config.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: ok=%v err=%v", name, tc.ok, err)
		}
	}
}

func TestFromEnvParsesEnabledConfiguration(t *testing.T) {
	env := map[string]string{
		"CAO_ADX_ENABLED": "true", "CAO_ADX_ENDPOINT": "https://synthetic.northcentralus.kusto.windows.net",
		"CAO_ADX_DATABASE": "CAO", "CAO_ADX_TENANT_ID": "11111111-1111-1111-1111-111111111111",
		"CAO_ADX_CLIENT_ID": "22222222-2222-2222-2222-222222222222", "CAO_ADX_CLIENT_SECRET": "test-only",
		"CAO_ADX_NON_PERSONAL_DATA": "true", "CAO_ADX_SINGLE_REPLICA": "true",
		"CAO_ADX_REPOSITORY_IDS": " b , a ", "CAO_ADX_INTERVAL": "5s", "CAO_ADX_MAX_STORAGE_BYTES": "2097152",
	}
	config, err := FromEnv(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.Interval != 5*time.Second || config.MaxStorageBytes != 2097152 || config.RepositoryIDs[0] != "a" {
		t.Fatalf("unexpected config: %+v", config)
	}
	env["CAO_ADX_INTERVAL"] = "soon"
	if _, err := FromEnv(func(name string) string { return env[name] }); err == nil {
		t.Fatal("invalid interval accepted")
	}
}
