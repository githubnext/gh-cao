package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type maintenanceBackend struct {
	operational.Backend
	calls atomic.Int64
	err   error
}

func (b *maintenanceBackend) Maintain(ctx context.Context) error {
	b.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.err
}

func TestOperationalMaintenanceRunsAndStopsWithCancellation(t *testing.T) {
	store := newTestOperationalStore(t)
	backend := &maintenanceBackend{Backend: store}
	services := store.OperationalServices()
	services.Backend = backend
	var output bytes.Buffer
	app := &App{services: services, config: Config{Logger: log.New(&output, "", 0)}}
	if err := app.initializeOperationalMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 1 {
		t.Fatal("startup did not run maintenance")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.runOperationalMaintenance(ctx, time.Millisecond)
	}()
	deadline := time.After(time.Second)
	for backend.calls.Load() < 2 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("periodic maintenance did not run")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop on cancellation")
	}
}

func TestOperationalMaintenanceFailsClosedAndSanitizesErrors(t *testing.T) {
	store := newTestOperationalStore(t)
	private := errors.New("private backend details")
	backend := &maintenanceBackend{Backend: store, err: private}
	services := store.OperationalServices()
	services.Backend = backend
	var output bytes.Buffer
	app := &App{services: services, config: Config{Logger: log.New(&output, "", 0)}}
	err := app.initializeOperationalMaintenance(t.Context())
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("maintenance failure was hidden or exposed backend data: %v", err)
	}
	pressure := &operational.MaintenanceError{UsedBytes: 2, BudgetBytes: 1, Evicted: 1, Cause: private}
	backend.err = pressure
	if err := app.initializeOperationalMaintenance(t.Context()); !errors.Is(err, pressure) {
		t.Fatalf("protected-state pressure was hidden: %v", err)
	}
	if strings.Contains(output.String(), "private") || !strings.Contains(output.String(), "preserve_protected_state") {
		t.Fatalf("unsafe maintenance diagnostics: %s", output.String())
	}
}
