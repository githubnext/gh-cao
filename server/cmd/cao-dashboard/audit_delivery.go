package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/auditdelivery"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/server"
)

var auditDeliveryLog = debuglogger.New("cao:dashboard:audit-delivery")

func newAuditDeliverCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliver",
		Short: "verify and replay one bounded page to an independent HTTPS audit destination",
		Args:  cobra.NoArgs,
	}
	stream := cmd.Flags().String("stream", "", "stable deployment/namespace identity at the independent destination")
	bootstrapPath := cmd.Flags().String("bootstrap-anchor", "", "independently preserved JSON anchor; only used if destination has no checkpoint")
	maxEvents := cmd.Flags().Int("max-events", 100, "maximum committed events per invocation (1-1000)")
	maxAge := cmd.Flags().Duration("max-anchor-age", 24*time.Hour, "maximum age of independent verification evidence (1m-168h)")
	timeout := cmd.Flags().Duration("timeout", 5*time.Minute, "whole invocation deadline (1s-30m), including verification and delivery")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		result := auditdelivery.Result{Status: "failure", Stage: "configuration"}
		runErr := func() error {
			options, err := buildDeliveryOptions(*stream, *maxEvents, *maxAge, *timeout, *bootstrapPath)
			if err != nil {
				return err
			}
			destination, err := auditdelivery.NewHTTPDestination(os.Getenv("CAO_AUDIT_DELIVERY_URL"), os.Getenv("CAO_AUDIT_DELIVERY_TOKEN"))
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()
			result.Stage = "source_open"
			store, closeStore, err := server.OpenAuditReader(ctx)
			if err != nil {
				return errors.New("audit delivery could not open the configured audit reader")
			}
			defer func() { _ = closeStore() }()
			result, err = auditdelivery.Run(ctx, store, destination, options)
			return err
		}()
		out := cmd.OutOrStdout()
		if runErr != nil {
			out = cmd.ErrOrStderr()
		}
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return errors.New("audit delivery could not write its operational result")
		}
		return runErr
	}
	return cmd
}

// buildDeliveryOptions validates the invocation bounds and loads the optional
// bootstrap anchor.
func buildDeliveryOptions(stream string, maxEvents int, maxAge, timeout time.Duration, bootstrapPath string) (auditdelivery.Options, error) {
	if timeout < time.Second || timeout > 30*time.Minute {
		auditDeliveryLog.Printf("rejected delivery options: reason=timeout_out_of_range")
		return auditdelivery.Options{}, errors.New("audit delivery timeout must be between 1s and 30m")
	}
	options := auditdelivery.Options{Stream: stream, MaxEvents: maxEvents, MaxAnchorAge: maxAge}
	if err := options.Validate(); err != nil {
		auditDeliveryLog.Printf("rejected delivery options: reason=invalid_options")
		return auditdelivery.Options{}, err
	}
	if bootstrapPath != "" {
		anchor, err := readBootstrapAnchor(bootstrapPath)
		if err != nil {
			auditDeliveryLog.Printf("rejected delivery options: reason=bootstrap_anchor")
			return auditdelivery.Options{}, err
		}
		options.Bootstrap = &anchor
	}
	return options, nil
}

func readBootstrapAnchor(path string) (auditdelivery.Anchor, error) {
	var anchor auditdelivery.Anchor
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return anchor, errors.New("audit delivery bootstrap anchor must be a regular file of at most 16 KiB")
	}
	file, err := os.Open(path) // #nosec G304 -- explicitly selected operator bootstrap file, read-only and size-bounded.
	if err != nil {
		return anchor, errors.New("audit delivery could not read bootstrap anchor")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(body) > 16<<10 || json.Unmarshal(body, &anchor) != nil {
		return anchor, errors.New("audit delivery bootstrap anchor is unreadable, oversized, or invalid JSON")
	}
	return anchor, nil
}
