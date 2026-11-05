package sessionarchive

import "context"

// Source is the enrollment identity frozen when an exact attempt is dispatched.
// Admission must not substitute a newer Current source for this identity.
type Source struct {
	Repository     string
	InstallationID int64
	Epoch          int64
}

func (s Source) Validate() error {
	if !repositoryPattern.MatchString(s.Repository) || s.InstallationID <= 0 || s.Epoch <= 0 {
		return ErrIntegrity
	}
	return nil
}

// NativeAuthority owns metadata-only archive enrollment visibility and durable
// cleanup. It never stores approved content, Blob manifests or archive catalogs.
//
// The adapter is scoped to the source enrollment namespace; destination is the
// exact native Blob acceptance binding. Registration rows are permanent bounded
// per-source privacy fences. In the SAME source enrollment transaction, every
// old-epoch registration must become withdrawn with cleanup_pending=true.
// Registration capacity exhaustion must reject admission, not drop fences.
type NativeAuthority interface {
	// Register commits an idempotent destination/source registration while
	// fencing the exact current enrolled installation and epoch. Its commit
	// MUST precede any Blob write. A rolled-back registration is not success.
	Register(context.Context, string, Source) error

	// Current returns only a registered, nonwithdrawn source whose installation
	// and epoch still match authoritative current enrollment. No implicit
	// registration or enrollment is allowed. Unavailable authority fails closed.
	Current(context.Context, string, string) (Source, error)

	// WithRead checks the exact registered source and holds shared source and
	// registration row locks THROUGH action and its bounded response delivery.
	// Enrollment withdrawal takes conflicting locks in the same order. No read
	// callback may start under a withdrawn/replaced epoch. The adapter must
	// cancel the callback context if its transaction/connection is lost.
	WithRead(context.Context, string, Source, func(context.Context) error) error

	// DrainWithdrawals visits at most limit durably pending withdrawn sources,
	// retaining each row until its idempotent Blob cleanup succeeds AND the
	// acknowledgement commits. Concurrent consumers must use database row
	// claims/locks, not process locks; failures remain retryable after restart.
	// Callback execution must not hold the current source enrollment row lock.
	DrainWithdrawals(context.Context, string, int, func(context.Context, Source) error) (int, error)
}
