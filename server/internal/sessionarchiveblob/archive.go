package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

type retirement struct {
	Digest string
	Key    string
	Bytes  int64
	After  time.Time
}

type archiveState struct {
	Manifest              sessionarchive.Manifest
	Fences                []sessionarchive.Fence
	WithdrawnRepositories []string
	Pending               []sessionarchive.File
	Retired               []retirement
}

type pointer struct {
	Version      int
	State        string
	Generation   uint64
	Revisions    uint64
	ReadersUntil time.Time
}

type reader struct {
	members []sessionarchive.Member
	cancel  context.CancelFunc
}

type fileVerifier interface {
	Verify(context.Context, string, sessionarchive.File) error
}

type Archive struct {
	config    Config
	converter fileVerifier
	blob      *blobClient
	queryHTTP *http.Client
	verifyADX bool
	mu        sync.Mutex
	state     archiveState
	active    pointer
	etag      string
	lease     string
	lost      atomic.Bool
	closed    bool
	readers   map[string]reader
	stop      context.CancelFunc
	done      chan struct{}
	now       func() time.Time
}

// New acquires the Blob lease only after the explicit activation gate. A
// disabled configuration returns an inert adapter without credential or I/O.
func New(ctx context.Context, config Config, converter *sessionarchive.Converter) (*Archive, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return &Archive{config: config}, nil
	}
	if config.Acceptance != config.AcceptanceBinding() {
		return nil, ErrConformance
	}
	if converter == nil {
		return nil, errors.New("hosted archive requires the shared verifying Parquet converter")
	}
	a := &Archive{config: config, converter: converter,
		blob:      &blobClient{root: config.blobRoot(), http: newHTTP(config, "https://storage.azure.com/.default")},
		queryHTTP: newHTTP(config, "https://kusto.kusto.windows.net/.default"),
		verifyADX: true, readers: map[string]reader{}, now: time.Now}
	if err := a.open(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Archive) open(ctx context.Context) error {
	if err := a.blob.private(ctx); err != nil {
		return err
	}
	initial, _ := json.Marshal(pointer{Version: 1})
	response, err := a.blob.request(ctx, http.MethodPut, "active.json", "", bytes.NewReader(initial), int64(len(initial)),
		http.Header{"If-None-Match": {"*"}, "x-ms-blob-type": {"BlockBlob"}})
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil && !isStatus(err, http.StatusPreconditionFailed) {
		// An ambiguous create must be resolved by the same subsequent read.
		if _, _, readErr := a.blob.get(ctx, "active.json", 4096); readErr != nil {
			return err
		}
	}
	a.lease = uuid.NewString()
	if err := a.blob.lease(ctx, "acquire", a.lease); err != nil {
		return err
	}
	if err := a.load(ctx); err != nil {
		_ = a.blob.lease(ctx, "release", a.lease)
		return err
	}
	// No runtime DDL: prove the preprovisioned external table is the approved
	// immutable-object partitioned destination before accepting any data.
	if err := a.verifyTable(ctx); err != nil {
		_ = a.blob.lease(ctx, "release", a.lease)
		return err
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.stop, a.done = cancel, make(chan struct{})
	go a.renew(lifetime)
	return nil
}

func decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrIntegrity
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrIntegrity
	}
	return nil
}

func (a *Archive) load(ctx context.Context) error {
	data, etag, err := a.blob.get(ctx, "active.json", 4096)
	if err != nil {
		return err
	}
	var active pointer
	if err := decode(data, &active); err != nil || active.Version != 1 ||
		(active.State == "" && active.Generation != 0) {
		return ErrIntegrity
	}
	state := archiveState{Manifest: sessionarchive.Manifest{Version: 1, CreatedAt: time.Unix(0, 0).UTC()}}
	if active.State != "" {
		if !digestPattern.MatchString(active.State) {
			return ErrIntegrity
		}
		data, _, err = a.blob.get(ctx, "manifests/"+active.State+".json", a.config.MaxStateBytes)
		if err != nil || digest(data) != active.State || decode(data, &state) != nil ||
			state.Manifest.Generation != active.Generation {
			return ErrIntegrity
		}
		if err := a.validateState(state); err != nil {
			return err
		}
	}
	a.active, a.etag, a.state = active, etag, state
	return nil
}

