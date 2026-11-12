package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewaylocal"
)

var localLog = logger.New("cao:webhook-gateway:local")

type localOptions struct {
	repository, namespace, apiURL string
	events                        []string
	interval                      time.Duration
	once                          bool
}

func runLocal(ctx context.Context, options localOptions, output io.Writer) (err error) {
	// Preserve caller cancellation even when the database reports a driver error.
	defer func(parent context.Context) {
		if err != nil && parent.Err() != nil {
			err = parent.Err()
		}
	}(ctx)
	if strings.TrimSpace(options.namespace) == "" || options.interval < time.Second {
		return errors.New("local simulation requires an explicit PostgreSQL namespace and a polling interval of at least one second")
	}
	store, err := postgres.Open(ctx, os.Getenv("CAO_POSTGRES_URL"), options.namespace, postgres.Config{})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	client, err := webhookgatewaylocal.NewClient(options.apiURL, options.repository, os.Getenv("GH_TOKEN"))
	if err != nil {
		return err
	}
	repository, err := client.Repository(ctx)
	if err != nil {
		return err
	}
	simulation, err := webhookgatewaylocal.NewSimulation(repository, options.events)
	if err != nil {
		return fmt.Errorf("create local webhook simulation: %w", err)
	}
	policy, err := simulation.Load(ctx)
	if err != nil {
		return err
	}
	namespace := policy.Policy.Namespace + "-" + wg.MetadataDigest([]byte(options.apiURL))[:16]
	queue, err := store.OpenWebhookQueue(ctx, namespace, policy.Policy.Queue)
	if err != nil {
		return fmt.Errorf("open local webhook queue: %w", err)
	}
	if err := queue.ApplyRevision(ctx, policy.Revision); err != nil {
		return fmt.Errorf("apply local webhook revision: %w", err)
	}
	if _, err := queue.Maintain(ctx, 512); err != nil {
		return fmt.Errorf("maintain local webhook queue: %w", err)
	}
	if _, err := fmt.Fprintln(output, "LOCAL SIMULATION: GitHub reads only; signed in-process ingress; dispatch acceptance is simulated, not remote workflow execution."); err != nil {
		return err
	}
	poller := &webhookgatewaylocal.Poller{
		Client: client, Repository: repository, Simulation: simulation, Interval: options.interval,
		Receipts: queue.(webhookgatewaylocal.ReceiptReader),
		Handler:  &wg.Ingress{SourceID: "local", Secrets: simulation, Policies: simulation, Queue: queue, Authorizer: simulation},
	}
	worker := &wg.Worker{Policies: simulation, Queue: queue, Authorizer: simulation, Dispatcher: simulation,
		Owner: "local-" + rand.Text(), HTTPTimeout: time.Second, LeaseDuration: time.Minute}
	if !options.once {
		worker.BatchWindow = wg.DispatchBatchWindow
	}
	if options.once {
		if _, err := poller.Poll(ctx); err != nil {
			return fmt.Errorf("poll local webhook events: %w", err)
		}
		for range policy.Policy.Queue.MaxRows {
			worked, err := worker.RunOnce(ctx)
			if err != nil {
				if errors.Is(err, wg.ErrCapacity) {
					break
				}
				return err
			}
			if !worked {
				break
			}
		}
		return reportLocal(ctx, queue, output)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	polled := make(chan error, 1)
	go func() { polled <- poller.Run(ctx) }()
	// Unlike the hosted resilient loop, local development fails visibly on
	// infrastructure errors rather than concealing them behind endless retries.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		worked, err := worker.RunOnce(ctx)
		if err != nil && !errors.Is(err, wg.ErrCapacity) {
			cancel()
			<-polled
			return err
		}
		if err == nil && worked {
			select {
			case err := <-polled:
				cancel()
				return err
			default:
				continue
			}
		}
		select {
		case err := <-polled:
			cancel()
			return err
		case <-ctx.Done():
			cancel()
			<-polled
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func reportLocal(ctx context.Context, queue wg.Queue, output io.Writer) error {
	counter, ok := queue.(interface {
		Counts(context.Context) (map[wg.JobState]int64, error)
	})
	if !ok {
		return errors.New("local simulation requires durable queue counts")
	}
	counts, err := counter.Counts(ctx)
	if err != nil {
		return err
	}
	summary, pending := summarizeLocalQueue(counts)
	if _, err := fmt.Fprintln(output, summary); err != nil {
		return err
	}
	localLog.Printf("local simulation queue reported: pending=%d", pending)
	if pending > 0 {
		return errors.New("local simulation is incomplete: retained work is deferred by leases or budgets")
	}
	return nil
}

// summarizeLocalQueue renders the simulated queue counts and returns the
// number of jobs still pending (ready, claimed or dispatching).
func summarizeLocalQueue(counts map[wg.JobState]int64) (string, int64) {
	pending := counts[wg.Ready] + counts[wg.Claimed] + counts[wg.Dispatching]
	return fmt.Sprintf("Simulated queue: succeeded=%d ambiguous=%d rejected=%d failed=%d expired=%d cancelled=%d pending=%d",
		counts[wg.Succeeded], counts[wg.Ambiguous], counts[wg.Rejected], counts[wg.Failed], counts[wg.Expired], counts[wg.Cancelled], pending), pending
}
