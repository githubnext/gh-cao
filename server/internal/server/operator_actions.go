package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalsqlite "github.com/githubnext/gh-cao/server/internal/operational/sqlite"
)

// LocalOperatorActionsConfig is supplied only by the owning native CLI.
// Credentials and terminal approval stay in that process, not in this server.
type LocalOperatorActionsConfig struct {
	QueuePath    string   `json:"queuePath"`
	Repositories []string `json:"repositories"`
}

func (c *LocalOperatorActionsConfig) allows(repository string) bool {
	return slices.Contains(c.Repositories, strings.ToLower(repository))
}

func (a *App) enableOperatorActions(ctx context.Context) error {
	config := a.config.Canvas.OperatorActions
	if a.oauth != nil || len(config.Repositories) == 0 || len(config.Repositories) > 20 || !filepath.IsAbs(config.QueuePath) {
		return errors.New("operator actions require a private queue and bounded exact repository scope")
	}
	for _, repository := range config.Repositories {
		if !hostedcli.ValidRepository(repository) || strings.Contains(repository, "..") || repository != strings.ToLower(repository) {
			return errors.New("invalid operator repository scope")
		}
	}
	catalog, err := hostedcli.LoadCatalog(filepath.Join(a.config.SiteDirectory, "dashboard.json"))
	if err != nil {
		return err
	}
	store, err := operationalsqlite.Open(ctx, config.QueuePath)
	if err != nil {
		return err
	}
	queue, err := store.OpenCLICommandQueue(ctx, "operator-cli", operational.CLICommandQueueOptions{MaxConcurrency: 1})
	if err != nil {
		_ = store.Close()
		return err
	}
	a.config.Audit = a.config.Audit.WithStore(store)
	a.operatorStore = store
	a.hostedCLI = &hostedCLI{
		queue: queue.(operational.AuditedCLICommandQueue), catalog: catalog,
		context: hostedcli.TemplateContext{}, local: config,
	}
	return nil
}

func (a *App) admitOperatorAction(request hostedcli.Request) (operational.CLICommandInvocation, error) {
	switch request.ID {
	case "upgrade-repository", "upgrade-target-repository", "update-campaign", "update-workflow-source", "update-repository":
	default:
		return operational.CLICommandInvocation{}, cliFailure(http.StatusForbidden, "Only declared repository maintenance actions are available.")
	}
	invocation, err := a.hostedCLI.catalog.Resolve(request, a.hostedCLI.context)
	if err != nil {
		return invocation, err
	}
	control := ""
	if len(a.hostedCLI.local.Repositories) == 1 {
		control = a.hostedCLI.local.Repositories[0]
	}
	plan, err := hostedcli.PlanCommand(invocation.Command, control)
	if err != nil || plan.Kind != hostedcli.KindGhAwUpgrade && plan.Kind != hostedcli.KindGhAwUpdate {
		return invocation, cliFailure(http.StatusUnprocessableEntity, "Maintenance must publish a pull request with supported gh-aw options.")
	}
	if !a.hostedCLI.local.allows(plan.Repository) {
		return invocation, cliFailure(http.StatusForbidden, "The command is outside the selected repository scope.")
	}
	invocation.Repository = plan.Repository
	return invocation, nil
}