func (a *Archive) renew(ctx context.Context) {
	defer close(a.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := a.blob.lease(step, "renew", a.lease)
			cancel()
			if err != nil {
				// Renewal must not wait behind a long conversion or upload.
				a.lost.Store(true)
				a.mu.Lock()
				a.failLease()
				a.mu.Unlock()
				return
			}
		}
	}
}

func (a *Archive) failLease() {
	a.lost.Store(true)
	for _, r := range a.readers {
		r.cancel()
	}
}

func (a *Archive) ready() error {
	if !a.config.Enabled {
		return ErrDisabled
	}
	if a.lost.Load() || a.closed {
		return ErrLease
	}
	return nil
}

func (a *Archive) save(ctx context.Context, state archiveState, horizon time.Time) error {
	if err := a.ready(); err != nil {
		return err
	}
	// Immutable state history is deliberately retained for recovery, not
	// silently expired. Stop before its explicit initial 4 GiB ceiling
	// (1024 state objects at the 4 MiB hard state limit).
	if a.active.Revisions >= 1024 {
		return ErrCapacity
	}
	if err := a.validateState(state); err != nil {
		return err
	}
	if err := a.blob.lease(ctx, "renew", a.lease); err != nil {
		a.failLease()
		return err
	}
	data, err := json.Marshal(state)
	if err != nil || int64(len(data)) > a.config.MaxStateBytes {
		return ErrCapacity
	}
	hash := digest(data)
	if err := a.blob.immutable(ctx, "manifests/"+hash+".json", bytes.NewReader(data), int64(len(data)), hash); err != nil {
		return err
	}
	next := pointer{Version: 1, State: hash, Generation: state.Manifest.Generation, Revisions: a.active.Revisions + 1,
		ReadersUntil: maxTime(a.active.ReadersUntil, horizon)}
	content, _ := json.Marshal(next)
	response, err := a.blob.request(ctx, http.MethodPut, "active.json", "", bytes.NewReader(content), int64(len(content)),
		http.Header{"If-Match": {a.etag}, "x-ms-lease-id": {a.lease}, "x-ms-blob-type": {"BlockBlob"}})
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		// CAS may have committed despite a lost acknowledgement. Resolve it
		// under our still-held lease; never assume success on a failed read.
		check, etag, readErr := a.blob.get(ctx, "active.json", 4096)
		var current pointer
		if readErr == nil && decode(check, &current) == nil &&
			current.Version == next.Version && current.State == next.State && current.Generation == next.Generation &&
			current.Revisions == next.Revisions && current.ReadersUntil.Equal(next.ReadersUntil) {
			a.active, a.etag, a.state = next, etag, state
			return nil
		}
		if isStatus(err, http.StatusPreconditionFailed) || isStatus(err, http.StatusConflict) {
			a.failLease()
			return ErrConflict
		}
		return err
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		a.failLease()
		return ErrIntegrity
	}
	a.active, a.etag, a.state = next, etag, state
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func memberKey(m sessionarchive.Member) string {
	b, _ := json.Marshal([]any{m.Namespace, m.Repository, m.RunID, m.Attempt})
	return string(b)
}

func sameMember(a, b sessionarchive.Member) bool {
	return a == b
}

func mergeFence(fences []sessionarchive.Fence, incoming sessionarchive.Fence) []sessionarchive.Fence {
	for i, f := range fences {
		if f.Namespace == incoming.Namespace && f.Repository == incoming.Repository && f.RunID == incoming.RunID && f.Attempt == incoming.Attempt {
			f.Versions = slices.Clone(f.Versions)
			for _, version := range incoming.Versions {
				if !slices.Contains(f.Versions, version) {
					f.Versions = append(f.Versions, version)
				}
			}
			f.Withdrawn = f.Withdrawn || incoming.Withdrawn
			fences[i] = f
			return fences
		}
	}
	incoming.Versions = slices.Clone(incoming.Versions)
	return append(fences, incoming)
}

