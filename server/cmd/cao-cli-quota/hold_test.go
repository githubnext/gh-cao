package main

import (
	"context"
	"testing"
)

func TestHoldRecordsReservationUnderFirstBucketID(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	if err := b.observeSnapshot(ctx, *current, []string{"core", "graphql"}); err != nil {
		t.Fatal(err)
	}

	id, err := b.hold(ctx, current.identity, []string{"core", "graphql"}, 50, 100)
	if err != nil || id == "" {
		t.Fatalf("hold = %q, %v", id, err)
	}
	held := b.reservations[id]
	if held == nil || held.finished || len(held.buckets) != 2 || held.buckets[0].ID != id {
		t.Fatalf("reservation not recorded under first bucket ID: %+v", held)
	}
	for _, resource := range []string{"core", "graphql"} {
		state, err := b.service.State(ctx, bucket(current.identity, resource))
		if err != nil || state.Reserved != 50 {
			t.Fatalf("%s not reserved: %+v, %v", resource, state, err)
		}
	}
}

func TestHoldRecordsNothingWhenAnyBucketCannotBeReserved(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	if err := b.observeSnapshot(ctx, *current, []string{"core"}); err != nil {
		t.Fatal(err)
	}

	id, err := b.hold(ctx, current.identity, []string{"core", "graphql"}, 50, 100)
	if err == nil || id != "" {
		t.Fatalf("hold = %q, %v, want failure", id, err)
	}
	if len(b.reservations) != 0 {
		t.Fatalf("failed hold recorded reservations: %d", len(b.reservations))
	}
	state, err := b.service.State(ctx, bucket(current.identity, "core"))
	if err != nil || state.Reserved != 0 {
		t.Fatalf("core reservation leaked after failed hold: %+v, %v", state, err)
	}
}
