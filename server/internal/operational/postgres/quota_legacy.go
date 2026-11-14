package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type legacyBudget struct{ remaining, reset, parked int64 }

func readBudget(value []byte) (legacyBudget, bool) {
	parts := strings.Split(string(value), "|")
	var result legacyBudget
	if len(parts) != 3 {
		return result, false
	}
	for i, p := range []*int64{&result.remaining, &result.reset, &result.parked} {
		// ParseInt accepts a leading "+", which the memory and Redis adapters reject.
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil || strings.HasPrefix(parts[i], "+") {
			return legacyBudget{}, false
		}
		*p = n
	}
	return result, true
}

func (t *transaction) saveBudget(k key, b legacyBudget) error {
	value := strconv.FormatInt(b.remaining, 10) + "|" + strconv.FormatInt(b.reset, 10) + "|" + strconv.FormatInt(b.parked, 10)
	return t.put(k, []byte(value), time.Time{})
}

func (s *Store) ReserveRateLimit(ctx context.Context, name, field string, floor, cost int, now int64) (int, error) {
	if err := names(name, field); err != nil {
		return 0, err
	}
	if floor < 0 || cost <= 0 || int64(cost) > maxQuotaValue || now <= 0 {
		return 0, invalid("invalid legacy reservation")
	}
	result := 3
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"attribute", name, field}
		value, err := t.get(k)
		if err != nil {
			return err
		}
		b, known := readBudget(value)
		if !known {
			return nil
		}
		switch {
		case b.parked > now:
			result = 1
		case b.reset > 0 && b.reset < now:
			result = 3
		case b.remaining <= int64(floor):
			result = 2
		default:
			result = 0
			b.remaining = max(b.remaining-int64(cost), 0)
			return t.saveBudget(k, b)
		}
		return nil
	})
	return result, err
}

func (s *Store) ObserveRateLimit(ctx context.Context, name, field string, remaining int, reset int64) error {
	if err := names(name, field); err != nil {
		return err
	}
	if remaining < 0 || int64(remaining) > maxQuotaValue || reset < 0 {
		return invalid("invalid legacy observation")
	}
	return s.transact(ctx, func(t *transaction) error {
		k := key{"attribute", name, field}
		value, err := t.get(k)
		if err != nil {
			return err
		}
		b, known := readBudget(value)
		if known && b.reset == reset {
			b.remaining = min(b.remaining, int64(remaining))
		} else {
			b.remaining = int64(remaining)
		}
		b.reset = reset
		return t.saveBudget(k, b)
	})
}

func (s *Store) ParkRateLimit(ctx context.Context, name, field string, until int64) error {
	if err := names(name, field); err != nil {
		return err
	}
	if until < 0 {
		return invalid("invalid legacy parking time")
	}
	return s.transact(ctx, func(t *transaction) error {
		k := key{"attribute", name, field}
		value, err := t.get(k)
		if err != nil {
			return err
		}
		b, _ := readBudget(value)
		b.parked = max(b.parked, until)
		return t.saveBudget(k, b)
	})
}