func (a *Archive) validateState(state archiveState) error {
	if _, err := a.normalize(state.Manifest, false); err != nil {
		return err
	}
	if len(state.Pending)+len(state.Manifest.Files)+len(state.Retired) > a.config.MaxFiles*3 {
		return ErrCapacity
	}
	entries := len(state.Manifest.Members) + len(state.WithdrawnRepositories)
	for _, repository := range state.WithdrawnRepositories {
		if !a.config.authorized(a.config.Namespace, repository) {
			return ErrScope
		}
	}
	for _, fence := range state.Fences {
		if !a.config.authorized(fence.Namespace, fence.Repository) || fence.RunID == "" || fence.Attempt < 1 {
			return ErrScope
		}
		entries += 1 + len(fence.Versions)
		for _, version := range fence.Versions {
			if !digestPattern.MatchString(version) {
				return ErrIntegrity
			}
		}
	}
	if entries > a.config.MaxMembers {
		return ErrCapacity
	}
	if len(state.Pending) > 0 {
		pending := sessionarchive.Manifest{Version: 1, CreatedAt: state.Manifest.CreatedAt, Files: state.Pending}
		if _, err := a.normalize(pending, false); err != nil {
			return err
		}
	}
	for _, retired := range state.Retired {
		parts := strings.Split(retired.Key, "/")
		if len(parts) != 6 || parts[0] != "objects" || !digestPattern.MatchString(parts[1]) ||
			!digestPattern.MatchString(parts[2]) || !digestPattern.MatchString(parts[4]) ||
			parts[4] != retired.Digest || parts[5] != "events.parquet" || retired.Bytes < 1 ||
			retired.Bytes > a.config.MaxFileBytes || retired.After.IsZero() {
			return ErrIntegrity
		}
		if parts[3] != "undated" {
			if _, err := time.Parse(time.DateOnly, parts[3]); err != nil {
				return ErrIntegrity
			}
		}
		if slices.ContainsFunc(state.Manifest.Files, func(f sessionarchive.File) bool { return f.Digest == retired.Digest }) {
			return ErrIntegrity
		}
	}
	return nil
}

func fileDay(file sessionarchive.File) string {
	if file.Day == "" {
		return "undated"
	}
	return file.Day
}

func objectKey(file sessionarchive.File) string {
	return "objects/" + digest([]byte(file.Members[0].Namespace)) + "/" +
		digest([]byte(file.Members[0].Repository)) + "/" + fileDay(file) + "/" +
		file.Digest + "/events.parquet"
}

