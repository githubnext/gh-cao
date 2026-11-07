package collectnative

import "time"

// ValueHint is durable invalidation state, never execution or acceptance
// authority. A consumer must independently resolve its reviewed Value policy.
type ValueHint struct {
	Dirty      int64
	Processed  int64
	AttemptDay string
	RetryAt    time.Time
}