func (a *App) runOperatorCommands(ctx context.Context) {
	queue := a.hostedCLI.queue
	lease, err := queue.Acquire(ctx, "native-dashboard", 30*time.Second)
	if err != nil {
		a.config.Logger.Printf("operator command queue could not acquire ownership: %v", err)
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := queue.Release(cleanup, lease); err != nil {
			a.config.Logger.Printf("operator command queue release failed: %v", err)
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var retainedAt time.Time
	for ctx.Err() == nil {
		if _, err := queue.Renew(ctx, lease, 30*time.Second); err != nil {
			a.config.Logger.Printf("operator command queue ownership lost: %v", err)
			return
		}
		if time.Since(retainedAt) >= time.Minute {
			if _, err := queue.Retain(ctx); err != nil {
				a.config.Logger.Printf("operator command queue retention failed: %v", err)
				return
			}
			retainedAt = time.Now()
		}
		command, err := queue.Claim(ctx, lease)
		if err != nil {
			a.config.Logger.Printf("operator command queue requires attention: %v", err)
			return
		}
		if command != nil && !a.executeOperatorCommand(ctx, lease, *command) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) executeOperatorCommand(parent context.Context, lease operational.CLICommandLease, command operational.CLICommand) bool {
	queue := a.hostedCLI.queue
	deadline := time.Now().Add(15 * time.Minute)
	if command.Deadline != nil {
		deadline = *command.Deadline
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := queue.Renew(ctx, lease, 30*time.Second)
				current, detailErr := queue.Detail(ctx, command.ID)
				if err != nil || detailErr != nil || current.Status.Terminal() {
					cancel()
					return
				}
			}
		}
	}()
	output := hostedcli.NewCommandOutput(ctx, queue, lease, command.ID)
	code, executionErr := a.callOperatorAdapter(ctx, command, output.Write)
	output.Close() //nolint:contextcheck // the sink carries the command context, including during cancellation.
	cancel()
	<-done
	cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer stop()
	result := operational.CLICommandResult{Status: operational.CLICommandCompleted, ExitCode: code}
	if executionErr != nil || code == nil || *code != 0 {
		result.Status = operational.CLICommandFailed
		result.Error = "Maintenance failed or terminal approval was declined; inspect command output."
	}
	if command.Deadline != nil && !time.Now().Before(*command.Deadline) {
		result.Status = operational.CLICommandTimedOut
		result.Error = "Maintenance exceeded its execution deadline."
	}
	if code == nil {
		a.config.Logger.Printf("operator command completion is unconfirmed; queue recovery is required: %v", executionErr)
		// A disconnected RPC is not proof that the native subprocess exited.
		return false
	}
	if err := queue.Finish(cleanup, lease, command.ID, result); err != nil {
		a.config.Logger.Printf("operator command result could not be persisted: %v", err)
		return false
	}
	if err := queue.Settle(cleanup, lease, command.ID, code); err != nil {
		a.config.Logger.Printf("operator command settlement failed: %v", err)
		return false
	}
	return true
}

func (a *App) callOperatorAdapter(ctx context.Context, command operational.CLICommand, output func(string, []byte)) (*int, error) {
	plan, err := hostedcli.PlanCommand(command.Command, command.Repository)
	if err != nil {
		return nil, err
	}
	content, err := json.Marshal(plan.Args)
	if err != nil {
		return nil, err
	}
	// Cancellation uses a separate request. Keep the output RPC attached until
	// the owning CLI confirms that its subprocess has actually exited.
	rpcCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 16*time.Minute)
	defer stop()
	request, err := http.NewRequestWithContext(rpcCtx, http.MethodPost, a.config.Canvas.AdapterURL+"/execute", bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Command-ID", fmt.Sprint(command.ID))
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	defer client.CloseIdleConnections()
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-watchDone:
			return
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(cleanup, http.MethodPost, a.config.Canvas.AdapterURL+"/cancel", nil)
			if err == nil {
				request.Header.Set("X-Command-ID", fmt.Sprint(command.ID))
				response, cancelErr := client.Do(request)
				if cancelErr == nil {
					_ = response.Body.Close()
					if response.StatusCode == http.StatusAccepted {
						return
					}
				}
			}
			a.config.Logger.Printf("operator cancellation could not be confirmed")
		}
	}()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("operator adapter returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 2<<20))
	for {
		var event struct {
			Stream   string `json:"stream"`
			Data     string `json:"data"`
			Complete bool   `json:"complete"`
			ExitCode *int   `json:"exitCode"`
		}
		if err := decoder.Decode(&event); err != nil {
			return nil, err
		}
		if event.Stream == "stdout" || event.Stream == "stderr" {
			output(event.Stream, []byte(event.Data))
		}
		if event.Complete {
			return event.ExitCode, nil
		}
	}
}