func (a *Archive) normalize(candidate sessionarchive.Manifest, local bool) (sessionarchive.Manifest, error) {
	if candidate.Version != 1 || candidate.CreatedAt.IsZero() ||
		len(candidate.Files) > a.config.MaxFiles || len(candidate.Members) > a.config.MaxMembers {
		return sessionarchive.Manifest{}, ErrCapacity
	}
	candidate.Files = slices.Clone(candidate.Files)
	candidate.Members = slices.Clone(candidate.Members)
	physical := map[sessionarchive.Member]bool{}
	files := map[string]bool{}
	for i, file := range candidate.Files {
		if !digestPattern.MatchString(file.Digest) || files[file.Digest] ||
			file.Bytes < 1 || file.Bytes > a.config.MaxFileBytes || file.Rows < 1 ||
			len(file.Members) == 0 || len(file.Members) > a.config.MaxMembers {
			return sessionarchive.Manifest{}, ErrIntegrity
		}
		if file.Day != "" {
			day, err := time.Parse(time.DateOnly, file.Day)
			if err != nil || day.Format(time.DateOnly) != file.Day {
				return sessionarchive.Manifest{}, ErrIntegrity
			}
		}
		files[file.Digest] = true
		var rows int64
		for _, m := range file.Members {
			if !a.config.authorized(m.Namespace, m.Repository) || m.RunID == "" || m.Attempt < 1 ||
				!digestPattern.MatchString(m.ID) || !digestPattern.MatchString(m.Version) || m.Events < 1 || m.Events > 32768 || physical[m] ||
				m.Namespace != file.Members[0].Namespace || m.Repository != file.Members[0].Repository {
				return sessionarchive.Manifest{}, ErrScope
			}
			physical[m] = true
			rows += m.Events
		}
		if rows != file.Rows {
			return sessionarchive.Manifest{}, ErrIntegrity
		}
		if !local && file.Path != objectKey(file) {
			return sessionarchive.Manifest{}, ErrScope
		}
		candidate.Files[i].Path = objectKey(file)
	}
	if len(physical) > a.config.MaxMembers {
		return sessionarchive.Manifest{}, ErrCapacity
	}
	keys := map[string]bool{}
	for _, m := range candidate.Members {
		if !physical[m] || keys[memberKey(m)] {
			return sessionarchive.Manifest{}, ErrIntegrity
		}
		keys[memberKey(m)] = true
	}
	return candidate, nil
}

