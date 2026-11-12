package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/testutil"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

func localEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CAO_GATEWAY_RECEIVE_KEY_FILE", "CAO_GATEWAY_DISPATCH_KEY_FILE",
		"CAO_GATEWAY_WEBHOOK_SECRET", "CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET", "GH_TOKEN"} {
		t.Setenv(name, "")
	}
}

func localPostgres(t *testing.T) (string, string) {
	t.Helper()
	_, dsn, namespace := testutil.OperationalPostgres(t)
	t.Setenv("CAO_POSTGRES_URL", dsn)
	return dsn, namespace
}

func TestSpecLocalHost(t *testing.T) {
	localEnvironment(t)
	t.Run("LG-H01", func(t *testing.T) {
		for _, args := range [][]string{
			{"--role=receive", "--namespace=forbidden"},
			{"--role=dispatch", "--once=false"},
			{"--role=receive", "--events=issues,issue_comment,pull_request"},
			{"--role=local", "--policy=/etc/cao/cao.json"},
			{"--role=local", "--listen=127.0.0.1:8081"},
		} {
			if err := run(t.Context(), args); err == nil || !strings.Contains(err.Error(), "options") {
				t.Fatal("production/local options were combined", err)
			}
		}
		err := runLocal(t.Context(), localOptions{interval: time.Minute}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "namespace") {
			t.Fatal("missing PostgreSQL namespace accepted", err)
		}
	})
	t.Run("LG-H02", func(t *testing.T) {
		for _, name := range []string{"CAO_GATEWAY_RECEIVE_KEY_FILE", "CAO_GATEWAY_DISPATCH_KEY_FILE",
			"CAO_GATEWAY_WEBHOOK_SECRET", "CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET"} {
			t.Setenv(name, "production-credential-must-not-be-used")
			if err := validateRoleCredentials("local"); err == nil || strings.Contains(err.Error(), "must-not-be-used") {
				t.Fatal("local credential isolation failed", err)
			}
			t.Setenv(name, "")
		}
	})
	t.Run("LG-H03", func(t *testing.T) {
		_, namespace := localPostgres(t)
		server := localFixtureServer(t, nil, nil)
		options := localOptions{apiURL: server.URL, repository: "octo/repo", events: []string{"issues"},
			namespace: namespace, interval: time.Minute, once: true}
		var output bytes.Buffer
		for range 2 {
			if err := runLocal(t.Context(), options, &output); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Count(output.String(), "succeeded=1") != 2 || !strings.Contains(output.String(), "LOCAL SIMULATION") ||
			!strings.Contains(output.String(), "not remote workflow execution") {
			t.Fatal("local acceptance was not labeled or restart duplicated work", output.String())
		}
	})
	t.Run("LG-H04", func(t *testing.T) {
		_, namespace := localPostgres(t)
		observed := make(chan struct{}, 1)
		server := localFixtureServer(t, observed, nil)
		options := localOptions{apiURL: server.URL, repository: "octo/repo", events: []string{"issues"},
			namespace: namespace, interval: time.Minute}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		finished := make(chan error, 1)
		go func() { finished <- runLocal(ctx, options, &bytes.Buffer{}) }()
		select {
		case <-observed:
		case <-time.After(5 * time.Second):
			t.Fatal("local polling did not start")
		}
		cancel()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("local host ignored cancellation", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("local host left polling or storage work running")
		}
		// Reopening the same database exercises the joined close/reopen path.
		options.once = true
		if err := runLocal(t.Context(), options, &bytes.Buffer{}); err != nil && !strings.Contains(err.Error(), "incomplete") {
			t.Fatal("local database unavailable after shutdown", err)
		}
	})
}

func localFixtureServer(t *testing.T, observed chan<- struct{}, eventID *atomic.Int32) *httptest.Server {
	t.Helper()
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Error("local host attempted remote mutation")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body string
		switch r.URL.Path {
		case "/repos/octo/repo":
			body = `{"id":42,"full_name":"octo/repo","owner":{"id":7,"type":"Organization"}}`
		case "/repos/octo/repo/events":
			id := int32(1)
			if eventID != nil {
				id = eventID.Load()
			}
			body = fmt.Sprintf(`[{"id":"%d","type":"IssuesEvent","repo":{"id":42},"payload":{"action":"opened"}}]`, id)
			if observed != nil {
				select {
				case observed <- struct{}{}:
				default:
				}
			}
		default:
			t.Error("local host requested another repository")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if requests.Load() == 0 {
			t.Error("local host did not exercise GitHub reads")
		}
	})
	return server
}

