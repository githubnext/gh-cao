package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// Only private receipt metadata belongs in state, never raw session content.
type nativeRecord struct {
	Source     sessionarchive.Source
	Member     sessionarchive.Member
	CapturedAt time.Time
	Gap        string
}

// Native is the explicitly selected, file-free hosted producer. The existing
// destination-bound acceptance and single-exporter lease gates still apply.
// Acquisition may acknowledge an attempt only after CaptureSession succeeds.
type Native struct {
	*Archive
	capture sessionarchive.Config
}

func NewNative(ctx context.Context, capture sessionarchive.Config, destination Config) (*Native, error) {
	if !capture.Enabled || !capture.NativeProducer || !destination.Enabled {
		return nil, errors.New("native archive requires explicit approved capture and Blob selection")
	}
	if err := capture.Validate(); err != nil {
		return nil, err
	}
	if err := destination.Validate(); err != nil {
		return nil, err
	}
	if capture.Namespace != destination.Namespace || !slices.Equal(capture.Repositories, destination.RepositoryIDs) {
		return nil, ErrScope
	}
	if destination.Acceptance != destination.NativeAcceptanceBinding() {
		return nil, ErrConformance
	}
	a := &Archive{native: true, config: destination,
		blob:      &blobClient{root: destination.blobRoot(), http: newHTTP(destination, "https://storage.azure.com/.default")},
		queryHTTP: newHTTP(destination, "https://kusto.kusto.windows.net/.default"),
		verifyADX: true, now: time.Now}
	if err := a.open(ctx); err != nil {
		return nil, err
	}
	return &Native{Archive: a, capture: capture}, nil
}

func (a *Archive) validateNativeRecords(state archiveState) error {
	if state.SourceFenced && state.Producer != "native" {
		return ErrIntegrity
	}
	if len(state.NativeRecords) > 0 && state.Producer != "native" {
		return ErrIntegrity
	}
	keys := map[string]bool{}
	sources := map[sessionarchive.Source]bool{}
	for _, source := range state.WithdrawnSources {
		if !state.SourceFenced || source.Validate() != nil || !a.config.authorized(a.config.Namespace, source.Repository) || sources[source] {
			return ErrIntegrity
		}
		sources[source] = true
	}
	active := make(map[sessionarchive.Member]bool, len(state.Manifest.Members))
	complete := make(map[sessionarchive.Member]bool, len(state.Manifest.Members))
	for _, m := range state.Manifest.Members {
		active[m] = true
	}
	for _, record := range state.NativeRecords {
		m := record.Member
		if state.SourceFenced && (record.Source.Validate() != nil || record.Source.Repository != m.Repository || sources[record.Source]) {
			return ErrIntegrity
		}
		run, err := strconv.ParseUint(m.RunID, 10, 64)
		if !a.config.authorized(m.Namespace, m.Repository) || err != nil || run == 0 ||
			strconv.FormatUint(run, 10) != m.RunID || m.Attempt < 1 || record.CapturedAt.IsZero() ||
			keys[memberKey(m)] || !validNativeGap(record.Gap) ||
			m.ID != digest([]byte(fmt.Sprintf("%s\x00%s\x00%d", m.Repository, m.RunID, m.Attempt))) {
			return ErrIntegrity
		}
		keys[memberKey(m)] = true
		if record.Gap == "" {
			if !active[m] {
				return ErrIntegrity
			}
			complete[m] = true
		}
	}
	if state.Producer == "native" && len(complete) != len(active) {
		return ErrIntegrity
	}
	return nil
}

func validNativeGap(gap string) bool {
	switch gap {
	case "", "missing-session", "ambiguous-session", "artifact-identity-gap", "size-exclusion", "projection-gap":
		return true
	}
	return false
}

func (n *Native) traceConfig() adxtrace.Config {
	return adxtrace.Config{Enabled: true, SharedStorage: true, Repositories: n.capture.Repositories,
		Policy: n.capture.Policy, Mode: "full", MaxTraceBytes: n.capture.MaxTraceBytes,
		MaxSpoolBytes: n.capture.MaxStagingBytes, MaxEntries: n.capture.MaxEntries}
}

// CaptureSession consumes only the collector's exact artifact/attempt-checked
// envelope. It uses the shared closed approval/redaction projection; sampled
// archive capture is deliberately unsupported, not approximated.
func (n *Native) CaptureSession(ctx context.Context, repository, runID string, attempt int64, completed time.Time, content []byte, gap string) error {
	return n.captureSession(ctx, sessionarchive.Source{}, repository, runID, attempt, completed, content, gap)
}

func (n *Native) CaptureSessionAt(ctx context.Context, source sessionarchive.Source, runID string, attempt int64, completed time.Time, content []byte, gap string) error {
	if err := source.Validate(); err != nil {
		return err
	}
	return n.captureSession(ctx, source, source.Repository, runID, attempt, completed, content, gap)
}

