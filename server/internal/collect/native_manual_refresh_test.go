package collect

import (
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeManualInventoryRefreshRereadsWithoutHourlyDeduplication(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	worker := nativeEventWorker(queue, enrollment, hooks)
	for range 2 {
		count, err := queue.RequestInventoryRefresh(t.Context(), enrollment)
		if err != nil || count != 1 {
			t.Fatalf("manual admission: count=%d err=%v", count, err)
		}
		if more, err := worker.Step(t.Context()); err != nil || !more {
			t.Fatalf("manual inventory processing: more=%v err=%v", more, err)
		}
	}
	if hooks.inventories != 2 || hooks.values != 0 || hooks.outcomes != 0 {
		t.Fatalf("manual refresh skipped inventory or expanded lanes: %+v", hooks)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), runTaskStream, ""); err != nil || stats.Length != 0 {
		t.Fatalf("inventory refresh unexpectedly admitted history: %+v %v", stats, err)
	}
}

func TestNativeInventoryRepairCadenceIsAtLeastDaily(t *testing.T) {
	for _, interval := range []time.Duration{0, time.Minute, time.Hour, 48 * time.Hour} {
		actual := (NativeWorker{RefreshInterval: interval}).interval()
		if actual <= 0 || actual > 24*time.Hour {
			t.Fatalf("interval %v resolved to %v", interval, actual)
		}
	}
	if _, err := (Queue{}).RequestInventoryRefresh(t.Context(), Enrollment{}); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("missing authority did not fail closed: %v", err)
	}
}
