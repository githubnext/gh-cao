package adxingest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

var exportLog = logger.New("cao:adx")

type Status struct {
	Degraded bool
	Stage    string
	Gaps     int64
}

type deliveryState struct {
	Version           int
	CompletedRevision int64
	ScanRevision      int64
	After             string
	Pending           *Projection
	PendingSince      time.Time
	GapCount          int64
	LastGapRunID      string
	LastGapRevision   int64
}

// Worker serializes one bounded pending export in protected operational state.
// V1 requires one confirmed replica; the lease also protects restart overlap.
type Worker struct {
	config   Config
	reader   sessionexport.Reader
	sink     Sink
	services operational.OperationalServices
	key      string
	owner    string
	mu       sync.Mutex
	status   Status
}

func NewWorker(config Config, reader sessionexport.Reader, sink Sink, services operational.OperationalServices) (*Worker, error) {
	if !config.Enabled || reader == nil || sink == nil || services.State == nil ||
		services.Leases == nil || services.Deliveries == nil || services.IngestionMetrics == nil {
		return nil, errors.New("ADX worker requires an enabled config, reader, sink and durable operational services")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	key := hash([]byte(config.Endpoint + "\x00" + config.Database + "\x00" +
		strings.Join(config.RepositoryIDs, "\x00") + fmt.Sprintf("\x00%d", contractVersion)))
	return &Worker{config: config, reader: reader, sink: sink, services: services,
		key: "adx:" + key, owner: rand.Text(), status: Status{Stage: "waiting"}}, nil
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *Worker) setStatus(stage string, degraded bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Stage, w.status.Degraded = stage, degraded || w.status.Gaps > 0
}

func (w *Worker) Run(ctx context.Context) {
	for {
		w.setStatus("exporting", false)
		// Bound a pass independently from core collection. Catch-up work is
		// serialized and capped; a failing sink is retried only next interval.
		for i := 0; i < 100 && ctx.Err() == nil; i++ {
			stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			more, err := w.Step(stepCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					reason := "retrying"
					switch {
					case errors.Is(err, ErrStorage):
						reason = "capacity"
					case errors.Is(err, ErrVisibility):
						reason = "visibility"
					}
					w.setStatus(reason, true)
					slog.WarnContext(ctx, "ADX export deferred", "reason", reason)
					w.counter(ctx, "adxFailure")
				}
				break
			}
			if !more {
				w.setStatus("waiting", false)
				break
			}
		}
		timer := time.NewTimer(w.config.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) Step(ctx context.Context) (more bool, err error) {
	// The deadline is shorter than the lease and applies even to direct users
	// of Step (simulator tests and future bounded administration commands).
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	locked, err := w.services.Leases.TryLock(ctx, w.key, w.owner, 2*time.Minute)
	if err != nil || !locked {
		return false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if unlockErr := w.services.Leases.Unlock(cleanup, w.key, w.owner); unlockErr != nil {
			slog.WarnContext(cleanup, "ADX lease release failed")
			err = errors.Join(err, unlockErr)
		}
	}()
	state := deliveryState{Version: contractVersion}
	content, err := w.services.State.OperationalState(ctx, w.key)
	if err != nil {
		return false, err
	}
	if len(content) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&state); err != nil || state.Version != contractVersion {
			return false, errors.New("ADX delivery state is invalid or incompatible")
		}
	}
	w.mu.Lock()
	w.status.Gaps = state.GapCount
	w.mu.Unlock()
	if state.Pending == nil {
		fields := map[string][]string{}
		for _, family := range families {
			for _, field := range family.fields {
				fields[family.source] = append(fields[family.source], field.source)
			}
		}
		session, err := w.reader.ReadSession(ctx, sessionexport.Request{
			RepositoryIDs: w.config.RepositoryIDs, After: state.After, Fields: fields, CompletedRevision: state.CompletedRevision,
		})
		if state.After == "" && session.Revision == state.CompletedRevision &&
			(err == nil || errors.Is(err, sessionexport.ErrTooLarge)) {
			return false, nil
		}
		if errors.Is(err, sessionexport.ErrTooLarge) {
			id, _ := session.Run["id"].(string)
			if id == "" {
				return false, err
			}
			state.GapCount++
			if state.After == "" {
				state.ScanRevision = session.Revision
			}
			state.LastGapRunID, state.LastGapRevision = id, session.Revision
			state.After = id
			w.counter(ctx, "adxGap")
			slog.WarnContext(ctx, "ADX export gap", "reason", "session-budget")
			return true, w.save(ctx, state)
		}
		if err != nil {
			return false, err
		}
		if state.After == "" {
			if state.CompletedRevision == session.Revision {
				return false, nil
			}
			state.ScanRevision = session.Revision
		}
		if len(session.Run) == 0 {
			state.After = ""
			state.CompletedRevision = state.ScanRevision
			return false, w.save(ctx, state)
		}
		repositoryID, _ := session.Run["repositoryId"].(string)
		if !slices.Contains(w.config.RepositoryIDs, repositoryID) {
			return false, errors.New("ADX reader returned an out-of-scope repository")
		}
		projection, err := project(session)
		if errors.Is(err, sessionexport.ErrTooLarge) {
			state.GapCount++
			state.After = session.Run["id"].(string)
			state.LastGapRunID, state.LastGapRevision = state.After, session.Revision
			w.counter(ctx, "adxGap")
			slog.WarnContext(ctx, "ADX export gap", "reason", "projection-budget")
			return true, w.save(ctx, state)
		}
		if err != nil {
			return false, err
		}
		state.Pending = &projection
		state.PendingSince = time.Now().UTC()
		// No destination call occurs until this immutable approved payload
		// survives a crash independently of canonical replacement/retention.
		if err := w.save(ctx, state); err != nil {
			return false, err
		}
	}
	if !slices.Contains(w.config.RepositoryIDs, state.Pending.RepositoryID) {
		return false, errors.New("ADX pending export is outside configured scope")
	}
	if state.PendingSince.IsZero() {
		return false, errors.New("ADX pending export is missing its retention clock")
	}
	if time.Since(state.PendingSince) > 7*24*time.Hour {
		state.GapCount++
		state.LastGapRunID, state.LastGapRevision = state.Pending.RunID, state.Pending.Revision
		state.After, state.Pending = state.Pending.RunID, nil
		state.PendingSince = time.Time{}
		w.counter(ctx, "adxGap")
		slog.WarnContext(ctx, "ADX export gap", "reason", "delivery-expired")
		return true, w.save(ctx, state)
	}
	if err := w.deliver(ctx, *state.Pending); err != nil {
		return false, err
	}
	state.After = state.Pending.RunID
	state.Pending = nil
	state.PendingSince = time.Time{}
	if err := w.save(ctx, state); err != nil {
		return false, err
	}
	w.counter(ctx, "adxExported")
	exportLog.Print("event=adx-export status=completed")
	return true, nil
}

