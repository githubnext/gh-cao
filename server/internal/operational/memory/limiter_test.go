package memory

import (
	"testing"
	"time"
)

func TestClassifyTokenBucketRequestIdentifiesEachRejection(t *testing.T) {
	cases := []struct {
		name        string
		limit       int
		period      time.Duration
		cost        int
		exists      bool
		bucketCount int
		maxSubjects int
		want        tokenBucketRejection
	}{
		{
			name: "accepted new subject within capacity", limit: 10, period: time.Second, cost: 1,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketAccepted,
		},
		{
			name: "accepted existing subject ignores capacity", limit: 10, period: time.Second, cost: 1,
			exists: true, bucketCount: 10, maxSubjects: 10, want: tokenBucketAccepted,
		},
		{
			name: "rejected non-positive limit", limit: 0, period: time.Second, cost: 1,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketRejectedParameter,
		},
		{
			name: "rejected non-positive cost", limit: 10, period: time.Second, cost: 0,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketRejectedParameter,
		},
		{
			name: "rejected cost over limit", limit: 10, period: time.Second, cost: 11,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketRejectedParameter,
		},
		{
			name: "rejected sub-millisecond period", limit: 10, period: time.Microsecond, cost: 1,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketRejectedParameter,
		},
		{
			name: "rejected oversized period", limit: 10, period: time.Duration(1<<63 - 1), cost: 1,
			exists: false, bucketCount: 0, maxSubjects: 10, want: tokenBucketRejectedParameter,
		},
		{
			name: "rejected new subject at capacity", limit: 10, period: time.Second, cost: 1,
			exists: false, bucketCount: 10, maxSubjects: 10, want: tokenBucketRejectedSubjects,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyTokenBucketRequest(c.limit, c.period, c.cost, c.exists, c.bucketCount, c.maxSubjects)
			if got != c.want {
				t.Fatalf("classifyTokenBucketRequest(%d, %s, %d, %t, %d, %d) = %s, want %s",
					c.limit, c.period, c.cost, c.exists, c.bucketCount, c.maxSubjects, got, c.want)
			}
		})
	}
}

func TestClassifyTokenBucketRequestOversizedLimit(t *testing.T) {
	got := classifyTokenBucketRequest(int(maxQuotaValue)+1, time.Second, 1, false, 0, 10)
	if got != tokenBucketRejectedParameter {
		t.Fatalf("classifyTokenBucketRequest with oversized limit = %s, want %s", got, tokenBucketRejectedParameter)
	}
}

func TestTakeRateLimitTokensClockRegressionDoesNotRefillTwice(t *testing.T) {
	now := time.Unix(1000, 0)
	s, err := New(Config{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	take := func(cost int) bool {
		t.Helper()
		r, err := s.TakeRateLimitTokens(t.Context(), "subject", 10, 10*time.Second, cost)
		if err != nil {
			t.Fatal(err)
		}
		return r.Allowed
	}
	if !take(10) {
		t.Fatal("initial full-bucket spend denied")
	}
	now = now.Add(5 * time.Second)
	if !take(5) {
		t.Fatal("refilled spend denied")
	}
	now = now.Add(-5 * time.Second)
	if take(1) {
		t.Fatal("empty bucket allowed a token after clock regression")
	}
	now = now.Add(5 * time.Second)
	if take(1) {
		t.Fatal("clock regression refilled the same elapsed interval twice")
	}
}
