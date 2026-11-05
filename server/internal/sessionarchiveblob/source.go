package sessionarchiveblob

import (
	"context"
	"slices"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// RequireSourceFencing is persisted before a PG-authorized archive is exposed.
// An existing unscoped native archive cannot become enrolled by configuration:
// its evidence has no recoverable source epoch and must remain inaccessible.
func (n *Native) RequireSourceFencing(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.ready(); err != nil {
		return err
	}
	if err := n.load(ctx); err != nil {
		return err
	}
	if n.state.SourceFenced {
		return nil
	}
	if len(n.state.NativeRecords)+len(n.state.Pending)+len(n.state.Manifest.Members) > 0 || n.active.Generation > 0 {
		return ErrConformance
	}
	next := n.state
	next.SourceFenced = true
	return n.save(ctx, next, time.Time{})
}

// WithdrawSource is the idempotent Blob side of the PG withdrawal outbox.
// Its permanent source tombstone also fences an old admission that resumes
// after its PG connection was lost and the cleanup row was already consumed.
func (n *Native) WithdrawSource(ctx context.Context, source sessionarchive.Source) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.ready(); err != nil {
		return err
	}
	if source.Validate() != nil || !n.config.authorized(n.config.Namespace, source.Repository) {
		return ErrScope
	}
	if err := n.load(ctx); err != nil {
		n.failLease()
		return err
	}
	if !n.state.SourceFenced {
		return ErrConformance
	}
	if slices.Contains(n.state.WithdrawnSources, source) {
		return nil
	}
	if r := n.reader.Load(); r != nil && r.source != nil && *r.source == source {
		r.cancel()
	}
	records := slices.DeleteFunc(append([]nativeRecord{}, n.state.NativeRecords...), func(r nativeRecord) bool {
		return r.Source == source
	})
	selected := make(map[sessionarchive.Member]bool, len(records))
	for _, record := range records {
		if record.Gap == "" {
			selected[record.Member] = true
		}
	}
	next := n.state.Manifest
	next.Generation++
	next.CreatedAt = n.now().UTC()
	next.Members = slices.DeleteFunc(slices.Clone(next.Members), func(m sessionarchive.Member) bool { return !selected[m] })
	next.Files = slices.DeleteFunc(slices.Clone(next.Files), func(f sessionarchive.File) bool {
		return !slices.ContainsFunc(f.Members, func(m sessionarchive.Member) bool { return selected[m] })
	})
	if err := n.publish(ctx, next, func(context.Context, sessionarchive.File) error { return ErrIntegrity }, records, &source); err != nil {
		n.failLease()
		return err
	}
	return nil
}
