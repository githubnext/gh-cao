package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/spf13/cobra"

	grants "github.com/githubnext/gh-cao/server/postgres"
)

var localDatabasePassword = regexp.MustCompile(`^[a-f0-9]{64}$`)

func newLocalDatabaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "provision-local", Hidden: true, Args: cobra.NoArgs,
		Short: "provision the operator-owned local PostgreSQL container",
	}
	namespace := cmd.Flags().String("namespace", "", "exact local dashboard namespace")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return provisionLocalDatabase(cmd.Context(), os.Getenv("CAO_DATABASE_ADMIN_URL"),
			os.Getenv("CAO_LOCAL_POSTGRES_RUNTIME_PASSWORD"), *namespace)
	}
	return cmd
}

func provisionLocalDatabase(ctx context.Context, endpoint, password, namespace string) error {
	address, err := url.Parse(endpoint)
	if err != nil || address == nil || address.User == nil ||
		address.User.Username() != "cao_bootstrap" || address.Path != "/cao_dashboard" ||
		net.ParseIP(address.Hostname()) == nil || !net.ParseIP(address.Hostname()).IsLoopback() ||
		address.Scheme != "postgres" || !localDatabasePassword.MatchString(password) ||
		strings.TrimSpace(namespace) == "" {
		return errors.New("managed PostgreSQL provisioning requires its local administrator, database, password and namespace")
	}
	db, err := sql.Open("pgx", endpoint)
	if err != nil {
		return errors.New("cannot open the managed PostgreSQL administrator")
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, `
DO $roles$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='cao_owner') THEN
    CREATE ROLE cao_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    CREATE ROLE cao_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    CREATE ROLE cao_retention NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    CREATE ROLE cao_evidence NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
    CREATE ROLE cao_dashboard_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS IN ROLE cao_runtime;
  END IF;
END
$roles$;
CREATE SCHEMA IF NOT EXISTS cao AUTHORIZATION cao_owner;
REVOKE ALL ON DATABASE cao_dashboard FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE cao_dashboard TO cao_runtime;
GRANT TEMPORARY ON DATABASE cao_dashboard TO cao_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
`)
	if err != nil {
		return errors.New("cannot establish managed PostgreSQL role separation")
	}
	passwordTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("cannot start managed PostgreSQL credential configuration")
	}
	defer func() { _ = passwordTx.Rollback() }()
	if _, err := passwordTx.ExecContext(ctx, "SELECT set_config('cao.local_runtime_password', $1, true)", password); err != nil {
		return errors.New("cannot configure the managed PostgreSQL runtime credential")
	}
	if _, err := passwordTx.ExecContext(ctx, `DO $credential$
BEGIN
  EXECUTE format('ALTER ROLE cao_dashboard_runtime PASSWORD %L', current_setting('cao.local_runtime_password'));
END
$credential$`); err != nil {
		return errors.New("cannot configure the managed PostgreSQL runtime credential")
	}
	if err := passwordTx.Commit(); err != nil {
		return errors.New("cannot configure the managed PostgreSQL runtime credential")
	}
	parameters := address.Query()
	parameters.Set("role", "cao_owner")
	parameters.Set("search_path", "cao")
	address.RawQuery = parameters.Encode()
	for _, store := range []string{"canonical", "operational"} {
		if _, err := runDatabaseAction(ctx, address.String(), store, namespace, "bootstrap"); err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				return fmt.Errorf("cannot bootstrap managed PostgreSQL %s storage: %s (%s)", store, databaseError.Message, databaseError.Code)
			}
			return errors.New("cannot bootstrap managed PostgreSQL " + store + " storage")
		}
	}
	owner, err := sql.Open("pgx", address.String())
	if err != nil {
		return errors.New("cannot open the managed PostgreSQL schema owner")
	}
	defer func() { _ = owner.Close() }()
	replacer := strings.NewReplacer(`:"schema"`, `"cao"`, `:"runtime"`, `"cao_runtime"`,
		`:"retention"`, `"cao_retention"`, `:"reader"`, `"cao_evidence"`)
	for _, contract := range []string{grants.CanonicalGrants, grants.OperationalGrants} {
		if _, err := owner.ExecContext(ctx, replacer.Replace(contract)); err != nil {
			return errors.New("cannot apply managed PostgreSQL runtime privilege contracts")
		}
	}
	return nil
}