func (w *Worker) save(ctx context.Context, state deliveryState) error {
	valid, err := w.services.Leases.RenewLock(ctx, w.key, w.owner, 2*time.Minute)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("ADX worker lost its lease")
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(content) > 512<<10 {
		return errors.New("ADX delivery state exceeds 512 KiB")
	}
	if err := w.services.State.SetOperationalState(ctx, w.key, content); err != nil {
		return err
	}
	w.mu.Lock()
	w.status.Gaps = state.GapCount
	w.status.Degraded = w.status.Degraded || state.GapCount > 0
	w.mu.Unlock()
	return nil
}

func (w *Worker) deliver(ctx context.Context, projection Projection) (err error) {
	if err := w.sink.Capacity(ctx); err != nil {
		return err
	}
	delivery := w.key + ":" + projection.ID
	reservation, err := w.services.Deliveries.ReserveDelivery(ctx, delivery, time.Minute)
	if err != nil {
		return err
	}
	if reservation == operational.DeliveryInProgress {
		return errors.New("ADX projection delivery is reserved")
	}
	if reservation == operational.DeliveryReserved {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if releaseErr := w.services.Deliveries.ReleaseDeliveryReservation(cleanup, delivery); releaseErr != nil {
				slog.WarnContext(cleanup, "ADX delivery reservation release failed")
				err = errors.Join(err, releaseErr)
			}
		}()
		for _, family := range families {
			if rows := projection.Tables[family.table]; len(rows) > 0 {
				if err := w.sink.Write(ctx, family.table, rows); err != nil {
					return err
				}
			}
		}
	}
	if err := w.sink.Verify(ctx, projection, false); err != nil {
		if reservation == operational.DeliveryAlreadyCommitted && errors.Is(err, ErrVisibility) {
			if forgetErr := w.services.Deliveries.ForgetDelivery(ctx, delivery); forgetErr != nil {
				return errors.Join(err, forgetErr)
			}
		}
		return err
	}
	if reservation != operational.DeliveryAlreadyCommitted {
		if _, err := w.services.Deliveries.RememberDelivery(ctx, delivery, time.Hour); err != nil {
			return err
		}
	}
	if err := w.sink.Write(ctx, "SessionVersions", []model.Row{projection.Manifest}); err != nil {
		return err
	}
	return w.sink.Verify(ctx, projection, true)
}

func (w *Worker) counter(ctx context.Context, name string) {
	if err := w.services.IngestionMetrics.IncrementIngestionCounter(ctx, name); err != nil {
		slog.WarnContext(ctx, "ADX metric recording failed")
	}
}
