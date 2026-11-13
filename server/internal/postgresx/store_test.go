package postgresx

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestClassifyMaintenanceTickSucceeded(t *testing.T) {
	if got := classifyMaintenanceTick(nil, nil); got != maintenanceTickOutcomeSucceeded {
		t.Fatalf("classifyMaintenanceTick(nil, nil) = %q, want %q", got, maintenanceTickOutcomeSucceeded)
	}
	if got := classifyMaintenanceTick(nil, context.Canceled); got != maintenanceTickOutcomeSucceeded {
		t.Fatalf("classifyMaintenanceTick(nil, cancelled) = %q, want %q", got, maintenanceTickOutcomeSucceeded)
	}
}

func TestClassifyMaintenanceTickShuttingDown(t *testing.T) {
	runErr := errors.New("run failed")
	if got := classifyMaintenanceTick(runErr, context.Canceled); got != maintenanceTickOutcomeShuttingDown {
		t.Fatalf("classifyMaintenanceTick(err, cancelled) = %q, want %q", got, maintenanceTickOutcomeShuttingDown)
	}
	if got := classifyMaintenanceTick(runErr, context.DeadlineExceeded); got != maintenanceTickOutcomeShuttingDown {
		t.Fatalf("classifyMaintenanceTick(err, deadline) = %q, want %q", got, maintenanceTickOutcomeShuttingDown)
	}
}

func TestClassifyMaintenanceTickFailed(t *testing.T) {
	if got := classifyMaintenanceTick(errors.New("run failed"), nil); got != maintenanceTickOutcomeFailed {
		t.Fatalf("classifyMaintenanceTick(err, nil) = %q, want %q", got, maintenanceTickOutcomeFailed)
	}
}

func TestDecodeJSONPreservesNumbers(t *testing.T) {
	var row map[string]any
	if err := decodeJSON([]byte(`{"id":9007199254740993}`), &row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != json.Number("9007199254740993") {
		t.Fatalf("number lost precision: %#v", row)
	}
}

func TestHasDataRevisionRequiresPublishedNamespace(t *testing.T) {
	store, config := nativeTestStore(t)
	if current, err := store.HasDataRevision(t.Context(), "typed"); err != nil || current {
		t.Fatalf("empty namespace matched publication: current=%v err=%v", current, err)
	}
	nativeSeed(t.Context(), t, store, nil)
	for revision, want := range map[string]bool{"typed": true, "different": false, "": false} {
		if current, err := store.HasDataRevision(t.Context(), revision); err != nil || current != want {
			t.Fatalf("publication %q current=%v want=%v err=%v", revision, current, want, err)
		}
	}
	other, err := NewConfig(t.Context(), config, "other")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if current, err := other.HasDataRevision(t.Context(), "typed"); err != nil || current {
		t.Fatalf("publication leaked across namespaces: current=%v err=%v", current, err)
	}
	if _, err := store.db.ExecContext(t.Context(), "UPDATE cao_state SET revision=0 WHERE namespace=$1", store.namespace); err != nil {
		t.Fatal(err)
	}
	if current, err := store.HasDataRevision(t.Context(), "typed"); err != nil || current {
		t.Fatalf("unpublished identity matched: current=%v err=%v", current, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.HasDataRevision(ctx, "typed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("publication identity lookup hid cancellation: %v", err)
	}
}

func TestNamespaceRequired(t *testing.T) {
	if _, err := NewWithNamespace(t.Context(), "postgres://127.0.0.1/postgres?sslmode=disable", ""); err == nil {
		t.Fatal("empty namespace accepted")
	}
}

func TestConnectionTransport(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		valid    bool
	}{
		{"postgres://127.0.0.1/postgres?sslmode=disable", true},
		{"postgres://localhost/postgres?sslmode=disable", true},
		{"postgres://db.example.com/postgres?sslmode=disable", false},
		{"postgres://db.example.com/postgres?sslmode=require", false},
		{"postgres://db.example.com/postgres?sslmode=verify-ca", false},
		{"postgres://db.example.com/postgres?sslmode=verify-full", true},
	} {
		config, err := pgx.ParseConfig(test.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if valid := validateTransport(config) == nil; valid != test.valid {
			t.Errorf("transport accepted=%t want=%t", valid, test.valid)
		}
	}
}

func TestRejectsInsecureDSNWithoutLeakingCredentials(t *testing.T) {
	_, err := New(context.Background(), "postgres://operator:secret@example.com/postgres?sslmode=disable")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe transport failure: %v", err)
	}
}

func TestPartitionMaintenanceRunsOnDailyTicks(t *testing.T) {
	if partitionMaintenanceInterval != 24*time.Hour {
		t.Fatalf("partition maintenance interval = %s", partitionMaintenanceInterval)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time)
	calls := make(chan time.Time, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPartitionMaintenanceLoop(ctx, ticks, func(_ context.Context, at time.Time) error {
			calls <- at
			return nil
		})
	}()
	first := time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{first, first.Add(partitionMaintenanceInterval)} {
		select {
		case ticks <- at:
		case <-time.After(time.Second):
			t.Fatal("maintenance loop did not accept daily tick")
		}
		select {
		case called := <-calls:
			if !called.Equal(at) {
				t.Fatalf("maintenance ran for %s, want %s", called, at)
			}
		case <-time.After(time.Second):
			t.Fatal("maintenance did not run on daily tick")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance loop did not stop")
	}
}
