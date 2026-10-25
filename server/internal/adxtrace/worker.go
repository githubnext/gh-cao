package adxtrace

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

type Sink interface {
	Capacity(context.Context) error
	Write(context.Context, string, []model.Row) error
	VerifyEvidence(context.Context, string, string, string, map[string][]model.Row) error
}

type Status struct {
	Stage    string `json:"stage"`
	Degraded bool   `json:"degraded"`
	Eligible int    `json:"eligible,omitempty"`
	Gaps     int    `json:"gaps,omitempty"`
	Pending  int    `json:"pending,omitempty"`
	Frames   int    `json:"frames,omitempty"`
}

type Worker struct {
	spool  *spool
	sink   Sink
	now    func() time.Time
	mu     sync.Mutex
	status Status
	mining sessionexport.MiningReader
}

func New(ctx context.Context, config Config, namespace string, sink Sink, readers ...sessionexport.MiningReader) (*Worker, error) {
	if sink == nil {
		return nil, errors.New("trace sink is required")
	}
	if len(readers) > 1 || (config.MiningEnabled && (len(readers) != 1 || readers[0] == nil)) || (!config.MiningEnabled && len(readers) != 0) {
		return nil, errors.New("enabled trace mining requires exactly one canonical reader")
	}
	s, err := newSpool(ctx, config, namespace)
	if err != nil {
		return nil, err
	}
	w := &Worker{spool: s, sink: sink, now: func() time.Time { return time.Now().UTC() },
		status: Status{Stage: "waiting"}}
	if len(readers) == 1 {
		w.mining = readers[0]
	}
	return w, nil
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *Worker) WithdrawRepository(ctx context.Context, repository string) error {
	if !slices.Contains(w.spool.config.Repositories, repository) {
		return nil
	}
	return w.spool.withdraw(ctx, repository)
}

func (w *Worker) Run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		more, err := w.Step(ctx)
		w.mu.Lock()
		w.status.Degraded = err != nil || w.status.Gaps > 0
		w.status.Stage = "waiting"
		if err != nil {
			w.status.Stage = "retrying"
		}
		w.mu.Unlock()
		switch {
		case err != nil && ctx.Err() == nil:
			slog.WarnContext(ctx, "ADX trace delivery deferred")
			delay = min(time.Minute, delay*2)
		case more:
			delay = time.Millisecond
		default:
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) Step(ctx context.Context) (more bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err = w.spool.locked(ctx, func(root *os.Root, state *ledger) error {
		if err := w.freeze(root, state); err != nil {
			return err
		}
		w.mu.Lock()
		w.status = Status{Stage: "exporting", Frames: len(state.Frames)}
		for _, r := range state.Records {
			if r.State == "eligible" {
				w.status.Eligible++
			} else {
				w.status.Gaps++
			}
			if r.Delivered == "" {
				w.status.Pending++
			}
			if r.Mining != nil && (r.Mining.State != "ready" || !w.now().Before(r.Mining.FreshUntil)) {
				w.status.Gaps++
			}
			if r.Mining != nil && r.Mining.Delivered == "" {
				w.status.Pending++
			}
			if w.mining != nil && r.State == "eligible" && r.Delivered != "" && w.selected(state, r) && r.Mining == nil {
				if r.MiningCheckedAt.IsZero() {
					w.status.Pending++
				} else {
					w.status.Gaps++
				}
			}
		}
		w.mu.Unlock()
		if w.mining != nil && !state.MiningTurn {
			did, err := w.stepMining(ctx, root, state)
			if err != nil {
				return err
			}
			if did {
				state.MiningTurn = true
				more = true
				return w.spool.save(root, state)
			}
		}
		state.MiningTurn = false
		ids := make([]string, 0, len(state.Records))
		for id := range state.Records {
			ids = append(ids, id)
		}
		slices.SortFunc(ids, func(a, b string) int {
			ra, rb := state.Records[a], state.Records[b]
			if ra.State == "withdrawn" && rb.State != "withdrawn" {
				return -1
			}
			if rb.State == "withdrawn" && ra.State != "withdrawn" {
				return 1
			}
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		})
		for _, id := range ids {
			r := state.Records[id]
			if r.Delivered != "" || r.State == "eligible" && !w.selected(state, r) {
				continue
			}
			if err := w.deliverRecord(ctx, root, r); err != nil {
				return err
			}
			r.Delivered = strconv.FormatInt(r.Revision, 10)
			more = true
			return w.spool.save(root, state)
		}
		frameIDs := make([]string, 0, len(state.Frames))
		for id := range state.Frames {
			frameIDs = append(frameIDs, id)
		}
		slices.Sort(frameIDs)
		for _, id := range frameIDs {
			f := state.Frames[id]
			if f.Delivered != "" {
				continue
			}
			ready := f.State != "invalidated"
			for member, version := range f.Members {
				r := state.Records[member]
				ready = ready && r != nil && r.State == "eligible" && r.Version == version && r.Delivered != ""
			}
			if !ready && f.State != "invalidated" {
				continue
			}
			if f.State != "invalidated" {
				f.State = "ready"
				if f.SourceGaps > 0 {
					f.State = "incomplete"
				}
			}
			if err := w.deliverFrame(ctx, f); err != nil {
				return err
			}
			f.Delivered = strconv.FormatInt(f.Revision, 10)
			more = true
			return w.spool.save(root, state)
		}
		datasetIDs := make([]string, 0, len(state.Datasets))
		for id := range state.Datasets {
			datasetIDs = append(datasetIDs, id)
		}
		slices.Sort(datasetIDs)
		for _, id := range datasetIDs {
			d := state.Datasets[id]
			if d.Delivered != "" {
				continue
			}
			for _, row := range d.Rows {
				rows := []model.Row{row}
				if err := seal(w.spool.namespace, scalar(row["RunId"]), scalar(row["ProjectionId"]),
					int64(integer(row["RunAttempt"])), int64(integer(row["SourceRevision"])), "TraceDatasets", rows); err != nil {
					return err
				}
				if err := w.writeBatches(ctx, "TraceDatasets", rows); err != nil {
					return err
				}
				if err := w.sink.VerifyEvidence(ctx, w.spool.namespace, scalar(row["RunId"]), scalar(row["ProjectionId"]),
					map[string][]model.Row{"TraceDatasets": rows}); err != nil {
					return err
				}
			}
			d.Delivered = d.State
			more = true
			return w.spool.save(root, state)
		}
		if w.mining != nil {
			did, err := w.stepMining(ctx, root, state)
			if err != nil {
				return err
			}
			if did {
				state.MiningTurn = true
				more = true
				return w.spool.save(root, state)
			}
		}
		return nil
	})
	return more, err
}

