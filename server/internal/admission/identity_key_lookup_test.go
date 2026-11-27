package admission

import (
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

func TestLookupKeyCachedKeyNeedsNoRefresh(t *testing.T) {
	now := time.Now()
	cached := &rsa.PublicKey{N: nil, E: 65537}
	v := &Verifier{keys: map[string]*rsa.PublicKey{"known": cached}, refreshed: now.Add(-time.Minute)}

	key, refresh, err := v.lookupKey("known", now)
	if key != cached || refresh || err != nil {
		t.Fatalf("lookupKey() = %v, %t, %v; want cached key without refresh", key, refresh, err)
	}
	if !v.lastRefresh.IsZero() || !v.unknownRefresh.IsZero() {
		t.Fatal("a cache hit must not record refresh attempts")
	}
}

func TestLookupKeyThrottles(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		verifier    Verifier
		kid         string
		wantRefresh bool
		wantErr     error
		wantLast    time.Time
		wantUnknown time.Time
	}{
		{
			name:        "stale cache refreshes and records attempt",
			verifier:    Verifier{refreshed: now.Add(-6 * time.Minute)},
			kid:         "any",
			wantRefresh: true,
			wantLast:    now,
		},
		{
			name:        "never refreshed cache refreshes",
			verifier:    Verifier{},
			kid:         "any",
			wantRefresh: true,
			wantLast:    now,
		},
		{
			name:        "first unknown key in fresh cache refreshes and records unknown attempt",
			verifier:    Verifier{refreshed: now.Add(-time.Minute), lastRefresh: now.Add(-2 * time.Second)},
			kid:         "rotated",
			wantRefresh: true,
			wantLast:    now,
			wantUnknown: now,
		},
		{
			name: "recently missing key is denied without refresh",
			verifier: Verifier{
				refreshed: now.Add(-time.Minute), missing: "gone", lastRefresh: now.Add(-10 * time.Second),
			},
			kid:         "gone",
			wantErr:     ErrDenied,
			wantLast:    now.Add(-10 * time.Second),
			wantUnknown: time.Time{},
		},
		{
			name: "missing key is retried after its window",
			verifier: Verifier{
				refreshed: now.Add(-time.Minute), missing: "gone", lastRefresh: now.Add(-31 * time.Second),
			},
			kid:         "gone",
			wantRefresh: true,
			wantLast:    now,
			wantUnknown: now,
		},
		{
			name: "repeated unknown key is throttled as unavailable",
			verifier: Verifier{
				refreshed: now.Add(-time.Minute), unknownRefresh: now.Add(-5 * time.Second),
				lastRefresh: now.Add(-5 * time.Second),
			},
			kid:         "other",
			wantErr:     ErrUnavailable,
			wantLast:    now.Add(-5 * time.Second),
			wantUnknown: now.Add(-5 * time.Second),
		},
		{
			name:        "refresh within one second of the last is unavailable",
			verifier:    Verifier{refreshed: now.Add(-6 * time.Minute), lastRefresh: now.Add(-500 * time.Millisecond)},
			kid:         "any",
			wantErr:     ErrUnavailable,
			wantLast:    now.Add(-500 * time.Millisecond),
			wantUnknown: time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.verifier
			key, refresh, err := v.lookupKey(tt.kid, now)
			if key != nil || refresh != tt.wantRefresh || !errors.Is(err, tt.wantErr) {
				t.Fatalf("lookupKey() = %v, %t, %v; want nil, %t, %v", key, refresh, err, tt.wantRefresh, tt.wantErr)
			}
			if !v.lastRefresh.Equal(tt.wantLast) || !v.unknownRefresh.Equal(tt.wantUnknown) {
				t.Fatalf("recorded lastRefresh=%v unknownRefresh=%v; want %v and %v", v.lastRefresh, v.unknownRefresh, tt.wantLast, tt.wantUnknown)
			}
		})
	}
}
