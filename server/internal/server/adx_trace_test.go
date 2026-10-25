package server

import (
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestTraceHostRequiresCoResidentAuthorizedCollection(t *testing.T) {
	c := adxtrace.Config{Enabled: true, Directory: t.TempDir(), DurableStorage: true, Repositories: []string{"octo/api"}, Mode: "full",
		Policy: adxtrace.Policy{Version: "v1", ContentApproved: true, RightsApproved: true, Events: map[string][]string{"user.message": {"content"}}}}
	config := Config{ADXTraces: &c, ADX: &adxingest.Config{Enabled: true, RepositoryIDs: []string{"repository:" + model.EncodeCoordinate("octo/api")}}, Collector: &CollectorConfig{Workers: 1}}
	if _, err := resolveTraceConfig(config); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.Collector = nil },
		func(c *Config) { c.Collector = &CollectorConfig{Workers: 0} },
		func(c *Config) { c.Collector = &CollectorConfig{Workers: 1, AdmitOnly: true} },
		func(c *Config) { c.ADX = &adxingest.Config{} },
		func(c *Config) { c.ADX = &adxingest.Config{Enabled: true, RepositoryIDs: []string{"different"}} }} {
		next := config
		change(&next)
		if _, err := resolveTraceConfig(next); err == nil {
			t.Fatal("unsupported topology or scope accepted")
		}
	}
	t.Setenv("CAO_ADX_TRACES_ENABLED", "true")
	t.Setenv("CAO_ADX_TRACE_POLICY", "/unavailable/policy")
	config.ADXTraces = &adxtrace.Config{}
	if _, err := resolveTraceConfig(config); err != nil {
		t.Fatal("explicit disabled config touched environment")
	}
	if got := (&App{}).ADXTraceStatus(); got.Stage != "disabled" {
		t.Fatal("disabled app reported trace worker")
	}
}
