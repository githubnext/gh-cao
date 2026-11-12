package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/server"
)

type localQueueRequest struct {
	Operation  string                           `json:"operation"`
	ID         int64                            `json:"id"`
	Key        string                           `json:"key"`
	RetryOf    int64                            `json:"retryOf"`
	Invocation operational.CLICommandInvocation `json:"invocation"`
	Result     operational.CLICommandResult     `json:"result"`
	Stream     string                           `json:"stream"`
	Data       string                           `json:"data"`
	After      int64                            `json:"after"`
	Limit      int                              `json:"limit"`
	Offset     int                              `json:"offset"`
	Statuses   []operational.CLICommandStatus   `json:"statuses"`
}

func newLocalQueueCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "local-queue", Hidden: true, Args: cobra.NoArgs,
		Short: "own a local PostgreSQL command queue for the canvas adapter"}
	namespace, namespaceErr := registerRedisNamespaceFlag(cmd, "operational namespace")
	queueName := cmd.Flags().String("queue", "canvas-actions", "command queue name")
	concurrency := cmd.Flags().Int("concurrency", 4, "maximum active commands")
	timeout := cmd.Flags().Duration("timeout", 15*time.Minute, "command deadline")
	recovery := cmd.Flags().Bool("acknowledge-recovery", false, "acknowledge independently reconciled interrupted execution")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if namespaceErr != nil {
			return namespaceErr
		}
		dsn, _, err := resolvePostgresEndpoint("", os.Getenv("CAO_POSTGRES_URL"))
		if err != nil {
			return err
		}
		config, err := server.PostgresOperationalConfigFromEnv()
		if err != nil {
			return err
		}
		scope, _ := resolveNamespaceDefault(os.Getenv("CAO_OPERATIONAL_NAMESPACE"), *namespace)
		store, err := postgres.Open(cmd.Context(), dsn, scope, config)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		queue, err := store.OpenCLICommandQueue(cmd.Context(), *queueName,
			operational.CLICommandQueueOptions{MaxConcurrency: *concurrency, Timeout: *timeout})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		lease, err := queue.Acquire(ctx, "canvas-"+rand.Text(), 30*time.Second)
		if err != nil {
			return err
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			if err := queue.Release(cleanup, lease); err != nil {
				commandLog.Printf("local command queue release failed")
			}
		}()
		if *recovery {
			if err := queue.AcknowledgeRecovery(ctx, lease); err != nil {
				return err
			}
		}
		var work sync.WaitGroup
		work.Go(func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := queue.Renew(ctx, lease, 30*time.Second); err != nil {
						commandLog.Printf("local command queue ownership lost")
						cancel()
						return
					}
				}
			}
		})
		defer func() { cancel(); work.Wait() }()
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if err := encoder.Encode(map[string]bool{"ready": true}); err != nil {
			return err
		}
		scanner := bufio.NewScanner(cmd.InOrStdin())
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			var request localQueueRequest
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				return errors.New("invalid local queue request")
			}
			value, err := runLocalQueueRequest(ctx, queue, lease, request)
			response := map[string]any{"value": value}
			if err != nil {
				response = map[string]any{"error": err.Error()}
			}
			if err := encoder.Encode(response); err != nil {
				return err
			}
		}
		return scanner.Err()
	}
	return cmd
}

func runLocalQueueRequest(ctx context.Context, queue operational.CLICommandQueue, lease operational.CLICommandLease, r localQueueRequest) (any, error) {
	switch r.Operation {
	case "enqueue":
		return queue.Enqueue(ctx, r.Invocation, r.Key, r.RetryOf)
	case "claim":
		return queue.Claim(ctx, lease)
	case "detail":
		value, err := queue.Detail(ctx, r.ID)
		if errors.Is(err, operational.ErrCLICommandNotFound) {
			return nil, nil
		}
		return value, err
	case "list":
		return queue.List(ctx, operational.CLICommandFilter{Statuses: r.Statuses, Limit: r.Limit, Offset: r.Offset})
	case "output":
		return queue.Output(ctx, r.ID, r.After, r.Limit)
	case "cancel":
		return queue.Cancel(ctx, r.ID)
	case "append":
		return queue.AppendOutput(ctx, lease, r.ID, r.Stream, operational.RedactCLICommandText(r.Data))
	case "finish":
		return nil, queue.Finish(ctx, lease, r.ID, r.Result)
	case "settle":
		return nil, queue.Settle(ctx, lease, r.ID, r.Result.ExitCode)
	case "retain":
		return queue.Retain(ctx)
	default:
		return nil, fmt.Errorf("unsupported local queue operation: %s", r.Operation)
	}
}
