package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/audit"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/server"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var auditCommandLog = debuglogger.New("cao:dashboard:audit")

// newAuditCommand inspects and exports the tamper-evident audit table for
// operational evidence and SIEM backfill.
func newAuditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "inspect, verify, or export the tamper-evident operational audit table",
	}
	cmd.AddCommand(newAuditVerifyCommand(), newAuditExportCommand(), newAuditDeliverCommand(), newAuditLifecyclesCommand())
	return cmd
}

func newAuditVerifyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "recompute the audit hash chain, check external anchors, and print the chain head",
		Args:  cobra.NoArgs,
	}
	rawAnchors := cmd.Flags().StringArray("anchor", nil,
		"previously reported chain position SEQUENCE:HASH kept outside the database; repeatable")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		anchors, err := parseAuditAnchors(*rawAnchors)
		if err != nil {
			return err
		}
		return runAuditCommand(func(ctx context.Context, store operational.AuditLog) error {
			return verifyAudit(ctx, store, anchors, cmd.OutOrStdout())
		})
	}
	return cmd
}

func newAuditExportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "write retained audit events as JSON lines in ascending sequence order",
		Args:  cobra.NoArgs,
	}
	after := cmd.Flags().Int64("after", 0, "export only events with a sequence greater than this")
	event := cmd.Flags().String("event", "", "export only this event name")
	actor := cmd.Flags().String("actor", "", "export only events by this actor")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if *after < 0 {
			return errors.New("--after must not be negative")
		}
		return runAuditCommand(func(ctx context.Context, store operational.AuditLog) error {
			return exportAudit(ctx, store, operational.AuditFilter{AfterSequence: *after, Event: *event, Actor: *actor}, cmd.OutOrStdout())
		})
	}
	return cmd
}

func runAuditCommand(run func(context.Context, operational.AuditLog) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	closeTelemetry, err := setupTelemetry(ctx, version, telemetry.Setup)
	if err != nil {
		return err
	}
	defer closeTelemetry()
	store, closeStore, err := server.OpenAuditStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = closeStore() }()
	return run(ctx, store)
}

// verifyAudit records its own outcome in the trail, so the reported head is
// also exported through the JSON mirror and OpenTelemetry logs, where it can
// serve as an external anchor for later runs.
func verifyAudit(ctx context.Context, store operational.AuditLog, anchors []operational.AuditAnchor, out io.Writer) error {
	result, verifyErr := store.VerifyAudit(ctx, anchors...)
	auditLog, err := audit.FromEnv()
	if err != nil {
		return err
	}
	auditLog = auditLog.WithStore(store)
	attrs := verifyAttrs(result)
	auditCommandLog.Printf("audit chain verified: events=%d anchors_checked=%d anchors_expired=%d failed=%t",
		result.Events, result.AnchorsChecked, result.AnchorsExpired, verifyErr != nil)
	if verifyErr != nil {
		_ = auditLog.Record(ctx, "audit.verify_failed", audit.Failure, attrs...)
		return verifyErr
	}
	if err := auditLog.Record(ctx, "audit.verified", audit.Success, attrs...); err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	return encoder.Encode(result)
}

// verifyAttrs describes a verification result as audit record attributes.
func verifyAttrs(result operational.AuditVerification) []slog.Attr {
	return []slog.Attr{slog.Int64("events", result.Events), slog.Int64("first_sequence", result.FirstSequence),
		slog.Int64("last_sequence", result.LastSequence), slog.String("last_hash", result.LastHash),
		slog.Int("anchors_checked", result.AnchorsChecked), slog.Int("anchors_expired", result.AnchorsExpired)}
}

func exportAudit(ctx context.Context, store operational.AuditLog, filter operational.AuditFilter, out io.Writer) error {
	encoder := json.NewEncoder(out)
	filter.Limit = operational.AuditListLimit
	exported, pages := 0, 0
	for {
		events, err := store.ListAudit(ctx, filter)
		if err != nil {
			return err
		}
		pages++
		for _, event := range events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
			filter.AfterSequence = event.Sequence
			exported++
		}
		if len(events) < filter.Limit {
			auditCommandLog.Printf("audit export complete: events=%d pages=%d", exported, pages)
			return nil
		}
	}
}

func parseAuditAnchors(values []string) ([]operational.AuditAnchor, error) {
	anchors := make([]operational.AuditAnchor, 0, len(values))
	for _, value := range values {
		rawSequence, hash, ok := strings.Cut(strings.TrimSpace(value), ":")
		sequence, err := strconv.ParseInt(rawSequence, 10, 64)
		if !ok || err != nil {
			return nil, fmt.Errorf("--anchor %q must be SEQUENCE:HASH", value)
		}
		anchors = append(anchors, operational.AuditAnchor{Sequence: sequence, Hash: hash})
	}
	return anchors, nil
}
