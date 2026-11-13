package collect

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestReporterRequiresPostgres(t *testing.T) {
	if _, err := (Reporter{}).Snapshot(context.Background()); err == nil {
		t.Fatal("collection status without Postgres must fail closed")
	}
}

type recoveryDeliveryPage struct {
	deliveries []githubapp.Delivery
	next       string
}

type recoveryDeliveryClient struct {
	pages       map[string]recoveryDeliveryPage
	cursors     []string
	failID      int64
	redelivered []int64
}

func (c *recoveryDeliveryClient) ListDeliveries(
	_ context.Context, cursor string, _ int,
) ([]githubapp.Delivery, string, error) {
	c.cursors = append(c.cursors, cursor)
	page, ok := c.pages[cursor]
	if !ok {
		return nil, "", errors.New("unexpected recovery cursor")
	}
	return page.deliveries, page.next, nil
}

func (c *recoveryDeliveryClient) Redeliver(_ context.Context, id int64) error {
	if id == c.failID {
		return errors.New("injected redelivery failure")
	}
	c.redelivered = append(c.redelivered, id)
	return nil
}

func newRecoveryStore() *memory.Store {
	store, err := memory.New(memory.Config{})
	if err != nil {
		panic(err)
	}
	return store
}

func TestPlanRedeliveriesSelectsOnlyNonSuccessStatuses(t *testing.T) {
	deliveries := []githubapp.Delivery{
		{ID: 3, GUID: "guid-3", StatusCode: 200},
		{ID: 2, GUID: "guid-2", StatusCode: 500},
		{ID: 1, GUID: "guid-1", StatusCode: 422},
	}
	plan := planRedeliveries(deliveries, "")
	if plan.inspected != 3 {
		t.Fatalf("inspected = %d, want 3", plan.inspected)
	}
	if want := []int64{2, 1}; !reflect.DeepEqual(plan.toRedeliver, want) {
		t.Fatalf("toRedeliver = %v, want %v", plan.toRedeliver, want)
	}
	if plan.newest != "guid-3" {
		t.Fatalf("newest = %q, want %q", plan.newest, "guid-3")
	}
}

func TestPlanRedeliveriesStopsAtBoundary(t *testing.T) {
	deliveries := []githubapp.Delivery{
		{ID: 3, GUID: "guid-3", StatusCode: 500},
		{ID: 2, GUID: "guid-2", StatusCode: 500},
		{ID: 1, GUID: "guid-1", StatusCode: 500},
	}
	plan := planRedeliveries(deliveries, "guid-2")
	if plan.inspected != 1 {
		t.Fatalf("inspected = %d, want 1 (only guid-3 precedes the boundary)", plan.inspected)
	}
	if want := []int64{3}; !reflect.DeepEqual(plan.toRedeliver, want) {
		t.Fatalf("toRedeliver = %v, want %v", plan.toRedeliver, want)
	}
	if plan.newest != "guid-3" {
		t.Fatalf("newest = %q, want %q", plan.newest, "guid-3")
	}
}

func TestPlanRedeliveriesHandlesEmptyPage(t *testing.T) {
	plan := planRedeliveries(nil, "guid-1")
	if plan.inspected != 0 {
		t.Fatalf("inspected = %d, want 0", plan.inspected)
	}
	if plan.toRedeliver != nil {
		t.Fatalf("toRedeliver = %v, want nil", plan.toRedeliver)
	}
	if plan.newest != "" {
		t.Fatalf("newest = %q, want empty", plan.newest)
	}
}

func TestPlanRedeliveriesBoundaryAsFirstEntryInspectsNothing(t *testing.T) {
	deliveries := []githubapp.Delivery{
		{ID: 1, GUID: "guid-1", StatusCode: 500},
	}
	plan := planRedeliveries(deliveries, "guid-1")
	if plan.inspected != 0 {
		t.Fatalf("inspected = %d, want 0", plan.inspected)
	}
	if plan.toRedeliver != nil {
		t.Fatalf("toRedeliver = %v, want nil", plan.toRedeliver)
	}
	// newest is still recorded even though the boundary stopped iteration
	// immediately, so the cursor does not regress on a page with no new
	// deliveries.
	if plan.newest != "guid-1" {
		t.Fatalf("newest = %q, want %q", plan.newest, "guid-1")
	}
	if !plan.foundBoundary {
		t.Fatal("boundary was not marked as found")
	}
}

