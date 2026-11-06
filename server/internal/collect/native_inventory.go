package collect

import (
	"context"
	"errors"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

// NativeInventoryService owns the production, file-free inventory refresh.
// Snapshot acquisition happens explicitly at composition using immutable,
// deployment-reviewed bytes; replicas pin the same digest in PostgreSQL.
type NativeInventoryService struct {
	snapshot collectnative.InventorySnapshot
	provider collectnative.InventoryProvider
	store    collectnative.InventoryStore
}

func (s *NativeInventoryService) ControlRepository() string {
	return s.snapshot.ControlRepository()
}

func NewNativeInventoryService(ctx context.Context, snapshot collectnative.InventorySnapshot,
	provider collectnative.InventoryProvider, store collectnative.InventoryStore,
) (*NativeInventoryService, error) {
	if provider == nil || store == nil {
		return nil, errors.New("native inventory requires provider and durable publication")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if err := store.PinInventoryConfiguration(ctx, snapshot); err != nil {
		return nil, err
	}
	return &NativeInventoryService{snapshot: snapshot, provider: provider, store: store}, nil
}

func (s *NativeInventoryService) RefreshInventory(ctx context.Context, scope collectnative.EnrollmentScope) error {
	claim, err := s.store.BeginInventoryRefresh(ctx, scope, s.snapshot.Digest)
	if err != nil {
		return err
	}
	evidence, err := s.provider.BuildInventory(ctx, scope, s.snapshot)
	if err != nil {
		// Generation remains pending in durable storage for another replica.
		return err
	}
	return s.store.PublishInventory(ctx, claim, evidence)
}

func (s *NativeInventoryService) EnsureReady(ctx context.Context, scope collectnative.EnrollmentScope) error {
	return s.store.EnsureInventoryReady(ctx, scope, s.snapshot.Digest)
}

// RetryPending is a bounded runtime tick, not repository discovery. It retries
// only already-requested refreshes still inside current committed enrollment.
func (s *NativeInventoryService) RetryPending(ctx context.Context, limit int) (int, error) {
	scopes, err := s.store.PendingInventoryScopes(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	var failures []error
	for _, scope := range scopes {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := s.RefreshInventory(ctx, scope); err != nil {
			failures = append(failures, err)
		} else {
			done++
		}
	}
	return done, errors.Join(failures...)
}
