package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// LocalOperatorActionsConfig is supplied only by the owning native CLI.
// Credentials stay in that process; confirmed commands execute from the queue.
type LocalOperatorActionsConfig struct {
	Repositories []string `json:"repositories"`
}

// LocalOperatorConfig binds queue-authorized maintenance independently of
// the canonical PostgreSQL ingestion connection.
type LocalOperatorConfig struct {
	AdapterURL      string                      `json:"adapterUrl"`
	OperatorActions *LocalOperatorActionsConfig `json:"operatorActions,omitempty"`
	ManagedUpdates  bool                        `json:"managedUpdates,omitempty"`
}

func (c *LocalOperatorActionsConfig) allows(repository string) bool {
	return slices.Contains(c.Repositories, strings.ToLower(repository))
}

func (a *App) operatorConnection() *LocalOperatorConfig {
	return a.config.OperatorActions
}

func (a *App) enableOperatorActions(ctx context.Context) error {
	connection := a.operatorConnection()
	if connection == nil || connection.OperatorActions == nil {
		return errors.New("operator actions require an explicit native adapter")
	}
	if !validOperatorAdapterURL(connection.AdapterURL) {
		return errors.New("operator adapter requires a private numeric loopback HTTP address")
	}
	config := connection.OperatorActions
	if a.oauth != nil || len(config.Repositories) == 0 || len(config.Repositories) > 20 {
		return errors.New("operator actions require bounded exact repository scope")
	}
	for _, repository := range config.Repositories {
		if !hostedcli.ValidRepository(repository) || strings.Contains(repository, "..") || repository != strings.ToLower(repository) {
			return errors.New("invalid operator repository scope")
		}
	}
	catalog, err := hostedcli.LoadCatalog(dashboardassets.Path(a.config.SiteDirectory, "dashboard.json"))
	if err != nil {
		return err
	}
	if a.services.CLICommands == nil || a.services.Audit == nil {
		return errors.New("operator actions require PostgreSQL command and audit services")
	}
	queue, err := a.services.CLICommands.OpenCLICommandQueue(ctx, "operator-cli", operational.CLICommandQueueOptions{MaxConcurrency: 1})
	if err != nil {
		return err
	}
	audited, ok := queue.(operational.AuditedCLICommandQueue)
	if !ok {
		return errors.New("operator actions require atomic queue and audit persistence")
	}
	a.config.Audit = a.config.Audit.WithStore(a.services.Audit)
	if !a.config.Audit.Durable() {
		return errors.New("operator actions require durable audit persistence")
	}
	a.hostedCLI = &hostedCLI{
		queue: audited, catalog: catalog,
		context: hostedcli.TemplateContext{}, local: config,
	}
	return nil
}

func (a *App) admitOperatorAction(ctx context.Context, request hostedcli.Request) (operational.CLICommandInvocation, error) {
	switch request.ID {
	case "upgrade-repository", "upgrade-target-repository", "update-campaign", "update-workflow-source", "update-workflows", "update-repository", "update-repository-sources", "enable-repository-workflows", "enable-workflow", "disable-repository-workflows", "disable-workflow", "add-repository", "setup-cao":
	default:
		return operational.CLICommandInvocation{}, cliFailure(http.StatusForbidden, "Only declared setup, repository maintenance, and enrollment actions are available.")
	}
	invocation, err := a.hostedCLI.catalog.Resolve(request, a.hostedCLI.context)
	if err != nil {
		return invocation, err
	}
	plan, err := hostedcli.PlanLocalMaintenance(invocation.Command)
	if err != nil {
		return invocation, cliFailure(http.StatusUnprocessableEntity, "Maintenance requires an exact target and supported options; updates and upgrades must publish a pull request.")
	}
	if !hostedcli.LocalRepositoryOperationsAllowed(plan.Repository) {
		return invocation, cliFailure(http.StatusForbidden, "github/gh-aw is excluded from CAO operations.")
	}
	if plan.Kind == hostedcli.KindCaoSetup {
		if request.ID != "setup-cao" {
			return invocation, cliFailure(http.StatusForbidden, "Only the declared setup action may prepare local CAO policy.")
		}
		invocation.Repository = plan.Repository
		return invocation, nil
	}
	if plan.Kind == hostedcli.KindCaoReposAdd {
		if request.ID != "add-repository" {
			return invocation, cliFailure(http.StatusForbidden, "Only the declared enrollment action may stage repository policy.")
		}
		invocation.Repository = plan.Repository
		return invocation, nil
	}
	allowed, err := a.operatorAllows(ctx, a.hostedCLI.local, plan)
	if err != nil {
		return invocation, err
	}
	if request.ID == "add-repository" || !allowed {
		return invocation, cliFailure(http.StatusForbidden, "The command is outside the approved local CAO maintenance scope.")
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
		result.Error = "Native maintenance failed; inspect command output."
		var rejected *hostedCLIError
		if errors.As(executionErr, &rejected) {
			result.Error = rejected.message
		}
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

func (a *App) callOperatorAdapter(ctx context.Context, command operational.CLICommand, output func(string, []byte)) (exitCode *int, executionErr error) {
	defer func() {
		var failure *hostedCLIError
		if errors.As(executionErr, &failure) {
			output("stderr", []byte(failure.message+"\n"))
		}
	}()
	rejected := 1 // A pre-dispatch failure is confirmed not to have started a native process.
	connection := a.operatorConnection()
	if connection == nil {
		return &rejected, cliFailure(http.StatusServiceUnavailable, "Native operator adapter is unavailable; no command was started.")
	}
	plan, err := hostedcli.PlanLocalMaintenance(command.Command)
	if err != nil {
		return &rejected, cliFailure(http.StatusUnprocessableEntity, "Queued command is invalid; no command was started.")
	}
	if !hostedcli.LocalRepositoryOperationsAllowed(plan.Repository) {
		return &rejected, cliFailure(http.StatusForbidden, "github/gh-aw is excluded from CAO operations; no command was started.")
	}
	// Enrollment only stages local review-mode policy; the native CLI verifies
	// access after the operator confirms the command in the dashboard.
	if !strings.EqualFold(plan.Repository, command.Repository) || connection.OperatorActions == nil {
		return &rejected, cliFailure(http.StatusForbidden, "Queued command does not match the approved repository; no command was started.")
	}
	if plan.Kind != hostedcli.KindCaoReposAdd {
		allowed, err := a.operatorAllows(ctx, connection.OperatorActions, plan)
		if err != nil {
			return &rejected, err
		}
		if !allowed {
			return &rejected, cliFailure(http.StatusForbidden, "The repository is outside the current local CAO maintenance scope; no command was started.")
		}
	}
	content, err := json.Marshal(plan.Args)
	if err != nil {
		return nil, err
	}
	// Cancellation uses a separate request. Keep the output RPC attached until
	// the owning CLI confirms that its subprocess has actually exited.
	rpcCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 16*time.Minute)
	defer stop()
	request, err := http.NewRequestWithContext(rpcCtx, http.MethodPost, connection.AdapterURL+"/execute", bytes.NewReader(content))
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
			request, err := http.NewRequestWithContext(cleanup, http.MethodPost, connection.AdapterURL+"/cancel", nil)
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
		if response.StatusCode == http.StatusForbidden {
			return &rejected, cliFailure(http.StatusForbidden, "The native executor rejected the command before execution; check current CAO repository scope.")
		}
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