func TestDeliveryRecoveryResumesPaginationBeforeAdvancingCursor(t *testing.T) {
	store := newRecoveryStore()
	if err := store.SetOperationalState(t.Context(), deliveryCursorKey, []byte("guid-1")); err != nil {
		t.Fatal(err)
	}
	client := &recoveryDeliveryClient{pages: map[string]recoveryDeliveryPage{
		"": {
			deliveries: []githubapp.Delivery{
				{ID: 3, GUID: "guid-3", StatusCode: 500},
				{ID: 2, GUID: "guid-2", StatusCode: 200},
			},
			next: "page-2",
		},
		"page-2": {deliveries: []githubapp.Delivery{
			{ID: 1, GUID: "guid-1", StatusCode: 500},
		}},
	}}
	replayer := DeliveryReplayer{State: store, Metadata: store, Client: client, Enabled: true, Limit: 2}

	first, err := replayer.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Inspected != 2 || first.Redelivered != 1 {
		t.Fatalf("first pass = %#v, want two inspected and one redelivery", first)
	}
	progress, err := store.OperationalState(t.Context(), deliveryProgressKey)
	if err != nil {
		t.Fatal(err)
	}
	var saved deliveryProgress
	if err := json.Unmarshal(progress, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Cursor != "page-2" || saved.Boundary != "guid-1" || saved.Newest != "guid-3" {
		t.Fatalf("saved progress = %#v", saved)
	}

	second, err := replayer.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second.Inspected != 0 || second.Redelivered != 0 {
		t.Fatalf("second pass = %#v, boundary entry should not be inspected", second)
	}
	lastSeen, err := store.OperationalState(t.Context(), deliveryCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(lastSeen) != "guid-3" {
		t.Fatalf("delivery cursor = %q, want guid-3", lastSeen)
	}
	progress, err = store.OperationalState(t.Context(), deliveryProgressKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 0 {
		t.Fatalf("recovery progress was not cleared: %s", progress)
	}
	if !reflect.DeepEqual(client.cursors, []string{"", "page-2"}) {
		t.Fatalf("requested cursors = %v", client.cursors)
	}
}

func TestDeliveryRecoveryDoesNotAdvanceAfterRedeliveryFailure(t *testing.T) {
	store := newRecoveryStore()
	if err := store.SetOperationalState(t.Context(), deliveryCursorKey, []byte("old-guid")); err != nil {
		t.Fatal(err)
	}
	client := &recoveryDeliveryClient{
		pages: map[string]recoveryDeliveryPage{"": {
			deliveries: []githubapp.Delivery{{ID: 2, GUID: "new-guid", StatusCode: 500}},
		}},
		failID: 2,
	}
	replayer := DeliveryReplayer{State: store, Metadata: store, Client: client, Enabled: true}
	if _, err := replayer.Recover(t.Context()); err == nil {
		t.Fatal("expected redelivery failure")
	}
	lastSeen, err := store.OperationalState(t.Context(), deliveryCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(lastSeen) != "old-guid" {
		t.Fatalf("delivery cursor advanced to %q despite failure", lastSeen)
	}
}

func TestIngestionHealthStateExplainsDegradationAndRecovery(t *testing.T) {
	cases := []struct {
		name   string
		status Status
		events map[string]string
		want   string
	}{
		{name: "idle", status: Status{}, want: "healthy"},
		{name: "backlog", status: Status{QueueDepth: 2}, want: "recovering"},
		{name: "in-flight", status: Status{PendingTasks: 1}, want: "recovering"},
		{
			name: "failure newer than success",
			events: map[string]string{
				"lastFailureAt": "2026-09-29T02:00:00Z",
				"lastSuccessAt": "2026-09-29T01:00:00Z",
			},
			want: "degraded",
		},
		{
			name:   "success after failure with backlog",
			status: Status{QueueDepth: 2},
			events: map[string]string{
				"lastFailureAt": "2026-09-29T01:00:00Z",
				"lastSuccessAt": "2026-09-29T02:00:00Z",
			},
			want: "recovering",
		},
		{name: "dead letter", status: Status{DeadLetters: 1}, want: "degraded"},
		{
			name:   "invalid persisted timestamp",
			events: map[string]string{"lastFailureAt": "not-a-time"},
			want:   "degraded",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := ingestionHealthState(test.status, test.events); got != test.want {
				t.Fatalf("ingestionHealthState() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseHealthTimeRejectsMissingAndInvalidValues(t *testing.T) {
	if _, ok := parseHealthTime(""); ok {
		t.Fatal("empty timestamp was accepted")
	}
	if _, ok := parseHealthTime(time.Now().Format(time.RFC822)); ok {
		t.Fatal("non-RFC3339 timestamp was accepted")
	}
}
