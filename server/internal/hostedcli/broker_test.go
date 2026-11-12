package hostedcli

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type rejectingMinter struct{ *fakeMinter }

func (m rejectingMinter) MintScopedToken(ctx context.Context, id int64, scope githubapp.TokenScope) (githubapp.ScopedToken, error) {
	token, err := m.fakeMinter.MintScopedToken(ctx, id, scope)
	return token, errors.Join(err, errors.New("grant rejected"))
}

type canceledMinter struct {
	*fakeMinter
	cancel context.CancelFunc
}

func (m canceledMinter) MintScopedToken(ctx context.Context, id int64, scope githubapp.TokenScope) (githubapp.ScopedToken, error) {
	token, err := m.fakeMinter.MintScopedToken(ctx, id, scope)
	m.cancel()
	return token, err
}

func (m canceledMinter) RevokeToken(ctx context.Context, token githubapp.ScopedToken) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return m.fakeMinter.RevokeToken(ctx, token)
}

func TestBrokerCanceledMintStillOwnsRevocation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	minter := canceledMinter{newFakeMinter(), cancel}
	broker := NewBroker(minter, BrokerOptions{})
	token, err := broker.Acquire(ctx, 1, testScope, time.Now().Add(time.Minute))
	if !errors.Is(err, context.Canceled) || token.Secret() != "" {
		t.Fatal("canceled mint must not hand out the received token")
	}
	if minter.liveCount() != 0 || len(minter.revoked) != 1 {
		t.Fatal("mint cancellation must not cancel token cleanup")
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type pausedMintAudit struct {
	failingAuditStore
	started, proceed chan struct{}
}

func (a pausedMintAudit) AppendAudit(_ context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
	if event.Event == "token.minted" {
		close(a.started)
		<-a.proceed
	}
	return event, nil
}

func TestBrokerRechecksHandoutAfterAudit(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "shutdown"}[shutdown], func(t *testing.T) {
			journal, _ := testTokenJournal(t)
			minter := newFakeMinter()
			store := pausedMintAudit{started: make(chan struct{}), proceed: make(chan struct{})}
			broker := NewBroker(minter, BrokerOptions{Revocations: journal, Audit: audit.New(io.Discard).WithStore(store)})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type acquired struct {
				token githubapp.ScopedToken
				err   error
			}
			result := make(chan acquired, 1)
			go func() {
				token, err := broker.Acquire(ctx, 1, testScope, time.Now().Add(time.Minute))
				result <- acquired{token, err}
			}()
			<-store.started
			var closed chan error
			expected := context.Canceled
			if shutdown {
				expected = ErrBrokerClosed
				closed = make(chan error, 1)
				go func() { closed <- broker.Close(t.Context()) }()
				for {
					broker.mutex.Lock()
					stopped := broker.closed
					broker.mutex.Unlock()
					if stopped {
						break
					}
					time.Sleep(time.Millisecond)
				}
			} else {
				cancel()
			}
			close(store.proceed)
			got := <-result
			if !errors.Is(got.err, expected) || got.token.Secret() != "" {
				t.Error("audit completion handed out a token after cancellation or shutdown")
			}
			if shutdown {
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			} else if err := broker.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if minter.liveCount() != 0 {
				t.Fatal("withheld token lost cleanup ownership")
			}
		})
	}
}

func TestBrokerRechecksUnsafeCleanupAfterAudit(t *testing.T) {
	journal, queue := testTokenJournal(t)
	minter := newFakeMinter()
	store := pausedMintAudit{started: make(chan struct{}), proceed: make(chan struct{})}
	broker := NewBroker(minter, BrokerOptions{Revocations: journal, Audit: audit.New(io.Discard).WithStore(store)})
	handout := make(chan bool, 1)
	go func() {
		token, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute))
		handout <- err == nil || token.Secret() != ""
	}()
	<-store.started
	queue.failWrite, minter.failRevoke = true, true
	other := githubapp.TokenScope{Repositories: []string{"other"}, Permissions: testScope.Permissions}
	_, err := broker.Acquire(t.Context(), 1, other, time.Now().Add(time.Minute))
	if err == nil || broker.Stats().PendingRevocations != 1 {
		t.Error("journal and revocation outage must retain unsafe cleanup debt")
	}
	close(store.proceed)
	if <-handout {
		t.Error("audit completion handed out a token despite new unsafe cleanup debt")
	}
	queue.failWrite, minter.failRevoke = false, false
	if err := broker.Close(t.Context()); err != nil || minter.liveCount() != 0 {
		t.Fatal("shutdown did not clean every withheld token")
	}
}

