package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type revocationTestStore interface {
	operational.RevocationQueue
	Close() error
}

type rejectedTokenMinter struct {
	revokeFails bool
	live        bool
}

func (m *rejectedTokenMinter) MintScopedToken(context.Context, int64, githubapp.TokenScope) (githubapp.ScopedToken, error) {
	m.live = true
	return githubapp.NewScopedToken("ghs_recovery_fixture", 9, time.Now().Add(time.Hour)), errors.New("grant rejected")
}

func (m *rejectedTokenMinter) RevokeToken(context.Context, githubapp.ScopedToken) error {
	if m.revokeFails {
		return errors.New("synthetic revocation failure")
	}
	m.live = false
	return nil
}

func TestHostedCLIRevocationsSurviveRestartAndKeyRotation(t *testing.T) {
	for _, backend := range []string{"postgres"} {
		t.Run(backend, func(t *testing.T) {
			var open func() (revocationTestStore, error)
			_, dsn, namespace := testutil.OperationalPostgres(t)
			open = func() (revocationTestStore, error) {
				return postgres.Open(t.Context(), dsn, namespace, postgres.Config{})
			}
			t.Setenv("CAO_SESSION_SECRET", strings.Repeat("a", 32))
			t.Setenv("CAO_SESSION_SECRET_PREVIOUS", "")
			first, err := open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.Close() }()
			journal, err := hostedCLIRevocationsFromEnv(first)
			if err != nil {
				t.Fatal(err)
			}
			minter := &rejectedTokenMinter{revokeFails: true}
			broker := hostedcli.NewBroker(minter, hostedcli.BrokerOptions{Revocations: journal})
			token, err := broker.Acquire(t.Context(), 9, githubapp.TokenScope{
				Repositories: []string{"api"}, Permissions: map[string]string{"contents": "read"},
			}, time.Now().Add(time.Minute))
			if err == nil || token.Secret() != "" || !minter.live {
				t.Fatal("rejected token must be retained but never usable")
			}
			if err := broker.Close(t.Context()); err == nil {
				t.Fatal("shutdown must report failed revocation")
			}
			record, err := first.PendingRevocation(t.Context(), hostedCLIRevocationNamespace)
			if err != nil || record.ID == "" || record.Value == "" || strings.Contains(record.Value, "ghs_recovery_fixture") {
				t.Fatal("cleanup must persist only encrypted credential material")
			}
			if record, err := first.PendingRevocation(t.Context(), "oauth"); err != nil || record.ID != "" {
				t.Fatal("hosted CLI cleanup must not enter the OAuth namespace")
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			second, err := open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = second.Close() }()
			t.Setenv("CAO_SESSION_SECRET", strings.Repeat("b", 32))
			// An unavailable rotation key must preserve debt, not discard it.
			journal, err = hostedCLIRevocationsFromEnv(second)
			if err != nil {
				t.Fatal(err)
			}
			withoutKey := hostedcli.NewBroker(minter, hostedcli.BrokerOptions{Revocations: journal})
			if err := withoutKey.Maintain(t.Context()); err == nil {
				t.Fatal("unavailable decryption key must fail visibly")
			}
			if err := withoutKey.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAO_SESSION_SECRET_PREVIOUS", strings.Repeat("a", 32))
			journal, err = hostedCLIRevocationsFromEnv(second)
			if err != nil {
				t.Fatal(err)
			}
			minter.revokeFails = false
			restarted := hostedcli.NewBroker(minter, hostedcli.BrokerOptions{Revocations: journal})
			if err := restarted.Maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
			if minter.live {
				t.Fatal("restart did not revoke the rejected grant")
			}
			if record, err := second.PendingRevocation(t.Context(), hostedCLIRevocationNamespace); err != nil || record.ID != "" {
				t.Fatal("confirmed revocation did not clear durable debt")
			}
			if err := restarted.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHostedCLIRevocationsRequireStableEncryption(t *testing.T) {
	t.Setenv("CAO_SESSION_SECRET", "")
	t.Setenv("CAO_SESSION_SECRET_PREVIOUS", "")
	if _, err := hostedCLIRevocationsFromEnv(&revocationQueueStub{}); err == nil {
		t.Fatal("missing encryption secret must fail closed")
	}
	t.Setenv("CAO_SESSION_SECRET", strings.Repeat("a", 32))
	if _, err := hostedCLIRevocationsFromEnv(nil); err == nil {
		t.Fatal("missing durable queue must fail closed")
	}
	t.Setenv("CAO_SESSION_SECRET_PREVIOUS", "too-short")
	if _, err := hostedCLIRevocationsFromEnv(&revocationQueueStub{}); err == nil {
		t.Fatal("invalid previous encryption secret must fail closed")
	}
}

type revocationQueueStub struct{ operational.RevocationQueue }
