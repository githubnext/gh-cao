package postgres

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"time"
)

var counterNames = map[string]bool{
	"webhookReceived": true, "webhookDuplicate": true, "webhookAdmissionFailed": true,
	"taskQueued": true, "taskCoalesced": true, "collectionSucceeded": true,
	"collectionFailed": true, "collectionRetried": true, "collectionDeadLettered": true,
	"adxExported": true, "adxFailure": true, "adxGap": true,
}
var loadPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type load struct {
	Amount float64
	At     time.Time
}

func decayed(l load, now time.Time, halfLife time.Duration) float64 {
	if l.At.IsZero() || !l.At.Add(8*time.Minute).After(now) {
		return 0
	}
	return l.Amount * math.Pow(0.5, math.Max(0, now.Sub(l.At).Seconds())/halfLife.Seconds())
}

func (t *transaction) increment(field string) error {
	k := key{"diagnostic", "counters", field}
	value, err := t.get(k)
	if err != nil {
		return err
	}
	var n int64
	if value != nil {
		n, err = strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return err
		}
	}
	if n == math.MaxInt64 {
		return invalid("counter overflow")
	}
	return t.put(k, []byte(strconv.FormatInt(n+1, 10)), time.Time{})
}

func (s *Store) IncrementIngestionCounter(ctx context.Context, name string) error {
	if !counterNames[name] {
		return invalid("unknown ingestion counter")
	}
	return s.transact(ctx, func(t *transaction) error {
		if err := t.increment(name); err != nil {
			return err
		}
		if err := t.increment("healthRevision"); err != nil {
			return err
		}
		category := map[string]string{"webhookReceived": "webhook", "collectionSucceeded": "collection", "collectionFailed": "failure"}[name]
		if category != "" {
			k := key{"load", category, ""}
			var l load
			if _, err := t.json(k, &l); err != nil {
				return err
			}
			l = load{Amount: decayed(l, t.now, time.Minute) + 1, At: t.now}
			return t.putJSON(k, l, t.now.Add(8*time.Minute))
		}
		return nil
	})
}

func (s *Store) RecordIngestionHealthEvent(ctx context.Context, event, code string, at time.Time) error {
	field := map[string]string{"failure": "lastFailureAt", "success": "lastSuccessAt", "webhook": "lastWebhookAt"}[event]
	if field == "" || at.IsZero() || (event != "failure" && code != "") ||
		(event == "failure" && code != "admission" && code != "collection" && code != "redis") {
		return invalid("invalid ingestion event, code, or time")
	}
	return s.transact(ctx, func(t *transaction) error {
		if err := t.put(key{"diagnostic", "events", field}, []byte(at.UTC().Format(time.RFC3339Nano)), time.Time{}); err != nil {
			return err
		}
		if event == "failure" {
			if err := t.put(key{"diagnostic", "events", "lastFailureCode"}, []byte(code), time.Time{}); err != nil {
				return err
			}
		}
		return t.increment("healthRevision")
	})
}

func (s *Store) IngestionHealth(ctx context.Context) (map[string]int64, map[string]string, error) {
	counters, events := map[string]int64{}, map[string]string{}
	err := s.transact(ctx, func(t *transaction) error {
		records, err := t.list("diagnostic", "")
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.name == "events" || r.field == "healthRevision" {
				events[r.field] = string(r.value)
			} else {
				n, err := strconv.ParseInt(string(r.value), 10, 64)
				if err != nil {
					return err
				}
				counters[r.field] = n
			}
		}
		return nil
	})
	return counters, events, err
}

func (s *Store) Loads(ctx context.Context, names []string, halfLife time.Duration) (map[string]float64, error) {
	if len(names) == 0 || len(names) > 16 || halfLife < time.Second || halfLife > 24*time.Hour {
		return nil, invalid("invalid load names or half-life")
	}
	for _, name := range names {
		if !loadPattern.MatchString(name) {
			return nil, invalid("invalid load name")
		}
	}
	result := map[string]float64{}
	err := s.transact(ctx, func(t *transaction) error {
		for _, name := range names {
			var l load
			if _, err := t.json(key{"load", name, ""}, &l); err != nil {
				return err
			}
			result[name] = decayed(l, t.now, halfLife)
		}
		return nil
	})
	return result, err
}