func (n *Native) captureSession(ctx context.Context, source sessionarchive.Source, repository, runID string, attempt int64, completed time.Time, content []byte, gap string) error {
	if !slices.Contains(n.capture.Repositories, repository) {
		return nil
	}
	run, err := strconv.ParseUint(runID, 10, 64)
	if err != nil || run == 0 || strconv.FormatUint(run, 10) != runID || attempt < 1 || completed.IsZero() ||
		!validNativeGap(gap) || gap == "size-exclusion" || gap == "projection-gap" {
		return ErrIntegrity
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := n.expire(ctx); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.ready(); err != nil {
		return err
	}
	// Resolve an ambiguous previous acknowledgement before selecting a new
	// generation. No in-process receipt is authority after a failed CAS read.
	if err := n.load(ctx); err != nil {
		return err
	}
	if n.state.SourceFenced != (source.Epoch > 0) || slices.Contains(n.state.WithdrawnSources, source) {
		return ErrWithdrawn
	}
	n.state.Producer = "native"
	now := n.now().UTC()
	if completed.After(now.Add(time.Minute)) || completed.Before(now.Add(-n.capture.ReplayHorizon)) {
		return sessionarchive.ErrReplay
	}
	m := sessionarchive.Member{Namespace: n.capture.Namespace, Repository: repository, RunID: runID, Attempt: attempt,
		ID: digest([]byte(fmt.Sprintf("%s\x00%s\x00%d", repository, runID, attempt)))}
	if slices.Contains(n.state.WithdrawnRepositories, repository) {
		return ErrWithdrawn
	}
	for _, fence := range n.state.Fences {
		if memberKey(m) == memberKey(sessionarchive.Member{Namespace: fence.Namespace, Repository: fence.Repository, RunID: fence.RunID, Attempt: fence.Attempt}) && fence.Withdrawn {
			return ErrWithdrawn
		}
	}
	records := slices.Clone(n.state.NativeRecords)
	index := slices.IndexFunc(records, func(r nativeRecord) bool { return memberKey(r.Member) == memberKey(m) })
	if index >= 0 && records[index].Source != source {
		return ErrWithdrawn // Old-epoch cleanup must finish before this identity can be reused.
	}
	if int64(len(content)) > n.capture.MaxTraceBytes {
		gap = "size-exclusion"
	}
	var projection sessionarchive.Projection
	if gap == "" {
		projection, err = sessionarchive.Project(n.traceConfig(), n.capture.Namespace, repository, runID,
			completed.UTC().Format(time.DateOnly), attempt, content, now)
		if err != nil {
			gap = "projection-gap"
		}
	}
	if gap != "" {
		// A later source gap must not erase already admitted complete evidence.
		if index >= 0 && records[index].Gap == "" {
			return fmt.Errorf("session archive acquisition gap: %s", gap)
		}
		record := nativeRecord{Source: source, Member: m, CapturedAt: now, Gap: gap}
		if index < 0 {
			records = append(records, record)
		} else {
			records[index] = record
		}
		if len(records) > n.capture.MaxEntries {
			return sessionarchive.ErrCapacity
		}
		next := n.state
		next.NativeRecords = records
		if err := n.save(ctx, next, time.Time{}); err != nil {
			return err
		}
		return fmt.Errorf("session archive acquisition gap: %s", gap)
	}
	m = projection.Member()
	for _, fence := range n.state.Fences {
		if fence.Namespace == m.Namespace && fence.Repository == m.Repository && fence.RunID == m.RunID &&
			fence.Attempt == m.Attempt && slices.Contains(fence.Versions, m.Version) {
			return sessionarchive.ErrReplay
		}
	}
	if index >= 0 && records[index].Gap == "" && records[index].Member == m {
		for _, file := range n.state.Manifest.Files {
			if slices.Contains(file.Members, m) {
				return n.blob.verify(ctx, file.Path, file.Digest, file.Bytes)
			}
		}
		return ErrIntegrity
	}
	encoded, err := json.Marshal(projection)
	if err != nil || int64(len(encoded)) > 4*n.capture.MaxTraceBytes || int64(len(encoded)) > n.capture.MaxStagingBytes {
		return sessionarchive.ErrCapacity
	}
	data, file, err := sessionarchive.NativeParquet(ctx, projection, min(n.config.MaxFileBytes, n.capture.MaxStagingBytes))
	if err != nil {
		return err
	}
	var stored int64
	for _, f := range append(slices.Clone(n.state.Manifest.Files), n.state.Pending...) {
		stored += f.Bytes
	}
	for _, f := range n.state.Retired {
		stored += f.Bytes
	}
	if stored+file.Bytes > n.capture.MaxArchiveBytes {
		return sessionarchive.ErrCapacity
	}
	record := nativeRecord{Source: source, Member: m, CapturedAt: now}
	if index < 0 {
		records = append(records, record)
	} else {
		records[index] = record
	}
	if len(records) > n.capture.MaxEntries {
		return sessionarchive.ErrCapacity
	}
	next := n.state.Manifest
	next.Generation++
	next.CreatedAt = now
	next.Members = slices.DeleteFunc(slices.Clone(next.Members), func(old sessionarchive.Member) bool { return memberKey(old) == memberKey(m) })
	next.Members = append(next.Members, m)
	next.Files = slices.DeleteFunc(slices.Clone(next.Files), func(old sessionarchive.File) bool {
		return !slices.ContainsFunc(old.Members, func(member sessionarchive.Member) bool { return slices.Contains(next.Members, member) })
	})
	file.Path = objectKey(file)
	next.Files = append(next.Files, file)
	if records == nil {
		records = []nativeRecord{}
	}
	return n.publish(ctx, next, func(ctx context.Context, f sessionarchive.File) error {
		if f.Digest != file.Digest || f.Bytes != int64(len(data)) || digest(data) != f.Digest {
			return ErrIntegrity
		}
		return n.blob.immutable(ctx, objectKey(f), bytes.NewReader(data), f.Bytes, f.Digest)
	}, records, nil)
}

func (n *Native) Flush(ctx context.Context) error { return n.expire(ctx) }

func (n *Native) expire(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.ready(); err != nil {
		return err
	}
	if err := n.load(ctx); err != nil {
		return err
	}
	now := n.now().UTC()
	records := slices.DeleteFunc(slices.Clone(n.state.NativeRecords), func(r nativeRecord) bool {
		return r.CapturedAt.Before(now.Add(-n.capture.Retention)) || slices.Contains(n.state.WithdrawnRepositories, r.Member.Repository)
	})
	next := n.state.Manifest
	next.Generation++
	next.CreatedAt = now
	next.Members = slices.DeleteFunc(slices.Clone(next.Members), func(m sessionarchive.Member) bool {
		return !slices.ContainsFunc(records, func(r nativeRecord) bool { return r.Gap == "" && r.Member == m })
	})
	next.Files = slices.DeleteFunc(slices.Clone(next.Files), func(f sessionarchive.File) bool {
		return !slices.ContainsFunc(f.Members, func(m sessionarchive.Member) bool { return slices.Contains(next.Members, m) })
	})
	if len(records) == len(n.state.NativeRecords) && len(next.Files) == len(n.state.Manifest.Files) {
		return nil
	}
	return n.publish(ctx, next, func(context.Context, sessionarchive.File) error { return ErrIntegrity }, append([]nativeRecord{}, records...), nil)
}

func (n *Native) WithdrawRepository(ctx context.Context, repository string) error {
	if err := n.Archive.WithdrawRepository(ctx, repository); err != nil {
		return err
	}
	return n.expire(ctx)
}

func (n *Native) Withdraw(ctx context.Context, repository, runID string, attempt int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.ready(); err != nil {
		return err
	}
	if err := n.load(ctx); err != nil {
		n.failLease()
		return err
	}
	run, err := strconv.ParseUint(runID, 10, 64)
	if !n.config.authorized(n.config.Namespace, repository) || err != nil || run == 0 ||
		strconv.FormatUint(run, 10) != runID || attempt < 1 {
		return ErrScope
	}
	m := sessionarchive.Member{Namespace: n.config.Namespace, Repository: repository, RunID: runID, Attempt: attempt}
	key := memberKey(m)
	for _, fence := range n.state.Fences {
		if fence.Namespace == m.Namespace && fence.Repository == repository && fence.RunID == runID &&
			fence.Attempt == attempt && fence.Withdrawn {
			return nil
		}
	}
	next := n.state.Manifest
	next.Generation++
	next.CreatedAt = n.now().UTC()
	next.Members = slices.DeleteFunc(slices.Clone(next.Members), func(m sessionarchive.Member) bool { return memberKey(m) == key })
	next.Files = slices.DeleteFunc(slices.Clone(next.Files), func(f sessionarchive.File) bool {
		return !slices.ContainsFunc(f.Members, func(m sessionarchive.Member) bool { return slices.Contains(next.Members, m) })
	})
	next.Fences = append(slices.Clone(next.Fences), sessionarchive.Fence{Namespace: m.Namespace,
		Repository: repository, RunID: runID, Attempt: attempt, Withdrawn: true})
	records := slices.DeleteFunc(append([]nativeRecord{}, n.state.NativeRecords...), func(r nativeRecord) bool {
		return memberKey(r.Member) == key
	})
	if err := n.publish(ctx, next, func(context.Context, sessionarchive.File) error { return ErrIntegrity }, records, nil); err != nil {
		n.failLease()
		return err
	}
	return nil
}

func (n *Native) Maintain(ctx context.Context) error {
	if err := n.expire(ctx); err != nil {
		return err
	}
	return n.Archive.Maintain(ctx)
}