// Publish implements sessionarchive.Publisher. The candidate is the complete
// selected generation, not a delta. Durable staging remains owned by the
// shared archive until this returns success.
func (a *Archive) Publish(ctx context.Context, candidate sessionarchive.Manifest) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return err
	}
	normalized, err := a.normalize(candidate, true)
	if err != nil {
		return err
	}
	if normalized.Generation == a.state.Manifest.Generation && reflect.DeepEqual(normalized, a.state.Manifest) {
		return nil
	}
	if normalized.Generation != a.state.Manifest.Generation+1 {
		return ErrConflict
	}
	if err := a.blob.private(ctx); err != nil {
		return err
	}
	if a.verifyADX {
		if err := a.verifyTable(ctx); err != nil {
			return err
		}
	}
	next := a.state
	next.Manifest = normalized
	next.Fences = slices.Clone(a.state.Fences)
	withdrawing, published := false, false
	defer func() {
		if withdrawing && !published {
			a.failLease()
		}
	}()
	for _, fence := range normalized.Fences {
		if !a.config.authorized(fence.Namespace, fence.Repository) || fence.RunID == "" || fence.Attempt < 1 ||
			(!fence.Withdrawn && len(fence.Versions) == 0) {
			return ErrScope
		}
		// Shared fences enumerate all observed versions, including the current
		// admitted version. Only superseded observations are replay-denied;
		// permanent withdrawal denies the complete canonical attempt.
		if !fence.Withdrawn {
			fence.Versions = slices.Clone(fence.Versions)
			fence.Versions = slices.DeleteFunc(fence.Versions, func(version string) bool {
				return slices.ContainsFunc(normalized.Members, func(m sessionarchive.Member) bool {
					return m.Namespace == fence.Namespace && m.Repository == fence.Repository && m.RunID == fence.RunID &&
						m.Attempt == fence.Attempt && m.Version == version
				})
			})
		}
		if fence.Withdrawn || len(fence.Versions) > 0 {
			next.Fences = mergeFence(next.Fences, fence)
		}
	}
	for _, m := range normalized.Members {
		if slices.Contains(next.WithdrawnRepositories, m.Repository) {
			return ErrWithdrawn
		}
		for _, f := range next.Fences {
			if f.Namespace == m.Namespace && f.Repository == m.Repository && f.RunID == m.RunID && f.Attempt == m.Attempt &&
				(f.Withdrawn || slices.Contains(f.Versions, m.Version)) {
				return ErrWithdrawn
			}
		}
	}
	for _, old := range a.state.Manifest.Members {
		match, replaced := false, false
		for _, m := range normalized.Members {
			if memberKey(old) == memberKey(m) {
				match = sameMember(old, m)
				replaced = true
				break
			}
		}
		if match {
			continue
		}
		withdrawing = withdrawing || !replaced
		next.Fences = mergeFence(next.Fences, sessionarchive.Fence{Namespace: old.Namespace, Repository: old.Repository,
			RunID: old.RunID, Attempt: old.Attempt, Versions: []string{old.Version}, Withdrawn: !replaced})
		for _, r := range a.readers {
			for _, m := range r.members {
				if memberKey(m) == memberKey(old) && !replaced {
					r.cancel()
				}
			}
		}
	}
	for _, fence := range next.Fences {
		if fence.Withdrawn {
			for _, r := range a.readers {
				for _, m := range r.members {
					if m.Namespace == fence.Namespace && m.Repository == fence.Repository && m.RunID == fence.RunID && m.Attempt == fence.Attempt {
						r.cancel()
						withdrawing = true
					}
				}
			}
		}
	}
	if len(next.Fences)+len(next.Manifest.Members) > a.config.MaxMembers {
		return ErrCapacity
	}
	// Register every potentially uploaded object before external I/O, so a
	// restart never loses its orphan inventory or storage accounting.
	reserved := a.state
	reserved.Pending = slices.Clone(reserved.Pending)
	for _, file := range normalized.Files {
		known := slices.ContainsFunc(reserved.Manifest.Files, func(f sessionarchive.File) bool { return f.Digest == file.Digest }) ||
			slices.ContainsFunc(reserved.Pending, func(f sessionarchive.File) bool { return f.Digest == file.Digest })
		if !known {
			reserved.Pending = append(reserved.Pending, file)
		}
	}
	if len(reserved.Pending)+len(reserved.Retired)+len(reserved.Manifest.Files) > a.config.MaxFiles*3 {
		return ErrCapacity
	}
	if err := a.save(ctx, reserved, time.Time{}); err != nil {
		return err
	}
	for _, file := range candidate.Files {
		old := slices.IndexFunc(a.state.Manifest.Files, func(f sessionarchive.File) bool { return f.Digest == file.Digest })
		if old >= 0 {
			previous, proposed := a.state.Manifest.Files[old], file
			proposed.Path = previous.Path
			if !reflect.DeepEqual(previous, proposed) {
				return ErrIntegrity
			}
			if err := a.blob.verify(ctx, previous.Path, previous.Digest, previous.Bytes); err != nil {
				return err
			}
			continue
		}
		if err := a.blob.lease(ctx, "renew", a.lease); err != nil {
			a.failLease()
			return err
		}
		if err := a.converter.Verify(ctx, file.Path, file); err != nil {
			return ErrIntegrity
		}
		handle, err := os.Open(file.Path)
		if err != nil {
			return errors.New("archive staging file is unavailable")
		}
		info, err := handle.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Bytes {
			_ = handle.Close()
			return ErrIntegrity
		}
		err = a.blob.immutable(ctx, objectKey(file), handle, file.Bytes, file.Digest)
		_ = handle.Close()
		if err != nil {
			return err
		}
		if a.verifyADX {
			if err := a.verifyFile(ctx, file); err != nil {
				return err
			}
		}
	}
	next.Pending = nil
	next.Retired = slices.Clone(a.state.Retired)
	after := maxTime(a.now(), a.active.ReadersUntil).Add(a.config.RetirementGrace)
	for _, file := range append(slices.Clone(a.state.Manifest.Files), a.state.Pending...) {
		if !slices.ContainsFunc(normalized.Files, func(f sessionarchive.File) bool { return f.Digest == file.Digest }) &&
			!slices.ContainsFunc(next.Retired, func(r retirement) bool { return r.Digest == file.Digest }) {
			next.Retired = append(next.Retired, retirement{Digest: file.Digest, Key: objectKey(file), Bytes: file.Bytes, After: after})
		}
	}
	next.Retired = slices.DeleteFunc(next.Retired, func(r retirement) bool {
		return slices.ContainsFunc(normalized.Files, func(f sessionarchive.File) bool { return f.Digest == r.Digest })
	})
	err = a.save(ctx, next, time.Time{})
	published = err == nil
	return err
}

