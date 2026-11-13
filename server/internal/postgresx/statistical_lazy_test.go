package postgresx

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatisticalRefreshConfiguration(t *testing.T) {
	t.Setenv("CAO_STATISTICAL_REFRESH_INTERVAL", "15m")
	if interval, err := configuredStatisticalRefreshInterval(); err != nil || interval != 15*time.Minute {
		t.Fatalf("configured refresh interval: %v %v", interval, err)
	}
	for _, value := range []string{"", "0", "-1m", "NaN", "1ns"} {
		t.Setenv("CAO_STATISTICAL_REFRESH_INTERVAL", value)
		if _, err := configuredStatisticalRefreshInterval(); err == nil {
			t.Fatalf("invalid interval accepted: %q", value)
		}
	}
}

func TestStatisticalRefreshThrottle(t *testing.T) {
	for _, interval := range []time.Duration{time.Minute, 15 * time.Minute} {
		t.Run(strconv.FormatInt(interval.Milliseconds(), 10), func(t *testing.T) {
			now := time.Now()
			cache := statisticalCache{interval: interval, now: func() time.Time { return now }}
			builds := 0
			build := func() (*statisticalSnapshot, error) {
				builds++
				return &statisticalSnapshot{revision: int64(builds)}, nil
			}
			first, err := cache.get(t.Context(), 1, "scope", build)
			if err != nil || first == nil || builds != 1 {
				t.Fatalf("first demand failed: %v", err)
			}
			for revision := int64(2); revision <= 5; revision++ {
				value, err := cache.get(t.Context(), revision, "scope", build)
				if err != nil || value != first || builds != 1 {
					t.Fatal("canonical revision bypassed throttle")
				}
			}
			now = now.Add(interval - time.Nanosecond)
			_, _ = cache.get(t.Context(), 2, "scope", build)
			if builds != 1 {
				t.Fatal("rebuilt before interval elapsed")
			}
			now = now.Add(time.Nanosecond)
			second, err := cache.get(t.Context(), 2, "scope", build)
			if err != nil || second == first || builds != 2 {
				t.Fatal("eligible demand did not refresh")
			}
			now = now.Add(interval)
			_, _ = cache.get(t.Context(), 2, "scope", build)
			if builds != 2 {
				t.Fatal("unchanged revision was recomputed")
			}
		})
	}
}

func TestStatisticalFailedRefreshAndWithdrawalAreThrottled(t *testing.T) {
	now := time.Now()
	cache := statisticalCache{now: func() time.Time { return now }}
	first, err := cache.get(t.Context(), 1, "scope", func() (*statisticalSnapshot, error) {
		return &statisticalSnapshot{revision: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(defaultStatisticalRefreshInterval)
	failure := errors.New("sampling failed")
	if _, err := cache.get(t.Context(), 2, "scope", func() (*statisticalSnapshot, error) {
		return nil, failure
	}); !errors.Is(err, failure) {
		t.Fatal("build failure was hidden")
	}
	unexpectedBuild := func() (*statisticalSnapshot, error) {
		t.Error("throttled request started a build")
		return nil, failure
	}
	stale, err := cache.get(t.Context(), 2, "scope", unexpectedBuild)
	if err != nil || stale == first || stale.revision != 1 || !stale.refreshError {
		t.Fatalf("failed refresh lost stale provenance: %+v %v", stale, err)
	}
	withdrawn, err := cache.get(t.Context(), 3, "new-scope", unexpectedBuild)
	if err != nil || withdrawn != nil {
		t.Fatal("withdrawal served a previously authorized sample")
	}
	// Clearing result storage must not clear the attempt fence.
	_, _ = cache.get(t.Context(), 3, "new-scope", unexpectedBuild)
}

func TestStatisticalRefreshSingleFlightAndCancellation(t *testing.T) {
	cache := statisticalCache{}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var workers sync.WaitGroup
	build := func() (*statisticalSnapshot, error) {
		if calls.Add(1) != 1 {
			t.Error("duplicate concurrent build")
		}
		close(started)
		select {
		case <-release:
			return &statisticalSnapshot{revision: 1}, nil
		case <-t.Context().Done():
			return nil, t.Context().Err()
		}
	}
	for range 8 {
		workers.Go(func() {
			value, err := cache.get(t.Context(), 1, "scope", build)
			if err != nil || value == nil {
				t.Errorf("shared result failed: %v", err)
			}
		})
	}
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.get(ctx, 1, "scope", build); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled waiter ignored cancellation")
	}
	close(release)
	workers.Wait()
}
