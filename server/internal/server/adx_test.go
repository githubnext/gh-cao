package server

import (
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestADXDisabledApplicationDoesNotConstructWorker(t *testing.T) {
	t.Setenv("CAO_ADX_ENABLED", "false")
	t.Setenv("CAO_ADX_ENDPOINT", "invalid")
	store := redisx.NewStore(emptyRedisClient{}, "adx-disabled")
	app, err := New(t.Context(), store, Config{
		Database: constructorDatabase(), SiteDirectory: t.TempDir(),
		Listen: "127.0.0.1:8080", AccessToken: strings.Repeat("x", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.adx != nil || app.ADXStatus().Stage != "disabled" || app.ADXStatus().Degraded {
		t.Fatal("disabled ADX altered application readiness")
	}
}

func TestADXRejectsUnsupportedHostsBeforeCreatingClient(t *testing.T) {
	durable := operational.Capabilities{Coordination: operational.Capability{
		Scope: operational.ScopeDeployment, Persistence: operational.PersistenceRestart,
	}}
	base := Config{Database: &postgresx.Store{}, SingleReplicaConfirmed: true,
		HostProfile: HostProfile{Authentication: HostAuthenticationOAuth, Listener: HostListenerProcess},
		ADX:         &adxingest.Config{Enabled: true}}
	for _, mutate := range []func(*Config, *operational.Capabilities){
		func(c *Config, _ *operational.Capabilities) { c.Database = nil },
		func(c *Config, _ *operational.Capabilities) { c.SingleReplicaConfirmed = false },
		func(c *Config, _ *operational.Capabilities) { c.HostProfile.Listener = HostListenerPlatform },
		func(c *Config, _ *operational.Capabilities) { c.HostProfile.Authentication = HostAuthenticationBearer },
		func(c *Config, _ *operational.Capabilities) { c.Collector = &CollectorConfig{AdmitOnly: true} },
		func(_ *Config, p *operational.Capabilities) {
			p.Coordination.Persistence = operational.PersistenceVolatile
		},
	} {
		config, capabilities := base, durable
		mutate(&config, &capabilities)
		if _, err := newADXWorker(config, operational.OperationalServices{}, capabilities); err == nil {
			t.Fatal("unsupported ADX host accepted")
		}
	}
}

func TestADXConfigurationFailsClosedAndExplicitDisableWins(t *testing.T) {
	t.Setenv("CAO_ADX_ENABLED", "true")
	if _, err := resolveADXConfig(Config{}); err == nil {
		t.Fatal("incomplete enabled configuration accepted")
	}
	config, err := resolveADXConfig(Config{ADX: &adxingest.Config{}})
	if err != nil || config.Enabled {
		t.Fatal("explicit disabled configuration did not override environment")
	}
}