func (w *Worker) selected(state *ledger, r *record) bool {
	if w.spool.config.Mode == "full" {
		return true
	}
	f := state.Frames[w.spool.frameID(r.Repository, r.Day)]
	if f == nil {
		return false
	}
	_, selected := f.Members[r.ID]
	return selected
}

func (w *Worker) freeze(root *os.Root, state *ledger) error {
	changed := false
	for _, r := range state.Records {
		if r.State != "eligible" || w.now().Sub(r.CapturedAt) < w.spool.config.Retention {
			continue
		}
		file := r.File
		r.State, r.Reason, r.File, r.Delivered = "expired", "local-retention", "", ""
		derived := invalidateDatasets(state, r.ID)
		derived = append(derived, invalidateMining(state, r)...)
		state.Revision++
		r.Revision = state.Revision
		if f := state.Frames[w.spool.frameID(r.Repository, r.Day)]; f != nil {
			f.State, f.Delivered = "invalidated", ""
			state.Revision++
			f.Revision = state.Revision
		}
		// Save the no-replay intent before deleting any approved payload.
		if err := w.spool.save(root, state); err != nil {
			return err
		}
		if file != "" {
			if err := root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, file := range derived {
			if err := root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		changed = true
	}
	groups := map[string][]*record{}
	for _, r := range state.Records {
		id := w.spool.frameID(r.Repository, r.Day)
		if state.Frames[id] == nil {
			groups[id] = append(groups[id], r)
		}
	}
	for id, records := range groups {
		day, err := time.Parse(time.DateOnly, records[0].Day)
		if err != nil {
			return errors.New("invalid trace frame day")
		}
		cutoff := day.Add(24*time.Hour + w.spool.config.FrameDelay)
		if w.now().Before(cutoff) {
			continue
		}
		f := &frame{ID: id, Repository: records[0].Repository, Day: records[0].Day, Cutoff: w.now(),
			State: "pending", Members: map[string]string{}}
		eligible := make([]*record, 0, len(records))
		for _, r := range records {
			switch r.State {
			case "eligible":
				eligible = append(eligible, r)
			case "excluded":
				f.Excluded++
			default:
				f.SourceGaps++
			}
		}
		f.Population = len(eligible)
		slices.SortFunc(eligible, func(a, b *record) int {
			ra, rb := w.rank(id, a.ID), w.rank(id, b.ID)
			if ra < rb {
				return -1
			}
			if ra > rb {
				return 1
			}
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
		count := len(eligible)
		if w.spool.config.Mode == "sample" {
			count = min(count, w.spool.config.SampleCount)
		}
		for _, r := range eligible[:count] {
			f.Members[r.ID] = r.Version
		}
		membership, err := json.Marshal(f.Members)
		if err != nil {
			return err
		}
		f.MembershipDigest = digest(membership)
		state.Revision++
		f.Revision = state.Revision
		state.Frames[id] = f
		changed = true
	}
	if changed {
		return w.spool.save(root, state)
	}
	return nil
}

func (w *Worker) rank(frameID, id string) string {
	mac := hmac.New(sha256.New, []byte(w.spool.config.Seed))
	_, _ = mac.Write([]byte(frameID + "\x00" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

func (w *Worker) deliverRecord(ctx context.Context, root *os.Root, r *record) error {
	var e Evidence
	if r.State == "eligible" {
		var err error
		e, err = readEvidence(root, r, w.spool.config.MaxTraceBytes)
		if err != nil {
			return err
		}
	} else {
		e = Evidence{ID: r.ID, Version: digest([]byte(r.ID + r.State + strconv.FormatInt(r.Revision, 10))),
			Attempt: r.Attempt, Tables: map[string][]model.Row{"TraceManifest": {{
				"Repository": r.Repository, "ArtifactDigest": r.ArtifactDigest, "PolicyDigest": r.PolicyDigest,
				"EventCount": 0, "PartCount": 0, "ContentDigest": "", "State": r.State,
				"Gaps": []string{r.Reason}, "CapturedAt": r.CapturedAt.Format(time.RFC3339Nano),
			}}}}
		if err := seal(w.spool.namespace, e.ID, e.Version, e.Attempt, r.Revision, "TraceManifest", e.Tables["TraceManifest"]); err != nil {
			return err
		}
	}
	if err := w.sink.Capacity(ctx); err != nil {
		return err
	}
	data := map[string][]model.Row{}
	for _, table := range []string{"TraceEvents", "TraceContentParts"} {
		rows := e.Tables[table]
		if len(rows) == 0 {
			continue
		}
		if err := w.writeBatches(ctx, table, rows); err != nil {
			return err
		}
		data[table] = rows
	}
	if len(data) > 0 {
		if err := w.sink.VerifyEvidence(ctx, w.spool.namespace, e.ID, e.Version, data); err != nil {
			return err
		}
	}
	manifest := map[string][]model.Row{"TraceManifest": e.Tables["TraceManifest"]}
	if err := w.writeBatches(ctx, "TraceManifest", manifest["TraceManifest"]); err != nil {
		return err
	}
	return w.sink.VerifyEvidence(ctx, w.spool.namespace, e.ID, e.Version, manifest)
}

func (w *Worker) writeBatches(ctx context.Context, table string, rows []model.Row) error {
	start, bytes := 0, 0
	for i, row := range rows {
		content, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if len(content)+1 > 1<<20 {
			return errors.New("trace row exceeds streaming request limit")
		}
		if bytes+len(content)+1 > 1<<20 || i-start >= 256 {
			if err := w.sink.Write(ctx, table, rows[start:i]); err != nil {
				return err
			}
			start, bytes = i, 0
		}
		bytes += len(content) + 1
	}
	if start < len(rows) {
		return w.sink.Write(ctx, table, rows[start:])
	}
	return nil
}

func (w *Worker) deliverFrame(ctx context.Context, f *frame) error {
	version := digest([]byte(f.ID + f.MembershipDigest + f.State + strconv.FormatInt(f.Revision, 10)))
	members := make([]model.Row, 0, len(f.Members))
	for id, traceVersion := range f.Members {
		members = append(members, model.Row{
			"FrameId": f.ID, "TraceId": id, "TraceVersion": traceVersion,
			"Probability": float64(len(f.Members)) / float64(f.Population),
			"Weight":      float64(f.Population) / float64(len(f.Members)), "State": f.State,
		})
	}
	if err := seal(w.spool.namespace, f.ID, version, 0, f.Revision, "TraceSampleMembership", members); err != nil {
		return err
	}
	if len(members) > 0 {
		if err := w.writeBatches(ctx, "TraceSampleMembership", members); err != nil {
			return err
		}
		if err := w.sink.VerifyEvidence(ctx, w.spool.namespace, f.ID, version, map[string][]model.Row{"TraceSampleMembership": members}); err != nil {
			return err
		}
	}
	rows := []model.Row{{
		"Repository": f.Repository, "Day": f.Day, "Cutoff": f.Cutoff.Format(time.RFC3339Nano),
		"Mode": w.spool.config.Mode, "SeedDigest": digest([]byte(w.spool.config.Seed)), "Algorithm": "hmac-sha256-rank-v1",
		"Population": f.Population, "Selected": len(f.Members), "SourceGaps": f.SourceGaps, "Excluded": f.Excluded,
		"MembershipDigest": f.MembershipDigest, "State": f.State,
	}}
	if err := seal(w.spool.namespace, f.ID, version, 0, f.Revision, "TraceSampleFrames", rows); err != nil {
		return err
	}
	if err := w.writeBatches(ctx, "TraceSampleFrames", rows); err != nil {
		return err
	}
	return w.sink.VerifyEvidence(ctx, w.spool.namespace, f.ID, version, map[string][]model.Row{"TraceSampleFrames": rows})
}
