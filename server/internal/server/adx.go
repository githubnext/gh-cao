package server

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/model"
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

func resolveTraceConfig(config Config) (adxtrace.Config, error) {
	var traceConfig adxtrace.Config
	var err error
	if config.ADXTraces != nil {
		traceConfig = *config.ADXTraces
		err = traceConfig.Validate()
	} else {
		traceConfig, err = adxtrace.FromEnv(os.Getenv)
	}
	if err != nil {
		return traceConfig, err
	}
	if !traceConfig.Enabled {
		return traceConfig, nil
	}
	if config.ADX == nil || !config.ADX.Enabled || config.Collector == nil ||
		config.Collector.AdmitOnly || config.Collector.Workers <= 0 {
		return adxtrace.Config{}, errors.New("trace capture requires enabled ADX and co-resident collection workers")
	}
	for _, repo := range traceConfig.Repositories {
		if !slices.Contains(config.ADX.RepositoryIDs, "repository:"+model.EncodeCoordinate(repo)) {
			return adxtrace.Config{}, errors.New("trace scope must be a subset of the exact ADX repository scope")
		}
	}
	return traceConfig, nil
}

func newTraceWorker(ctx context.Context, config Config, collector *Collector) (*adxtrace.Worker, error) {
	if collector == nil || collector.config.Workers <= 0 {
		return nil, errors.New("trace capture requires co-resident acquisition")
	}
	client, err := adxingest.NewClient(*config.ADX)
	if err != nil {
		return nil, err
	}
	return adxtrace.New(ctx, *config.ADXTraces, adxtrace.Namespace(config.ADX.Endpoint, config.ADX.Database), client)
}

func (a *App) ADXTraceStatus() adxtrace.Status {
	if a.adxTraces == nil {
		return adxtrace.Status{Stage: "disabled"}
	}
	return a.adxTraces.Status()
}
