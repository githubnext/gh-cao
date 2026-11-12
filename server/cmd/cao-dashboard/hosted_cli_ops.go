package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/server"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var hostedCLILog = debuglogger.New("cao:dashboard:hosted-cli")

var hostedCLOperatorLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:\[bot\])?$`)

type hostedCLIQueueSession struct {
	queue operational.CLICommandQueue
	audit *audit.Logger
}

func newHostedCLICommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosted-cli",
		Short: "operate the hosted CLI command backend",
	}
	cmd.AddCommand(newHostedCLIQueueCommand())
	return cmd
}

func newHostedCLIQueueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "inspect and manage queued hosted CLI actions",
	}
	cmd.AddCommand(newHostedCLIQueueListCommand(), newHostedCLIQueueShowCommand(), newHostedCLIQueueCancelCommand())
	return cmd
}

func newHostedCLIQueueListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list hosted CLI queue summaries",
		Args:  cobra.NoArgs,
	}
	statuses := cmd.Flags().StringArray("status", nil, "filter by command status; repeatable")
	limit := cmd.Flags().Int("limit", 20, "maximum commands to return (1-100)")
	continuationToken := cmd.Flags().String("continuation-token", "", "opaque token returned by the previous page")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		filter, err := hostedCLIQueueFilter(*statuses, *limit, *continuationToken)
		if err != nil {
			return err
		}
		return runHostedCLIQueueCommand(func(ctx context.Context, session hostedCLIQueueSession) error {
			page, err := session.queue.ListBefore(ctx, filter)
			if err != nil {
				return err
			}
			response := map[string]any{
				"commands": hostedCLICommandSummaries(page.Commands),
				"limit":    filter.Limit, "hasMore": page.HasMore,
				"revision": page.Revision, "recoveryBlocked": page.RecoveryBlocked,
			}
			if page.HasMore && len(page.Commands) > 0 {
				response["nextContinuationToken"] = hostedCLIContinuationToken(page.Commands[len(page.Commands)-1].ID)
			}
			return writeHostedCLIQueueJSON(cmd, response)
		})
	}
	return cmd
}

// hostedCLIQueueFilter validates list flags and builds the queue cursor filter.
func hostedCLIQueueFilter(statuses []string, limit int, continuationToken string) (operational.CLICommandCursorFilter, error) {
	filter := operational.CLICommandCursorFilter{Limit: limit}
	for _, raw := range statuses {
		status := operational.CLICommandStatus(strings.TrimSpace(raw))
		if status == "" {
			hostedCLILog.Printf("queue list rejected: reason=empty_status")
			return filter, operational.ErrCLICommandInput
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	if filter.Validate() != nil || filter.Limit > 100 {
		hostedCLILog.Printf("queue list rejected: reason=invalid_filter statuses=%d limit=%d", len(statuses), limit)
		return filter, errors.New("invalid queue filter: limit must be 1-100 and statuses valid")
	}
	if continuationToken != "" {
		beforeID, err := parseHostedCLIContinuationToken(continuationToken)
		if err != nil {
			hostedCLILog.Printf("queue list rejected: reason=invalid_continuation_token")
			return filter, err
		}
		filter.BeforeID = beforeID
	}
	return filter, nil
}

func newHostedCLIQueueShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "show a hosted CLI command summary",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseHostedCLICommandID(args[0])
		if err != nil {
			return err
		}
		return runHostedCLIQueueCommand(func(ctx context.Context, session hostedCLIQueueSession) error {
			command, err := session.queue.Detail(ctx, id)
			if err != nil {
				return err
			}
			return writeHostedCLIQueueJSON(cmd, hostedCLICommandSummary(command))
		})
	}
	return cmd
}

func newHostedCLIQueueCancelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel ID",
		Short: "cancel a queued or running hosted CLI action",
		Args:  cobra.ExactArgs(1),
	}
	operator := cmd.Flags().String("actor", "", "self-reported operator login recorded in the durable audit trail")
	_ = cmd.MarkFlagRequired("actor")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseHostedCLICommandID(args[0])
		if err != nil {
			return err
		}
		login := strings.TrimSpace(*operator)
		if !hostedCLOperatorLogin.MatchString(login) {
			return errors.New("--actor must be a valid GitHub login")
		}
		return runHostedCLIQueueCommand(func(ctx context.Context, session hostedCLIQueueSession) error {
			if err := session.audit.Record(ctx, "cli_command.operator_cancel_requested", audit.Success,
				slog.String("actor", login), slog.String("actor_source", "self_reported_cli"),
				slog.Int64("command_id", id)); err != nil {
				return err
			}
			command, err := session.queue.Cancel(ctx, id)
			if err != nil {
				_ = session.audit.Record(ctx, "cli_command.operator_cancel_failed", audit.Failure,
					slog.String("actor", login), slog.String("actor_source", "self_reported_cli"),
					slog.Int64("command_id", id))
				return err
			}
			hostedCLILog.Printf("queue command cancelled: status=%s", command.Status)
			if err := session.audit.Record(ctx, "cli_command.operator_cancelled", audit.Success,
				slog.String("actor", login), slog.String("actor_source", "self_reported_cli"),
				slog.Int64("command_id", id),
				slog.String("status", string(command.Status))); err != nil {
				return err
			}
			return writeHostedCLIQueueJSON(cmd, hostedCLICommandSummary(command))
		})
	}
	return cmd
}

func runHostedCLIQueueCommand(run func(context.Context, hostedCLIQueueSession) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	closeTelemetry, err := setupTelemetry(ctx, version, telemetry.Setup)
	if err != nil {
		return err
	}
	defer closeTelemetry()

	store, err := server.StoreFromEnv(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	services := store.OperationalServices()
	queues, auditStore := services.CLICommands, services.Audit
	if queues == nil || auditStore == nil {
		return errors.New("hosted CLI queue operations require the operational CLI queue and audit store")
	}
	options, err := (operational.CLICommandQueueOptions{}).Resolve()
	if err != nil {
		return err
	}
	queue, err := queues.OpenCLICommandQueue(ctx, hostedcli.QueueName, options)
	if err != nil {
		return errors.New("open hosted CLI command queue failed")
	}
	auditLog, err := audit.FromEnv()
	if err != nil {
		return err
	}
	auditLog = auditLog.WithStore(auditStore)
	if !auditLog.Durable() {
		return errors.New("hosted CLI queue operations require a durable audit store")
	}
	return run(ctx, hostedCLIQueueSession{queue: queue, audit: auditLog})
}

func parseHostedCLICommandID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("command ID must be a positive integer")
	}
	return id, nil
}

func hostedCLIContinuationToken(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.FormatInt(id, 10)))
}

func parseHostedCLIContinuationToken(token string) (int64, error) {
	if len(token) > 128 {
		return 0, errors.New("invalid continuation token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(decoded), "v1:") {
		return 0, errors.New("invalid continuation token")
	}
	rawID := strings.TrimPrefix(string(decoded), "v1:")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != rawID {
		return 0, errors.New("invalid continuation token")
	}
	return id, nil
}

func hostedCLICommandSummaries(commands []operational.CLICommand) []map[string]any {
	summaries := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		summaries = append(summaries, hostedCLICommandSummary(command))
	}
	return summaries
}

func hostedCLICommandSummary(command operational.CLICommand) map[string]any {
	summary := map[string]any{
		"id": command.ID, "actionId": command.ActionID, "actor": command.Actor,
		"repository": command.Repository, "status": command.Status,
		"createdAt": command.CreatedAt.UTC(), "outputBytes": command.OutputBytes,
		"outputChunks": command.OutputChunks, "outputTruncated": command.OutputTruncated,
		"retryOf": command.RetryOf,
	}
	if command.StartedAt != nil {
		summary["startedAt"] = command.StartedAt.UTC()
	}
	if command.CompletedAt != nil {
		summary["completedAt"] = command.CompletedAt.UTC()
	}
	if command.Deadline != nil {
		summary["deadline"] = command.Deadline.UTC()
	}
	if command.ExitCode != nil {
		summary["exitCode"] = *command.ExitCode
	}
	return summary
}

func writeHostedCLIQueueJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write hosted CLI queue response: %w", err)
	}
	return nil
}