func TestBrokerCanceledAcquireDoesNotConsumePooledToken(t *testing.T) {
	minter := newFakeMinter()
	broker := NewBroker(minter, BrokerOptions{MaxPooled: 1})
	need := time.Now().Add(time.Minute)
	if err := broker.Prefetch(t.Context(), 1, testScope, need); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	token, err := broker.Acquire(ctx, 1, testScope, need)
	if !errors.Is(err, context.Canceled) || token.Secret() != "" || broker.Stats().Pooled != 1 {
		t.Error("canceled acquisition consumed a pooled token")
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type faultRevocations struct {
	operational.RevocationQueue
	failWrite, failComplete bool
	loseCommit              bool
}

func (q *faultRevocations) QueueRevocation(ctx context.Context, id, value, namespace string) error {
	if q.failWrite {
		return operational.ErrUnavailable
	}
	return q.RevocationQueue.QueueRevocation(ctx, id, value, namespace)
}

func (q *faultRevocations) CompleteRevocation(ctx context.Context, id, expected, namespace string) error {
	if q.failComplete {
		return operational.ErrUnavailable
	}
	return q.RevocationQueue.CompleteRevocation(ctx, id, expected, namespace)
}

func (q *faultRevocations) TransitionRevocation(ctx context.Context, id, expected, replacement, namespace string, events []operational.AuditEvent) (bool, error) {
	if q.failWrite || (q.failComplete && len(events) > 0) {
		return false, operational.ErrUnavailable
	}
	changed, err := q.RevocationQueue.(operational.RevocationTransitions).TransitionRevocation(ctx, id, expected, replacement, namespace, events)
	if err == nil && changed && q.loseCommit {
		q.loseCommit = false
		return false, operational.ErrUnavailable
	}
	return changed, err
}

func testTokenJournal(t *testing.T) (*Revocations, *faultRevocations) {
	t.Helper()
	return testTokenJournalDialect(t, "postgres")
}

func testTokenJournalDialect(t *testing.T, _ string) (*Revocations, *faultRevocations) {
	t.Helper()
	store, _, _ := testutil.OperationalPostgres(t)
	queue := &faultRevocations{RevocationQueue: store}
	block, err := aes.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		t.Fatal(err)
	}
	seal := func(plain []byte) (string, error) {
		return base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nil, plain, nil)), nil // #nosec G407 -- NewGCMWithRandomNonce generates and prepends a random nonce; it requires a nil nonce argument.
	}
	open := func(sealed string) ([]byte, error) {
		data, err := base64.RawURLEncoding.DecodeString(sealed)
		if err != nil {
			return nil, err
		}
		return aead.Open(nil, nil, data, nil)
	}
	return NewRevocations(queue, "test-hosted-cli", seal, open), queue
}

func TestBrokerExceptionalMintsRetainCleanupUntilRetrySucceeds(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		for _, prefetch := range []bool{false, true} {
			t.Run(map[bool]string{false: "audit-failure", true: "rejected-grant"}[rejected]+map[bool]string{false: "/acquire", true: "/prefetch"}[prefetch], func(t *testing.T) {
				journal, _ := testTokenJournal(t)
				minter := newFakeMinter()
				minter.failRevoke = true
				var mint Minter = minter
				if rejected {
					mint = rejectingMinter{minter}
				}
				broker := NewBroker(mint, BrokerOptions{MaxPooled: 2, Revocations: journal,
					Audit: audit.New(io.Discard).WithStore(failingAuditStore{event: "token.minted"})})
				t.Cleanup(func() { _ = broker.Close(context.Background()) })
				var err error
				if prefetch {
					err = broker.Prefetch(t.Context(), 1, testScope, time.Now().Add(time.Minute))
				} else {
					var token githubapp.ScopedToken
					token, err = broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute))
					if token.Secret() != "" {
						t.Fatal("failed mint escaped into execution")
					}
				}
				if err == nil || broker.Stats().Pooled != 0 || broker.Stats().PendingRevocations != 1 || minter.liveCount() != 1 {
					t.Fatal("failed mint must retain cleanup ownership without pooling or execution")
				}
				for range 3 {
					if err := broker.Maintain(t.Context()); err == nil || broker.Stats().PendingRevocations != 1 {
						t.Fatal("repeated failure lost cleanup debt or its error")
					}
				}
				minter.failRevoke = false
				if err := broker.Maintain(t.Context()); err != nil {
					t.Fatal(err)
				}
				if minter.liveCount() != 0 || broker.Stats().PendingRevocations != 0 {
					t.Fatal("recovered revoker did not retire cleanup")
				}
				if entry, err := journal.pending(t.Context()); err != nil || entry != nil {
					t.Fatal("confirmed revocation did not remove journal debt")
				}
			})
		}
	}
}

