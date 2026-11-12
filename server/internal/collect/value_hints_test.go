package collect

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRemoteValueHintsPreserveChangesDuringRefresh(t *testing.T) {
	worker := refreshWorker(t)
	repository := "octo/api"
	advance := func(hint *remoteValueHint) error {
		hint.Dirty++
		return nil
	}
	if err := worker.withRemoteValueHint(t.Context(), repository, advance); err != nil {
		t.Fatal(err)
	}
	started, err := worker.Values.readRemoteValueHint(t.Context(), repository)
	if err != nil || started.Dirty != 1 {
		t.Fatalf("initial hint = %+v, %v", started, err)
	}
	if err := worker.withRemoteValueHint(t.Context(), repository, advance); err != nil {
		t.Fatal(err)
	}
	if err := worker.withRemoteValueHint(t.Context(), repository, func(hint *remoteValueHint) error {
		hint.Processed = started.Dirty
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current, err := worker.Values.readRemoteValueHint(t.Context(), repository)
	if err != nil || current.Dirty != 2 || current.Processed != 1 {
		t.Fatalf("concurrent invalidation lost = %+v, %v", current, err)
	}
}

func TestRemoteValueHintRejectsInvalidGenerations(t *testing.T) {
	worker := refreshWorker(t)
	if err := worker.Values.State.SetOperationalState(context.Background(),
		remoteValueHintKey("octo/api"), []byte(`{"dirty":1,"processed":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Values.readRemoteValueHint(t.Context(), "octo/api"); err == nil {
		t.Fatal("invalid persisted generation was silently accepted")
	}
}

func TestWithdrawalErasesRemoteValueHintsAndPrivateProof(t *testing.T) {
	worker := refreshWorker(t)
	repository := "octo/api"
	if err := worker.Enrollment.AddRepositories(t.Context(), 7, []string{repository}); err != nil {
		t.Fatal(err)
	}
	if err := worker.withRemoteValueHint(t.Context(), repository, func(hint *remoteValueHint) error {
		hint.Dirty = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspace, err := worker.Runner.Lake.remoteValueWorkspace(t.Context(), repository, "daily-file-diet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Enrollment.RemoveRepositories(t.Context(), 7, []string{repository}); err != nil {
		t.Fatal(err)
	}
	if queued, err := worker.Queue.Enqueue(t.Context(), Task{Repository: repository, InstallationID: 7, Erase: true}); err != nil || !queued {
		t.Fatalf("withdrawal work: queued=%t, err=%v", queued, err)
	}
	worker.process(t.Context(), refreshLease(t, worker.Queue))
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("withdrawn private proof remains: %v", err)
	}
	hint, err := worker.Values.readRemoteValueHint(t.Context(), repository)
	if err != nil || hint != (remoteValueHint{}) {
		t.Fatalf("withdrawn hint remains: %+v, %v", hint, err)
	}
}

func TestRemoteValueSweepAttemptsWithoutAWebhookAndBacksOffOnFailure(t *testing.T) {
	worker := refreshWorker(t)
	repository := "octo/api"
	now := time.Now().UTC()
	if err := worker.collectRemoteValue(t.Context(), repository, now); !errors.Is(err, ErrRemoteValueDeferred) {
		t.Fatalf("daily repair did not try missing collection prerequisites: %v", err)
	}
	hint, err := worker.Values.readRemoteValueHint(t.Context(), repository)
	if err != nil || hint.RetryAt == "" || hint.AttemptDay != "" {
		t.Fatalf("failed sweep did not persist its retry clock: %+v, %v", hint, err)
	}
	if err := worker.collectRemoteValue(t.Context(), repository, now.Add(time.Minute)); err != nil {
		t.Fatalf("retry ignored its backoff: %v", err)
	}
}
