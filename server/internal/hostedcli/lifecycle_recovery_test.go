package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestLifecycleBrokerRecovery(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		for name, run := range map[string]func(*testing.T, string){
			"receipt-past-expiry":         testBrokerReceiptSurvivesAuditOutagePastExpiry,
			"crash-after-revoke":          testBrokerCrashAfterRevokeBeforeReceiptPersistence,
			"lost-commit":                 testBrokerFailureAndLostCommitAreDeduplicated,
			"initial-commit":              testBrokerAmbiguousInitialOwnership,
			"audit-does-not-block-revoke": testAuditCannotBlockAnotherRevocation,
		} {
			t.Run(dialect+"/"+name, func(t *testing.T) { run(t, dialect) })
		}
	}
}

type pausedJournalAudit struct {
	*faultRevocations
	entered, release chan struct{}
	once             sync.Once
}

func (p *pausedJournalAudit) TransitionRevocation(ctx context.Context, id, expected, replacement, namespace string, events []operational.AuditEvent) (bool, error) {
	if len(events) > 0 {
		p.once.Do(func() { close(p.entered) })
		select {
		case <-p.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return p.faultRevocations.TransitionRevocation(ctx, id, expected, replacement, namespace, events)
}

type observedRevoker struct {
	*fakeMinter
	revocations chan struct{}
}

func (m observedRevoker) RevokeToken(ctx context.Context, token githubapp.ScopedToken) error {
	err := m.fakeMinter.RevokeToken(ctx, token)
	m.revocations <- struct{}{}
	return err
}

func testAuditCannotBlockAnotherRevocation(t *testing.T, dialect string) {
	journal, faults := testTokenJournalDialect(t, dialect)
	gate := &pausedJournalAudit{faultRevocations: faults, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	defer release()
	journal.queue = gate
	minter := observedRevoker{newFakeMinter(), make(chan struct{}, 4)}
	broker := NewBroker(minter, BrokerOptions{Revocations: journal})
	first, err := broker.Acquire(t.Context(), 42, testScope, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.Acquire(t.Context(), 42, testScope, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	broker.Release(t.Context(), first)
	<-minter.revocations
	maintained := make(chan error, 1)
	go func() { maintained <- broker.Maintain(t.Context()) }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not reach the audit append")
	}
	// A concurrent journal writer can wait on the audit transaction's database
	// lock while holding cleanupMutex. Even that must not delay remote revoke.
	broker.cleanupMutex.Lock()
	released := make(chan struct{})
	go func() {
		broker.Release(t.Context(), second)
		close(released)
	}()
	select {
	case <-minter.revocations:
		broker.cleanupMutex.Unlock()
	case <-time.After(time.Second):
		broker.cleanupMutex.Unlock()
		t.Fatal("another token's audit append blocked credential revocation")
	}
	release()
	<-released
	if err := <-maintained; err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func testBrokerReceiptSurvivesAuditOutagePastExpiry(t *testing.T, dialect string) {
	journal, faults := testTokenJournalDialect(t, dialect)
	store := faults.RevocationQueue.(operational.AuditLog)
	now := time.Now()
	minter := newFakeMinter()
	minter.now = func() time.Time { return now }
	broker := NewBroker(minter, BrokerOptions{Revocations: journal, Now: func() time.Time { return now }})
	ctx := withTokenActor(t.Context(), operational.CLICommand{ID: 17, CLICommandInvocation: operational.CLICommandInvocation{Actor: "mona", Repository: "octo/app"}})
	token, err := broker.Acquire(ctx, 42, testScope, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	faults.failComplete = true
	broker.Release(ctx, token)
	record, err := faults.PendingRevocation(t.Context(), journal.namespace)
	if err != nil || !strings.HasPrefix(record.Value, "{") || strings.Contains(record.Value, token.Secret()) || strings.Contains(record.Value, `"token":`) {
		t.Fatal("audit outage retained a credential instead of a safe receipt")
	}
	if minter.liveCount() != 0 || len(minter.revoked) != 1 {
		t.Fatal("audit outage blocked revocation")
	}
	if entry := broker.owned[token.Hashed()]; entry != nil && entry.token.Secret() != "" {
		t.Fatal("in-process receipt retained a credential")
	}
	now = now.Add(500 * 24 * time.Hour)
	journal.open = func(string) ([]byte, error) { return nil, errors.New("old encryption key unavailable") }
	restarted := NewBroker(minter, BrokerOptions{Revocations: journal, Now: func() time.Time { return now }})
	if err := restarted.Maintain(t.Context()); err == nil {
		t.Fatal("continued audit outage was silently ignored")
	}
	faults.failComplete = false
	if err := restarted.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAudit(t.Context(), operational.AuditFilter{Event: "token.revoked"})
	if err != nil || len(events) != 1 || events[0].Actor != "mona" || events[0].Attributes["repository"] != "octo/app" ||
		events[0].Attributes["command_id"] != "17" || events[0].Attributes["hashed_token"] != token.Hashed() ||
		events[0].Attributes["recovered"] != "false" {
		t.Fatal("receipt recovery lost original identity or manufactured a later revoke")
	}
	if len(minter.revoked) != 1 || minter.next != 1 {
		t.Fatal("receipt recovery repeated external effects")
	}
	if err := restarted.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(t.Context()); err != nil {
		t.Fatal("old process repeated a completed durable receipt", err)
	}
}

func testBrokerAmbiguousInitialOwnership(t *testing.T, dialect string) {
	journal, faults := testTokenJournalDialect(t, dialect)
	minter := newFakeMinter()
	token, err := minter.MintScopedToken(t.Context(), 42, testScope)
	if err != nil {
		t.Fatal(err)
	}
	entry := &ownedToken{token: token, record: tokenRecord(t.Context(), token), pending: true}
	faults.loseCommit = true
	if err := journal.retain(t.Context(), entry); err == nil || entry.version != "" {
		t.Fatal("ambiguous initial persistence was treated as confirmed ownership")
	}
	restarted := NewBroker(minter, BrokerOptions{Revocations: journal})
	if err := restarted.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The old process cannot infer that its first write committed. Its later
	// cleanup must not recreate semantic events already committed by recovery.
	old := NewBroker(minter, BrokerOptions{Revocations: journal})
	old.owned[token.Hashed()] = entry
	if err := old.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	events, err := faults.RevocationQueue.(operational.AuditLog).ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil || len(events) != 1 || events[0].Event != "token.revoked" {
		t.Fatal("ambiguous initial ownership duplicated cleanup evidence", err)
	}
	if err := restarted.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func testBrokerCrashAfterRevokeBeforeReceiptPersistence(t *testing.T, dialect string) {
	journal, faults := testTokenJournalDialect(t, dialect)
	minter := newFakeMinter()
	broker := NewBroker(minter, BrokerOptions{Revocations: journal})
	token, err := broker.Acquire(t.Context(), 42, testScope, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	faults.failWrite = true
	broker.Release(t.Context(), token)
	if minter.liveCount() != 0 || len(minter.revoked) != 1 {
		t.Fatal("journal failure delayed revocation")
	}
	faults.failWrite = false
	restarted := NewBroker(minter, BrokerOptions{Revocations: journal})
	if err := restarted.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	events, err := faults.RevocationQueue.(operational.AuditLog).ListAudit(t.Context(), operational.AuditFilter{Event: "token.revoked"})
	if err != nil || len(events) != 1 || events[0].Attributes["recovered"] != "true" ||
		events[0].Attributes["confirmation"] != "revoke_api_accepted_or_already_invalid" {
		t.Fatal("uncertain external result was not explicitly reconciled")
	}
	if len(minter.revoked) != 2 || minter.next != 1 {
		t.Fatal("cleanup recovery reissued a credential or failed to reconcile the uncertain result")
	}
	if err := restarted.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(t.Context()); !errors.Is(err, operational.ErrConflict) {
		t.Fatal("old owner overwrote recovered evidence")
	}
}

func testBrokerFailureAndLostCommitAreDeduplicated(t *testing.T, dialect string) {
	for _, receiptCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "receipt"}[receiptCommit], func(t *testing.T) {
			journal, faults := testTokenJournalDialect(t, dialect)
			minter := newFakeMinter()
			broker := NewBroker(minter, BrokerOptions{Revocations: journal})
			token, err := broker.Acquire(t.Context(), 42, testScope, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			minter.failRevoke = !receiptCommit
			faults.loseCommit = true
			broker.Release(t.Context(), token)
			restarted := NewBroker(minter, BrokerOptions{Revocations: journal})
			if !receiptCommit {
				if err := restarted.Maintain(t.Context()); err == nil {
					t.Fatal("continued revoke failure was hidden")
				}
			}
			minter.failRevoke = false
			if err := restarted.Maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := restarted.Maintain(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			store := faults.RevocationQueue.(operational.AuditLog)
			events, err := store.ListAudit(t.Context(), operational.AuditFilter{})
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, event := range events {
				counts[event.Event]++
			}
			if counts["token.revoked"] != 1 || (!receiptCommit && counts["token.revoke_failed"] != 1) {
				t.Fatal("uncertain journal commit duplicated or lost semantic events", counts)
			}
			data, _ := json.Marshal(events)
			if strings.Contains(string(data), token.Secret()) {
				t.Fatal("token leaked into lifecycle evidence")
			}
			if _, err := store.VerifyAudit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := broker.Close(t.Context()); !errors.Is(err, operational.ErrConflict) {
				t.Fatal("stale owner did not defer to its durable replacement")
			}
		})
	}
}