func TestBrokerJournalWriteFailureStopsIssuanceAndCanRecover(t *testing.T) {
	journal, queue := testTokenJournal(t)
	queue.failWrite = true
	minter := newFakeMinter()
	minter.failRevoke = true
	broker := NewBroker(minter, BrokerOptions{Revocations: journal})
	token, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute))
	if err == nil || token.Secret() != "" || broker.Stats().PendingRevocations != 1 {
		t.Fatal("journal failure must withhold the token and retain cleanup")
	}
	for range 3 {
		if token, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute)); err == nil || token.Secret() != "" {
			t.Fatal("unsafe cleanup debt must stop further token issuance")
		}
	}
	if minter.next != 1 {
		t.Fatal("unsafe cleanup debt permitted another mint")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := broker.Close(ctx); err == nil || broker.Stats().PendingRevocations != 1 {
		t.Fatal("canceled shutdown must report and retain unsafe cleanup")
	}
	queue.failWrite = false
	if err := broker.Close(t.Context()); err == nil {
		t.Fatal("durable but unrevoked shutdown debt must remain visible")
	}
	if entry, err := journal.pending(t.Context()); err != nil || entry == nil || entry.token.Secret() == "" {
		t.Fatal("shutdown did not hand cleanup to durable storage")
	}
	minter.failRevoke = false
	restarted := NewBroker(minter, BrokerOptions{Revocations: journal})
	if err := restarted.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if minter.liveCount() != 0 {
		t.Fatal("restart did not recover durable cleanup")
	}
	if err := restarted.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerUnsafeDebtBlocksPooledHandout(t *testing.T) {
	journal, queue := testTokenJournal(t)
	minter := newFakeMinter()
	broker := NewBroker(minter, BrokerOptions{MaxPooled: 2, Revocations: journal})
	need := time.Now().Add(time.Minute)
	if err := broker.Prefetch(t.Context(), 1, testScope, need); err != nil {
		t.Fatal(err)
	}
	queue.failWrite, minter.failRevoke = true, true
	other := githubapp.TokenScope{Repositories: []string{"other"}, Permissions: testScope.Permissions}
	if _, err := broker.Acquire(t.Context(), 1, other, need); err == nil {
		t.Fatal("unpersisted mint must be withheld")
	}
	token, err := broker.Acquire(t.Context(), 1, testScope, need)
	if err == nil || token.Secret() != "" || broker.Stats().Pooled != 1 {
		t.Fatal("unsafe cleanup must block handout without discarding pooled ownership")
	}
	queue.failWrite, minter.failRevoke = false, false
	if err := broker.Close(t.Context()); err != nil || minter.liveCount() != 0 {
		t.Fatal("shutdown did not clean all retained tokens")
	}
}

func TestBrokerAuditFailureWithoutJournalRetries(t *testing.T) {
	minter := newFakeMinter()
	minter.failRevoke = true
	broker := NewBroker(minter, BrokerOptions{
		Audit: audit.New(io.Discard).WithStore(failingAuditStore{event: "token.minted"}),
	})
	if _, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute)); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatal("unaudited mint must fail closed")
	}
	if err := broker.Maintain(t.Context()); err == nil || broker.Stats().PendingRevocations != 1 {
		t.Fatal("maintenance lost the failed mint's cleanup owner")
	}
	minter.failRevoke = false
	if err := broker.Maintain(t.Context()); err != nil || minter.liveCount() != 0 || broker.Stats().PendingRevocations != 0 {
		t.Fatal("maintenance did not retry the failed mint's revocation")
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerShutdownDuringDualOutageEndsAtExpiry(t *testing.T) {
	journal, queue := testTokenJournal(t)
	queue.failWrite = true
	minter := newFakeMinter()
	minter.failRevoke = true
	minter.ttl = 500 * time.Millisecond
	broker := NewBroker(minter, BrokerOptions{Revocations: journal})
	if _, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("dual outage must fail acquisition")
	}
	if broker.Stats().PendingRevocations != 1 {
		t.Fatal("shutdown regression requires live unpersisted cleanup debt")
	}
	var expiry time.Time
	broker.mutex.Lock()
	for _, entry := range broker.owned {
		expiry = entry.token.ExpiresAt
	}
	broker.mutex.Unlock()
	started := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := broker.Close(ctx); err == nil {
		t.Fatal("shutdown must report evidence that cannot yet be persisted")
	}
	if broker.Stats().PendingRevocations != 1 || time.Now().Before(expiry) || time.Since(started) > 4*time.Second {
		t.Fatal("shutdown exceeded expiry plus its one-second retry interval")
	}
	queue.failWrite = false
	if err := broker.Close(ctx); err != nil || broker.Stats().PendingRevocations != 0 {
		t.Fatal("expiry evidence did not recover after the journal became available")
	}
}