// Maintain deletes only explicitly retired, unreferenced objects after the
// persisted remote-execution horizon and conservative recovery grace.
func (a *Archive) Maintain(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return err
	}
	for i := len(a.state.Retired) - 1; i >= 0; i-- {
		r := a.state.Retired[i]
		if a.now().Before(r.After) || a.now().Before(a.active.ReadersUntil.Add(a.config.RetirementGrace)) {
			continue
		}
		if err := a.blob.lease(ctx, "renew", a.lease); err != nil {
			a.failLease()
			return err
		}
		response, err := a.blob.request(ctx, http.MethodDelete, r.Key, "", nil, 0, http.Header{})
		if response != nil {
			_ = response.Body.Close()
		}
		if err != nil && !isStatus(err, http.StatusNotFound) {
			return err
		}
		next := a.state
		next.Retired = slices.Clone(a.state.Retired)
		next.Retired = slices.Delete(next.Retired, i, i+1)
		if err := a.save(ctx, next, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// WithdrawRepository installs immediate remote fences even when a recovering
// local spool has no surviving membership for this repository. It does not
// change the candidate generation counter owned by the shared publisher.
func (a *Archive) WithdrawRepository(ctx context.Context, repository string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return err
	}
	if !a.config.authorized(a.config.Namespace, repository) {
		return ErrScope
	}
	for _, r := range a.readers {
		if slices.ContainsFunc(r.members, func(m sessionarchive.Member) bool { return m.Repository == repository }) {
			r.cancel()
		}
	}
	next := a.state
	next.Fences = slices.Clone(next.Fences)
	next.WithdrawnRepositories = slices.Clone(next.WithdrawnRepositories)
	if !slices.Contains(next.WithdrawnRepositories, repository) {
		next.WithdrawnRepositories = append(next.WithdrawnRepositories, repository)
	}
	next.Manifest.Members = slices.Clone(next.Manifest.Members)
	for _, m := range next.Manifest.Members {
		if m.Repository == repository {
			next.Fences = mergeFence(next.Fences, sessionarchive.Fence{Namespace: m.Namespace, Repository: m.Repository,
				RunID: m.RunID, Attempt: m.Attempt, Versions: []string{m.Version}, Withdrawn: true})
		}
	}
	next.Manifest.Members = slices.DeleteFunc(next.Manifest.Members, func(m sessionarchive.Member) bool { return m.Repository == repository })
	if len(next.Fences)+len(next.Manifest.Members) > a.config.MaxMembers {
		a.failLease()
		return ErrCapacity
	}
	if err := a.save(ctx, next, time.Time{}); err != nil {
		// Never serve stale content after a withdrawal request whose remote
		// acknowledgement is unresolved. Restart recovery revalidates Blob.
		a.failLease()
		return err
	}
	return nil
}

func (a *Archive) Status() sessionarchive.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	status := sessionarchive.Status{Enabled: a.config.Enabled, Generation: a.active.Generation,
		ActiveTraces: len(a.state.Manifest.Members)}
	for _, f := range a.state.Manifest.Files {
		status.ArchiveBytes += f.Bytes
	}
	for _, f := range a.state.Pending {
		status.StagingBytes += f.Bytes
	}
	for _, r := range a.state.Retired {
		status.ArchiveBytes += r.Bytes
	}
	return status
}

func (a *Archive) Close() error {
	a.mu.Lock()
	if !a.config.Enabled || a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	for _, r := range a.readers {
		r.cancel()
	}
	a.stop()
	a.mu.Unlock()
	<-a.done
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.blob.lease(ctx, "release", a.lease)
}

func (a *Archive) String() string {
	return fmt.Sprintf("hosted session archive(enabled=%t)", a.config.Enabled)
}