func TestLocalRateBudgetBackpressure(t *testing.T) {
	localEnvironment(t)
	for _, once := range []bool{true, false} {
		t.Run(fmt.Sprintf("once=%t", once), func(t *testing.T) {
			dsn, namespace := localPostgres(t)
			var eventID atomic.Int32
			eventID.Store(1)
			server := localFixtureServer(t, nil, &eventID)
			options := localOptions{apiURL: server.URL, repository: "octo/repo", events: []string{"issues"},
				namespace: namespace, interval: time.Minute, once: true}
			if err := runLocal(t.Context(), options, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			// Freeze exhausted budgets until the test explicitly refills them.
			if _, err := db.ExecContext(t.Context(), `UPDATE cao_operational_records
			 SET value=convert_to(json_build_object('Tokens',0,'At',clock_timestamp()+interval '1 hour')::text,'UTF8'),
			 expires_at=clock_timestamp()+interval '2 hours' WHERE namespace=$1 AND kind='webhook-rate'`, namespace); err != nil {
				t.Fatal(err)
			}
			eventID.Store(2)
			if once {
				var output bytes.Buffer
				err := runLocal(t.Context(), options, &output)
				if err == nil || !strings.Contains(err.Error(), "simulation is incomplete") ||
					!strings.Contains(output.String(), "pending=1") {
					t.Fatal("one-shot budget exhaustion was not reported as incomplete", output.String(), err)
				}
				return
			}
			options.once = false
			ctx, cancel := context.WithCancel(t.Context())
			finished := make(chan error, 1)
			go func() { finished <- runLocal(ctx, options, &bytes.Buffer{}) }()
			defer func() {
				cancel()
				select {
				case err := <-finished:
					if !errors.Is(err, context.Canceled) {
						t.Error("continuous host did not stop on cancellation", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("continuous host left work running")
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var claimed int
				if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cao_operational_webhook_jobs WHERE namespace=$1 AND state='claimed'`, namespace).Scan(&claimed); err != nil {
					t.Fatal(err)
				}
				if claimed == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("budget-limited job was not claimed")
				}
				time.Sleep(10 * time.Millisecond)
			}
			select {
			case err := <-finished:
				finished <- err
				t.Fatal("normal budget exhaustion stopped the continuous host", err)
			case <-time.After(100 * time.Millisecond):
			}
			if _, err := db.ExecContext(t.Context(), `UPDATE cao_operational_records
			 SET value=convert_to(json_build_object('Tokens',300,'At',clock_timestamp())::text,'UTF8')
			 WHERE namespace=$1 AND kind='webhook-rate'`, namespace); err != nil {
				t.Fatal(err)
			}
			// Make the safely deferred claim recoverable without a minute-long sleep.
			if _, err := db.ExecContext(t.Context(), `UPDATE cao_operational_webhook_jobs
			 SET lease_until=clock_timestamp()-interval '1 second' WHERE namespace=$1 AND state='claimed'`, namespace); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for {
				var succeeded int
				if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cao_operational_webhook_jobs WHERE namespace=$1 AND state='succeeded'`, namespace).Scan(&succeeded); err != nil {
					t.Fatal(err)
				}
				if succeeded == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("continuous host did not resume after budget refill")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestLocalPollingFailureIsNotCancellation(t *testing.T) {
	localEnvironment(t)
	_, namespace := localPostgres(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octo/repo" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, err := fmt.Fprint(w, `{"id":42,"full_name":"octo/repo","owner":{"id":7,"type":"Organization"}}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runLocal(ctx, localOptions{apiURL: server.URL, repository: "octo/repo", events: []string{"issues"},
		namespace: namespace, interval: time.Minute}, &bytes.Buffer{})
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("internal shutdown concealed the polling failure", err)
	}
}

func TestLocalStartupRejectsInvalidPollingBeforeStorage(t *testing.T) {
	localEnvironment(t)
	t.Setenv("CAO_POSTGRES_URL", "must-not-connect")
	err := run(t.Context(), []string{"--role=local", "--namespace=not-provisioned", "--repository=octo/repo", "--poll-interval=0s"})
	if err == nil || !strings.Contains(err.Error(), "polling interval") {
		t.Fatal("unbounded polling interval accepted")
	}
}

func TestSummarizeLocalQueue(t *testing.T) {
	summary, pending := summarizeLocalQueue(map[wg.JobState]int64{
		wg.Succeeded: 3, wg.Ambiguous: 1, wg.Rejected: 2, wg.Failed: 4, wg.Expired: 5, wg.Cancelled: 6,
		wg.Ready: 7, wg.Claimed: 8, wg.Dispatching: 9,
	})
	const want = "Simulated queue: succeeded=3 ambiguous=1 rejected=2 failed=4 expired=5 cancelled=6 pending=24"
	if summary != want || pending != 24 {
		t.Fatalf("summary = %q pending = %d", summary, pending)
	}
}

func TestSummarizeLocalQueueEmpty(t *testing.T) {
	summary, pending := summarizeLocalQueue(nil)
	if pending != 0 || !strings.HasSuffix(summary, "cancelled=0 pending=0") {
		t.Fatalf("summary = %q pending = %d", summary, pending)
	}
}
