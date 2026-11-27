package postgres

import (
	"context"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (s *Store) TryLock(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	if err := names(name, owner); err != nil {
		return false, err
	}
	if err := ttlValid(ttl); err != nil {
		return false, err
	}
	acquired := false
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"lock", name, ""}
		value, err := t.get(k)
		if err != nil || value != nil {
			return err
		}
		if err := t.put(k, []byte(owner), t.now.Add(ttl)); err != nil {
			return err
		}
		acquired = true
		return nil
	})
	return acquired && err == nil, err
}

func (s *Store) RenewLock(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	if err := names(name, owner); err != nil {
		return false, err
	}
	if err := ttlValid(ttl); err != nil {
		return false, err
	}
	renewed := false
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"lock", name, ""}
		value, err := t.get(k)
		if err != nil || value == nil || string(value) != owner {
			return err
		}
		if err := t.put(k, value, t.now.Add(ttl)); err != nil {
			return err
		}
		renewed = true
		return nil
	})
	return renewed && err == nil, err
}

func (s *Store) Unlock(ctx context.Context, name, owner string) error {
	if err := names(name, owner); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		k := key{"lock", name, ""}
		value, err := t.get(k)
		if err != nil || string(value) != owner {
			return err
		}
		return t.remove(k)
	})
}

func (s *Store) LockHeld(ctx context.Context, name string) (bool, error) {
	if err := names(name); err != nil {
		return false, err
	}
	var held bool
	err := s.transact(ctx, func(t *transaction) error {
		value, err := t.get(key{"lock", name, ""})
		held = value != nil
		return err
	})
	return held && err == nil, err
}

func (s *Store) SetOperationalState(ctx context.Context, name string, value []byte) error {
	if err := names(name); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		return t.put(key{"state", name, ""}, value, time.Time{})
	})
}

func (s *Store) OperationalState(ctx context.Context, name string) ([]byte, error) {
	if err := names(name); err != nil {
		return nil, err
	}
	var value []byte
	err := s.transact(ctx, func(t *transaction) error {
		var err error
		value, err = t.get(key{"state", name, ""})
		return err
	})
	return value, err
}

// SetExpiringOperationalState retains a protected receipt without accumulating
// permanent per-delivery state. Expiry never permits replay of an unexpired job.
func (s *Store) SetExpiringOperationalState(ctx context.Context, name string, value []byte, ttl time.Duration) error {
	if err := names(name); err != nil {
		return err
	}
	if err := ttlValid(ttl); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		return t.put(key{"state", name, ""}, value, t.now.Add(ttl))
	})
}

func (s *Store) RememberDelivery(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	if err := names(id); err != nil {
		return false, err
	}
	if err := ttlValid(ttl); err != nil {
		return false, err
	}
	fresh := false
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"delivery", id, ""}
		value, err := t.get(k)
		if err != nil || value != nil {
			return err
		}
		if err := t.put(k, []byte{1}, t.now.Add(ttl)); err != nil {
			return err
		}
		fresh = true
		return nil
	})
	return fresh && err == nil, err
}

func (s *Store) ForgetDelivery(ctx context.Context, id string) error {
	return s.removeNamed(ctx, key{"delivery", id, ""})
}

func (s *Store) removeNamed(ctx context.Context, k key) error {
	if err := names(k.name); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error { return t.remove(k) })
}

func (s *Store) ReserveDelivery(ctx context.Context, id string, ttl time.Duration) (operational.DeliveryReservation, error) {
	if err := names(id); err != nil {
		return 0, err
	}
	if err := ttlValid(ttl); err != nil {
		return 0, err
	}
	result := operational.DeliveryAlreadyCommitted
	err := s.transact(ctx, func(t *transaction) error {
		value, err := t.get(key{"delivery", id, ""})
		if err != nil || value != nil {
			return err
		}
		k := key{"delivery-reservation", id, ""}
		value, err = t.get(k)
		if err != nil {
			return err
		}
		if value != nil {
			result = operational.DeliveryInProgress
			return nil
		}
		if err := t.put(k, []byte{1}, t.now.Add(ttl)); err != nil {
			return err
		}
		result = operational.DeliveryReserved
		return nil
	})
	return result, err
}

func (s *Store) ReleaseDeliveryReservation(ctx context.Context, id string) error {
	return s.removeNamed(ctx, key{"delivery-reservation", id, ""})
}
