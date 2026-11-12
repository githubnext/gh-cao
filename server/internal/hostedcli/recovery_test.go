package hostedcli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type queueWithoutExecutionStorage struct{ operational.CLICommandQueue }

func TestExecutorRequiresDurableExecutionStorage(t *testing.T) {
	_, err := NewExecutor(queueWithoutExecutionStorage{newFakeQueue()}, NewBroker(newFakeMinter(), BrokerOptions{}),
		fakeGitHub{permission: "write"}, &fakeRunner{}, Options{Owner: "test"})
	if err == nil || !strings.Contains(err.Error(), "durable container identity") {
		t.Fatalf("unsupported queue accepted: %v", err)
	}
}

func TestExecutorKeepsUnconfirmedContainerClaimAcrossRestart(t *testing.T) {
	for _, scenario := range []string{"create", "start", "output-error", "output-zero", "cancel", "timeout", "crash"} {
		// execute is called directly, without the drain loop's lease renewal.
		run := func(t *testing.T) {
			ctx := t.Context()
			store, dsn, namespace := testutil.OperationalPostgres(t)
			defer func() { _ = store.Close() }()
			options := operational.CLICommandQueueOptions{MaxConcurrency: 1}
			if scenario == "timeout" {
				options.Timeout = 20 * time.Millisecond
			}
			queue, err := store.OpenCLICommandQueue(ctx, QueueName, options)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := queue.Acquire(ctx, "first-executor", 200*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			invocation := operational.CLICommandInvocation{ActionID: "test", Command: "gh aw upgrade --repo octo/app --create-pull-request", Repository: "octo/app", Actor: "mona", ActorID: 101}
			first, err := queue.Enqueue(ctx, invocation, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			second, err := queue.Enqueue(ctx, invocation, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			command, err := queue.Claim(ctx, lease)
			if err != nil || command == nil {
				t.Fatalf("claim: %v", err)
			}
			runner, docker, _ := containerTest(t)
			base := docker.run
			cleanupFails := true
			var workspace string
			docker.run = func(runCtx context.Context, p Process, output OutputFunc) (int, error) {
				switch p.Args[2] {
				case "create":
					executions, err := queue.(operational.CLICommandExecutionQueue).UnsettledExecutions(ctx, lease)
					if err != nil || len(executions) != 1 || executions[0].CommandID != first.ID {
						t.Fatalf("container started without durable identity: %+v %v", executions, err)
					}
					mount := p.Args[slices.Index(p.Args, "--mount")+1]
					workspace = strings.TrimSuffix(strings.TrimPrefix(mount, "type=bind,src="), ",dst=/workspace,rw")
					if scenario == "create" {
						return -1, errors.New("create response lost")
					}
				case "start":
					if scenario == "start" {
						return -1, errors.New("start response lost")
					}
				case "logs":
					switch scenario {
					case "crash":
						panic("simulated executor crash")
					case "output-error":
						return -1, errors.New("output transport lost")
					case "cancel":
						if _, err := queue.Cancel(ctx, first.ID); err != nil {
							t.Fatal(err)
						}
						<-runCtx.Done()
						return -1, runCtx.Err()
					case "timeout":
						<-runCtx.Done()
						return -1, runCtx.Err()
					}
				case "inspect":
					output("stdout", []byte(`{"Status":"running","Running":true,"Restarting":false,"ExitCode":0}`))
					return 0, nil
				case "rm":
					if cleanupFails {
						return 1, errors.New("Docker cleanup unavailable")
					}
				}
				return base(runCtx, p, output)
			}
			minter := newFakeMinter()
			executor, err := NewExecutor(queue, NewBroker(minter, BrokerOptions{}), fakeGitHub{permission: "write"}, runner,
				Options{Owner: "first-executor", WorkRoot: t.TempDir(), CancelPollInterval: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "crash" {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("expected simulated crash")
						}
					}()
					executor.execute(ctx, lease, *command)
				}()
			} else {
				executor.execute(ctx, lease, *command)
			}
			detail, err := queue.Detail(ctx, first.ID)
			if err != nil || (scenario != "crash" && !detail.Status.Terminal()) || !detail.ExecutionClaimed || detail.ExitCode != nil {
				t.Fatalf("uncertain workload was settled: %+v %v", detail, err)
			}
			if scenario == "timeout" && detail.Status != operational.CLICommandTimedOut {
				t.Fatalf("command did not reach its execution deadline: %+v", detail)
			}
			if minter.liveCount() != 0 {
				t.Fatal("uncertain workload token was not revoked")
			}
			if events, err := store.ListAudit(ctx, operational.AuditFilter{Event: "cli_command.finished"}); err != nil || len(events) != 0 {
				t.Fatal("uncertain VM termination produced completion evidence", err)
			}
			if _, err := os.Stat(workspace); err != nil {
				t.Fatalf("live workload workspace was removed: %v", err)
			}
			next, err := queue.Claim(ctx, lease)
			if err != nil || next != nil {
				t.Fatalf("occupied capacity admitted another command: %+v %v", next, err)
			}
			if err := queue.Release(ctx, lease); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("unsettled lease released: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = postgres.Open(ctx, dsn, namespace, postgres.Config{})
			if err != nil {
				t.Fatal(err)
			}
			queue, err = store.OpenCLICommandQueue(ctx, QueueName, options)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Until(lease.ExpiresAt))
			replacement, err := queue.Acquire(ctx, "replacement-executor", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			executor.queue = queue
			executor.executions = queue.(operational.CLICommandExecutionQueue)
			if err := executor.recover(ctx, replacement); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
				t.Fatalf("failed cleanup acknowledged recovery: %v", err)
			}
			if _, err := queue.Claim(ctx, replacement); !errors.Is(err, operational.ErrCLIQueueRecovery) {
				t.Fatalf("recovery failed open: %v", err)
			}
			if err := queue.Settle(ctx, lease, first.ID, nil); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("stale settlement bypassed fence: %v", err)
			}
			cleanupFails = false
			if err := executor.recover(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			events, err := store.ListAudit(ctx, operational.AuditFilter{Event: "cli_command.finished"})
			if err != nil || len(events) != 1 || events[0].Attributes["execution_claimed"] != "false" || events[0].Attributes["recovered"] != "true" {
				t.Fatal("confirmed recovery did not atomically record settlement", err)
			}
			next, err = queue.Claim(ctx, replacement)
			if err != nil || next == nil || next.ID != second.ID {
				t.Fatalf("confirmed recovery did not release capacity: %+v %v", next, err)
			}
		}
		t.Run(scenario, run)
	}
}

func TestExecutorRecoveryFailsClosedWithoutReconciler(t *testing.T) {
	queue := &blockedQueue{fakeQueue: newFakeQueue()}
	queue.commands[1] = &operational.CLICommand{ExecutionClaimed: true}
	queue.executions[1] = operational.CLICommandExecution{CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: "original-daemon"}
	executor, err := NewExecutor(queue, NewBroker(newFakeMinter(), BrokerOptions{}), fakeGitHub{permission: "write"}, &fakeRunner{}, Options{Owner: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.recover(t.Context(), operational.CLICommandLease{}); err == nil || !strings.Contains(err.Error(), "cannot reconcile") {
		t.Fatalf("missing reconciler accepted: %v", err)
	}
}

type blockedQueue struct{ *fakeQueue }

func (*blockedQueue) List(context.Context, operational.CLICommandFilter) (operational.CLICommandPage, error) {
	return operational.CLICommandPage{RecoveryBlocked: true}, nil
}