func TestBrokerRejectsExcessiveLifetimeAndBoundsCleanup(t *testing.T) {
	now := time.Now()
	minter := newFakeMinter()
	minter.now = func() time.Time { return now }
	minter.ttl, minter.failRevoke = 2*time.Hour, true
	broker := NewBroker(minter, BrokerOptions{Now: func() time.Time { return now }})
	token, err := broker.Acquire(t.Context(), 1, testScope, now.Add(time.Minute))
	if err == nil || token.Secret() != "" || broker.Stats().PendingRevocations != 1 {
		t.Fatal("excessive lifetime must reject use and preserve cleanup")
	}
	now = now.Add(githubapp.ScopedTokenLifetime)
	if err := broker.Close(t.Context()); err != nil || broker.Stats().PendingRevocations != 0 {
		t.Fatal("invalid expiry evidence must not extend cleanup beyond GitHub's one-hour lifetime")
	}
}

func TestBrokerRecoveryAndExpiryKeepActiveTokensOwned(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoke", true: "expiry"}[expired], func(t *testing.T) {
			journal, _ := testTokenJournal(t)
			now := time.Now()
			minter := newFakeMinter()
			minter.now = func() time.Time { return now }
			broker := NewBroker(minter, BrokerOptions{Revocations: journal, Now: func() time.Time { return now }})
			token, err := broker.Acquire(t.Context(), 1, testScope, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if err := broker.Maintain(t.Context()); err != nil || minter.liveCount() != 1 {
				t.Fatal("maintenance revoked a locally owned active token")
			}
			minter.failRevoke = true
			if err := broker.Close(t.Context()); err == nil {
				t.Fatal("shutdown revocation failure must be reported")
			}
			if expired {
				now = token.ExpiresAt.Add(time.Second)
			} else {
				minter.failRevoke = false
			}
			restarted := NewBroker(minter, BrokerOptions{Revocations: journal, Now: func() time.Time { return now }})
			if err := restarted.Maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
			if entry, err := journal.pending(t.Context()); err != nil || entry != nil {
				t.Fatal("recovery did not retire confirmed revocation or expiry")
			}
			if (expired && len(minter.revoked) != 0) || (!expired && minter.liveCount() != 0) {
				t.Fatal("expiry/revocation handling was incorrect")
			}
			if err := restarted.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBrokerCompletionFailureRetriesWithoutRevokingAgain(t *testing.T) {
	journal, queue := testTokenJournal(t)
	minter := newFakeMinter()
	broker := NewBroker(minter, BrokerOptions{Revocations: journal})
	token, err := broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	queue.failComplete = true
	broker.Release(t.Context(), token)
	if entry, err := journal.pending(t.Context()); err != nil || entry == nil || !entry.revoked || minter.liveCount() != 0 {
		t.Fatal("completion must retain a credential-free journal receipt")
	}
	if err := broker.Maintain(t.Context()); err == nil {
		t.Fatal("completion failure must remain visible")
	}
	queue.failComplete = false
	if err := broker.Maintain(t.Context()); err != nil || len(minter.revoked) != 1 {
		t.Fatal("completion retry must not issue another revocation")
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type blockedMinter struct {
	*fakeMinter
	started, proceed chan struct{}
}

func (m blockedMinter) MintScopedToken(ctx context.Context, id int64, scope githubapp.TokenScope) (githubapp.ScopedToken, error) {
	close(m.started)
	<-m.proceed
	return m.fakeMinter.MintScopedToken(ctx, id, scope)
}

func TestBrokerCloseWaitsForMintCleanupOwnership(t *testing.T) {
	minter := blockedMinter{newFakeMinter(), make(chan struct{}), make(chan struct{})}
	broker := NewBroker(minter, BrokerOptions{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, _ = broker.Acquire(t.Context(), 1, testScope, time.Now().Add(time.Minute))
	}()
	<-minter.started
	closed := make(chan error, 1)
	go func() { closed <- broker.Close(t.Context()) }()
	// Synchronize on the closed gate, not on a timing assumption.
	for {
		broker.mutex.Lock()
		stopped := broker.closed
		broker.mutex.Unlock()
		if stopped {
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before the in-flight mint transferred ownership")
	default:
	}
	close(minter.proceed)
	workers.Wait()
	if err := <-closed; err != nil || minter.liveCount() != 0 || broker.Stats().PendingRevocations != 0 {
		t.Fatal("shutdown lost the in-flight token")
	}
}
