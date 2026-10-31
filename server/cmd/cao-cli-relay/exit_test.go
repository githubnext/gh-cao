package main

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestWorkloadExitCode(t *testing.T) {
	if got := workloadExitCode(nil); got != 0 {
		t.Fatalf("nil error = %d, want 0", got)
	}

	err := exec.Command("sh", "-c", "exit 7").Run()
	if got := workloadExitCode(err); got != 7 {
		t.Fatalf("exit 7 = %d, want 7", got)
	}

	if got := workloadExitCode(errors.New("start failed")); got != 125 {
		t.Fatalf("non-exit error = %d, want 125", got)
	}

	if err := exec.Command("/nonexistent/cao-relay-workload").Run(); err == nil {
		t.Fatal("expected start failure")
	} else if got := workloadExitCode(err); got != 125 {
		t.Fatalf("missing executable = %d, want 125", got)
	}
}

func TestWorkloadExitCodeSignalKilled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := exec.CommandContext(ctx, "sleep", "5").Run()
	if err == nil {
		t.Fatal("expected cancelled command to fail")
	}
	if got := workloadExitCode(err); got != 125 {
		t.Fatalf("cancelled workload = %d, want 125", got)
	}
}
