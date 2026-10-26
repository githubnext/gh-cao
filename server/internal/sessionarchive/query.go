package sessionarchive

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Snapshot struct {
	archive  *Archive
	manifest Manifest
	scope    Scope
	ctx      context.Context
	cancel   context.CancelFunc
	expires  time.Time
	revoked  bool
}

func (a *Archive) Pin(ctx context.Context, scope Scope) (*Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	if err := a.reconcile(); err != nil {
		return nil, err
	}
	if a.publisher != nil {
		return nil, errors.New("hosted archive queries require the generation-bound destination adapter")
	}
	if scope.Namespace != a.config.Namespace || len(scope.Repositories) == 0 || len(scope.Repositories) > 1000 {
		return nil, ErrIntegrity
	}
	for _, repository := range scope.Repositories {
		if !slices.Contains(a.config.Repositories, repository) {
			return nil, ErrIntegrity
		}
		if a.state.WithdrawnRepositories[repository] {
			return nil, ErrWithdrawn
		}
	}
	for reader := range a.readers {
		if reader.ctx.Err() != nil || a.now().After(reader.expires) {
			reader.cancel()
			delete(a.readers, reader)
		}
	}
	if len(a.readers) >= 64 {
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scoped, cancel := context.WithCancel(ctx)
	s := &Snapshot{archive: a, manifest: cloneManifest(a.state.Manifest), scope: Scope{scope.Namespace, slices.Clone(scope.Repositories)},
		ctx: scoped, cancel: cancel, expires: a.now().Add(a.config.QueryTimeout)}
	s.manifest.Members = slices.DeleteFunc(s.manifest.Members, func(m Member) bool { return !a.allowed(m) })
	a.readers[s] = true
	return s, nil
}

func (s *Snapshot) Generation() uint64 { return s.manifest.Generation }

func (s *Snapshot) Close() error {
	s.archive.mu.Lock()
	defer s.archive.mu.Unlock()
	s.cancel()
	delete(s.archive.readers, s)
	return nil
}

func (s *Snapshot) check() error {
	s.archive.mu.Lock()
	defer s.archive.mu.Unlock()
	return s.checkLocked()
}

// Deliver serializes the final access check and result delivery with withdrawal.
// The callback must not call back into the archive.
func (s *Snapshot) Deliver(deliver func() error) error {
	s.archive.mu.Lock()
	defer s.archive.mu.Unlock()
	if err := s.checkLocked(); err != nil {
		return err
	}
	return deliver()
}

func (s *Snapshot) checkLocked() error {
	if s.archive.closed {
		return ErrClosed
	}
	if err := s.archive.reconcile(); err != nil {
		return err
	}
	for _, member := range s.manifest.Members {
		if slices.Contains(s.scope.Repositories, member.Repository) && !s.archive.allowed(member) {
			return ErrWithdrawn
		}
	}
	if s.ctx.Err() != nil {
		if s.revoked {
			return ErrWithdrawn
		}
		return s.ctx.Err()
	}
	if s.archive.now().After(s.expires) {
		s.cancel()
		return context.DeadlineExceeded
	}
	return nil
}

func (s *Snapshot) selected() []Member {
	var members []Member
	for _, m := range s.manifest.Members {
		if m.Namespace == s.scope.Namespace && slices.Contains(s.scope.Repositories, m.Repository) {
			members = append(members, m)
		}
	}
	return members
}

func (s *Snapshot) Timeline(ctx context.Context, request TimelineRequest) ([]Event, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if !validIdentity(s.scope.Namespace, request.Repository, request.RunID, request.Attempt) ||
		!slices.Contains(s.scope.Repositories, request.Repository) || request.Limit < 1 || request.Limit > 32768 {
		return nil, ErrLimit
	}
	var selected Member
	for _, m := range s.selected() {
		if m.Repository == request.Repository && m.RunID == request.RunID && m.Attempt == request.Attempt {
			selected = m
			break
		}
	}
	if selected.Version == "" {
		return nil, ErrNotFound
	}
	if selected.Events > int64(request.Limit) {
		return nil, ErrLimit
	}
	var file File
	for _, f := range s.manifest.Files {
		if slices.Contains(f.Members, selected) {
			file = f
			break
		}
	}
	if file.Path == "" {
		return nil, ErrIntegrity
	}
	if err := s.archive.converter.Verify(ctx, filepath.Join(s.archive.config.Directory, file.Path), file); err != nil {
		return nil, err
	}
	sql := fmt.Sprintf("SELECT %s FROM read_parquet(%s,hive_partitioning=false) WHERE Namespace=%s AND Repository=%s AND RunId=%s AND RunAttempt=%d AND TraceId=%s AND ProjectionId=%s ORDER BY Sequence LIMIT %d",
		eventSelect, literal(filepath.Join(s.archive.config.Directory, file.Path)), literal(selected.Namespace), literal(selected.Repository),
		literal(selected.RunID), selected.Attempt, literal(selected.ID), literal(selected.Version), request.Limit+1)
	combined, cancel := context.WithCancel(ctx)
	//nolint:contextcheck // Independent snapshot revocation must cancel the caller-scoped subprocess.
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	var rows []Event
	if err := s.archive.converter.query(combined, sql, &rows); err != nil {
		if accessErr := s.check(); accessErr != nil {
			return nil, accessErr
		}
		return nil, err
	}
	if int64(len(rows)) != selected.Events {
		return nil, ErrIntegrity
	}
	for i, event := range rows {
		if event.Sequence != int64(i) || event.ContentDigest != hash([]byte(event.PayloadJSON)) {
			return nil, ErrIntegrity
		}
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	return rows, nil
}

// Summary reads only typed columns, joins exact active membership before
// aggregation, and never decodes or counts superseded packed payload rows.
func (s *Snapshot) Summary(ctx context.Context) ([]Summary, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	members := s.selected()
	if len(members) == 0 {
		return []Summary{}, nil
	}
	if len(members) > 10000 {
		return nil, ErrLimit
	}
	var values, paths []string
	for _, m := range members {
		values = append(values, fmt.Sprintf("(%s,%s,%s,%d,%s,%s)", literal(m.Namespace), literal(m.Repository), literal(m.RunID), m.Attempt, literal(m.ID), literal(m.Version)))
	}
	for _, f := range s.manifest.Files {
		if slices.ContainsFunc(f.Members, func(m Member) bool { return slices.Contains(members, m) }) {
			paths = append(paths, literal(filepath.Join(s.archive.config.Directory, f.Path)))
		}
	}
	sql := `WITH active(Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId) AS (VALUES ` + strings.Join(values, ",") + `)
SELECT e.Repository AS repository,e.RunId AS runId,e.RunAttempt AS attempt,e.ProjectionId AS projectionId,count(*) AS events
FROM read_parquet([` + strings.Join(paths, ",") + `],hive_partitioning=false) e
INNER JOIN active a USING(Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId)
GROUP BY e.Repository,e.RunId,e.RunAttempt,e.ProjectionId ORDER BY e.Repository,e.RunId,e.RunAttempt LIMIT 10001`
	combined, cancel := context.WithCancel(ctx)
	//nolint:contextcheck // Independent snapshot revocation must cancel the caller-scoped subprocess.
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	var rows []Summary
	if err := s.archive.converter.query(combined, sql, &rows); err != nil {
		if accessErr := s.check(); accessErr != nil {
			return nil, accessErr
		}
		return nil, err
	}
	if len(rows) != len(members) {
		return nil, ErrIntegrity
	}
	for _, row := range rows {
		if !slices.ContainsFunc(members, func(m Member) bool {
			return m.Repository == row.Repository && m.RunID == row.RunID && m.Attempt == row.Attempt && m.Version == row.ProjectionID && m.Events == row.Events
		}) {
			return nil, ErrIntegrity
		}
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	return rows, nil
}
