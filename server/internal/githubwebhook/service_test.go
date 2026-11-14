package githubwebhook

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func record(calls *[]string, name string, outcome Outcome, err error) Listener {
	return ListenerFunc(func(_ context.Context, delivery Delivery) (Outcome, error) {
		*calls = append(*calls, name+":"+delivery.Delivery)
		return outcome, err
	})
}

func TestRegistryRegisterAndUnregister(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Dispatch(t.Context(), Delivery{Delivery: "d"}); !errors.Is(err, ErrNoListeners) {
		t.Fatalf("empty registry dispatched: %v", err)
	}
	var calls []string
	first, err := registry.Register("first", record(&calls, "first", Outcome{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register("first", record(&calls, "other", Outcome{}, nil)); !errors.Is(err, ErrDuplicateListener) {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	for _, invalid := range []struct {
		name     string
		listener Listener
	}{{" ", record(&calls, "x", Outcome{}, nil)}, {"nil", nil}} {
		if _, err := registry.Register(invalid.name, invalid.listener); !errors.Is(err, ErrInvalidListener) {
			t.Fatalf("invalid listener accepted: %v", err)
		}
	}
	if _, err := registry.Register("second", record(&calls, "second", Outcome{}, nil)); err != nil {
		t.Fatal(err)
	}
	if got := registry.Listeners(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("listeners = %v", got)
	}

	first.Unregister()
	// A stale handle must not remove a listener re-registered under its name.
	if _, err := registry.Register("first", record(&calls, "replacement", Outcome{}, nil)); err != nil {
		t.Fatal(err)
	}
	first.Unregister()
	if !registry.Unregister("second") || registry.Unregister("second") {
		t.Fatal("named unregister did not report removal exactly once")
	}
	if _, err := registry.Dispatch(t.Context(), Delivery{Delivery: "d1"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"replacement:d1"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestRegistryDispatchFansOutAndMergesOutcomes(t *testing.T) {
	registry := NewRegistry()
	var calls []string
	_, _ = registry.Register("collector", record(&calls, "collector",
		Outcome{Duplicate: true, Fields: map[string]any{"kind": "run", "duplicate": true}}, nil))
	_, _ = registry.Register("audit", record(&calls, "audit",
		Outcome{Fields: map[string]any{"kind": "ignored", "audited": true}}, nil))

	outcome, err := registry.Dispatch(t.Context(), Delivery{Delivery: "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := Outcome{Fields: map[string]any{"kind": "run", "duplicate": false, "audited": true}}
	if !reflect.DeepEqual(outcome, want) || !reflect.DeepEqual(calls, []string{"collector:d", "audit:d"}) {
		t.Fatalf("outcome = %+v calls = %v", outcome, calls)
	}

	registry.Unregister("audit")
	if outcome, _ := registry.Dispatch(t.Context(), Delivery{Delivery: "d"}); !outcome.Duplicate || outcome.Fields["duplicate"] != true {
		t.Fatalf("sole duplicate listener was not reported as duplicate: %+v", outcome)
	}
}

func TestRegistryDispatchDeliversToAllListenersBeforeFailing(t *testing.T) {
	registry := NewRegistry()
	var calls []string
	failure := errors.New("unavailable")
	_, _ = registry.Register("failing", record(&calls, "failing", Outcome{}, failure))
	_, _ = registry.Register("healthy", record(&calls, "healthy", Outcome{}, nil))

	_, err := registry.Dispatch(t.Context(), Delivery{Delivery: "d"})
	var listenerErr *ListenerError
	if !errors.Is(err, failure) || !errors.As(err, &listenerErr) || listenerErr.Name != "failing" {
		t.Fatalf("listener failure was not attributed: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"failing:d", "healthy:d"}) {
		t.Fatalf("later listener skipped after failure: %v", calls)
	}
}
