package webhookgateway_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

type simulatorStorage struct {
	open func(*testing.T) (wg.Queue, func() error)
	jobs func(*testing.T) []wg.Job
}

func localSimulatorStorage(t *testing.T, options wg.QueueOptions) simulatorStorage {
	t.Helper()
	return postgresSimulatorStorage(t, simulatorPostgresDSN(t), options)
}

func simulatorPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		if os.Getenv("CAO_ACTIONS_SIMULATOR_POSTGRES") == "1" {
			t.Fatal("POSTGRES_URL is required for the PostgreSQL gateway simulator")
		}
		t.Skip("set POSTGRES_URL to a disposable local PostgreSQL database")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL gateway simulator configuration")
	}
	hosts := make([]string, 0, 1+len(config.Fallbacks))
	hosts = append(hosts, config.Host)
	for _, fallback := range config.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		if !strings.HasPrefix(host, "/") && !net.ParseIP(host).IsLoopback() {
			t.Fatal("gateway simulator requires numeric loopback or a local Unix socket")
		}
	}
	// Namespace isolation does not isolate migration locks from other packages'
	// runtime writes. Give each simulator suite its own physical schema.
	schema := "gateway_simulator_" + strings.ToLower(rand.Text())
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open PostgreSQL simulator schema owner")
	}
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close()
		t.Fatal("create PostgreSQL simulator schema")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("remove PostgreSQL simulator schema")
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid PostgreSQL simulator schema configuration")
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	return dsn
}

func postgresSimulatorStorage(t *testing.T, dsn string, options wg.QueueOptions) simulatorStorage {
	t.Helper()
	namespace := "gateway-simulator-" + strings.ToLower(rand.Text())
	store, err := postgres.New(t.Context(), dsn, namespace, postgres.Config{})
	if err != nil {
		t.Fatal("provision PostgreSQL gateway simulator storage")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		store, err := postgres.Open(ctx, dsn, namespace, postgres.Config{})
		if err != nil {
			t.Error("open PostgreSQL simulator namespace for cleanup")
			return
		}
		if err := store.DeleteNamespace(ctx); err != nil {
			t.Error("remove PostgreSQL simulator namespace")
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return simulatorStorage{
		open: func(t *testing.T) (wg.Queue, func() error) {
			t.Helper()
			store, err := postgres.Open(t.Context(), dsn, namespace, postgres.Config{})
			if err != nil {
				t.Fatal("reopen PostgreSQL gateway simulator storage")
			}
			queue, err := store.OpenWebhookQueue(t.Context(), "github-simulator", options)
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			return queue, store.Close
		},
		jobs: func(t *testing.T) []wg.Job {
			t.Helper()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal("open PostgreSQL simulator observation connection")
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			return readSimulatorJobs(t, db, namespace, `SELECT document,state,attempt,accepted_status,reason
				FROM cao_operational_webhook_jobs WHERE namespace=$1 AND queue='github-simulator' ORDER BY id`)
		},
	}
}

func readSimulatorJobs(t *testing.T, db *sql.DB, namespace, query string) []wg.Job {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query, namespace)
	if err != nil {
		t.Fatal("read durable simulator jobs", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var jobs []wg.Job
	for rows.Next() {
		var data []byte
		var job wg.Job
		var state wg.JobState
		var attempt uint32
		var accepted int
		var reason string
		if err := rows.Scan(&data, &state, &attempt, &accepted, &reason); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
		job.State, job.Attempt, job.AcceptedStatus, job.Reason = state, attempt, accepted, reason
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jobs
}
