package server

import (
	"errors"
	"os"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func resolveADXConfig(config Config) (adxingest.Config, error) {
	if config.ADX != nil {
		resolved := *config.ADX
		if err := resolved.Validate(); err != nil {
			return adxingest.Config{}, err
		}
		return resolved, nil
	}
	return adxingest.FromEnv(os.Getenv)
}

func newADXWorker(config Config, services operational.OperationalServices, capabilities operational.Capabilities) (*adxingest.Worker, error) {
	database, ok := config.Database.(*postgresx.Store)
	if !ok || database == nil {
		return nil, errors.New("ADX export requires canonical PostgreSQL")
	}
	if !config.SingleReplicaConfirmed || config.HostProfile.Listener == HostListenerPlatform ||
		config.HostProfile.Authentication != HostAuthenticationOAuth {
		return nil, errors.New("ADX export requires an OAuth host with a confirmed single replica and a persistent worker lifecycle")
	}
	if config.Collector != nil && config.Collector.AdmitOnly {
		return nil, errors.New("ADX export is not supported by an admission-only host")
	}
	if capabilities.Coordination.Persistence != operational.PersistenceRestart ||
		capabilities.Coordination.Scope != operational.ScopeDeployment {
		return nil, errors.New("ADX export requires durable deployment-scoped operational state")
	}
	client, err := adxingest.NewClient(*config.ADX)
	if err != nil {
		return nil, err
	}
	return adxingest.NewWorker(*config.ADX, database, client, services)
}

// ADXStatus is independent of ordinary dashboard readiness. Disabled mode
// creates no worker, destination client, token source or delivery state.
func (a *App) ADXStatus() adxingest.Status {
	if a.adx == nil {
		return adxingest.Status{Stage: "disabled"}
	}
	return a.adx.Status()
}
