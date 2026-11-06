//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestDashboardApprovalCancellationPreservesFutureInput(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = output.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	completed := make(chan error, 1)
	go func() {
		_, err := readDashboardApproval(ctx, input)
		completed <- err
	}()
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("approval cancellation was lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal approval did not stop on cancellation")
	}
	if _, err := output.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	next, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	text, err := readDashboardApproval(next, input)
	if err != nil || text != "n\n" {
		t.Fatalf("next approval input was consumed by canceled approval: %q %v", text, err)
	}
}
