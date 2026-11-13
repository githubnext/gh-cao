// Package pgtemplate clones per-test PostgreSQL databases from a template
// provisioned once per test process.
//
// Provisioning a canonical store creates hundreds of daily partitions, which
// costs seconds per test. Cloning a provisioned template costs a fraction of a
// second while still giving every test a private database. The package has no
// store dependency so the store's own package tests can use it.
//
// Clones live in separate databases, so packages tested concurrently against one
// server no longer serialize on the store's database-scoped advisory locks.
// Dashboard queries lock every daily partition, so concurrent package runs
// (go test -p N) need a larger max_locks_per_transaction, for example 512;
// CI runs Postgres packages with -p 1.
package pgtemplate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Schema is the search_path of every template and clone.
const Schema = "cao_test"

// Provision initializes the template database through production code. It
// receives a configuration whose search_path is Schema and must release every
// connection before returning.
type Provision func(context.Context, *pgx.ConnConfig) error

type template struct {
	name        string
	unsupported bool
}

var (
	mu        sync.Mutex
	templates = map[string]*template{}
	sequence  atomic.Uint64
)

// Clone returns a configuration for a private database cloned from this
// process's template for endpoint, provisioning the template on first use. It
// returns false when the configured role cannot create databases. The clone is
// dropped when the test ends.
func Clone(t testing.TB, parent context.Context, endpoint string, provision Provision) (*pgx.ConnConfig, bool) {
	t.Helper()
	admin, err := sql.Open("pgx", endpoint)
	if err != nil {
		t.Fatal("configure Postgres test connection")
	}
	t.Cleanup(func() { _ = admin.Close() })
	mu.Lock()
	defer mu.Unlock()
	current := templates[endpoint]
	if current == nil {
		current = provisionTemplate(t, parent, admin, endpoint, provision)
		templates[endpoint] = current
	}
	if current.unsupported {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("%s_%d", processPrefix, sequence.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{current.name}.Sanitize()); err != nil {
		t.Fatalf("clone Postgres test template %s: %v", current.name, err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("remove isolated Postgres test database %s: %v", name, err)
		}
	})
	return DatabaseConfig(t, endpoint, name), true
}

func provisionTemplate(t testing.TB, parent context.Context, admin *sql.DB, endpoint string, provision Provision) *template {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Minute)
	defer cancel()
	started := time.Now()
	removeStale(t, ctx, admin)
	name := fmt.Sprintf("%s_tmpl", processPrefix)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Logf("Postgres test role cannot create databases (%v); provisioning a private schema per test", err)
		return &template{unsupported: true}
	}
	config := DatabaseConfig(t, endpoint, name)
	connection, err := pgx.ConnectConfig(ctx, config.Copy())
	if err == nil {
		_, err = connection.Exec(ctx, "CREATE SCHEMA "+Schema)
		_ = connection.Close(ctx)
	}
	if err == nil {
		err = provision(ctx, config)
	}
	if err == nil {
		_, err = admin.ExecContext(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH IS_TEMPLATE true ALLOW_CONNECTIONS false")
	}
	if err != nil {
		drop(ctx, admin, name)
		t.Fatalf("provision Postgres test template: %v", err)
	}
	t.Logf("provisioned Postgres test template %s in %s", name, time.Since(started).Round(time.Millisecond))
	return &template{name: name}
}

// Database names identify the owning host and process so later processes can
// reclaim databases left by interrupted runs: cao_test_<host>_<pid>_<started>_….
var (
	hostTag       = hostHash()
	processPrefix = fmt.Sprintf("cao_test_%s_%d_%d", hostTag, os.Getpid(), time.Now().Unix())
)

func hostHash() string {
	host, _ := os.Hostname()
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(host))
	return fmt.Sprintf("%08x", digest.Sum32())
}

// removeStale drops templates and clones whose owning process on this host has
// exited, and any older than a day. Live processes' databases stay intact.
func removeStale(t testing.TB, ctx context.Context, admin *sql.DB) {
	t.Helper()
	for _, current := range staleDatabases(ctx, admin) {
		if current.template {
			if _, err := admin.ExecContext(ctx, "ALTER DATABASE "+pgx.Identifier{current.name}.Sanitize()+" WITH IS_TEMPLATE false"); err != nil {
				continue
			}
		}
		drop(ctx, admin, current.name)
		t.Logf("removed stale Postgres test database %s", current.name)
	}
}

type staleDatabase struct {
	name     string
	template bool
}

// staleDatabases lists removable test databases. Cleanup is best effort, so a
// failed listing removes nothing rather than acting on a partial result.
func staleDatabases(ctx context.Context, admin *sql.DB) []staleDatabase {
	rows, err := admin.QueryContext(ctx, `SELECT datname, datistemplate FROM pg_database WHERE datname LIKE 'cao\_test\_%'`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var stale []staleDatabase
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	for rows.Next() {
		var current staleDatabase
		if rows.Scan(&current.name, &current.template) != nil {
			return nil
		}
		var host string
		var pid int
		var started int64
		if _, err := fmt.Sscanf(strings.ReplaceAll(current.name, "_", " "), "cao test %s %d %d", &host, &pid, &started); err != nil {
			continue
		}
		if started < cutoff || (host == hostTag && pid != os.Getpid() && !processAlive(pid)) {
			stale = append(stale, current)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return stale
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Treat unknown outcomes, such as unsupported signals, as alive.
	err = process.Signal(syscall.Signal(0))
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}

func drop(ctx context.Context, admin *sql.DB, name string) {
	_, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// DatabaseConfig targets database name with search_path Schema while
// preserving every other endpoint setting, so ConnString identifies the clone
// for subprocesses and adapters.
func DatabaseConfig(t testing.TB, endpoint, name string) *pgx.ConnConfig {
	t.Helper()
	target := endpoint + " dbname='" + name + "'"
	if parsed, err := url.Parse(endpoint); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		parsed.Path = "/" + name
		parsed.RawPath = ""
		target = parsed.String()
	}
	config, err := pgx.ParseConfig(target)
	if err != nil {
		t.Fatal("parse Postgres test configuration")
	}
	config.RuntimeParams["search_path"] = Schema
	return config
}
