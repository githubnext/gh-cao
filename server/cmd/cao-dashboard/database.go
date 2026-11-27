package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/server"
)

var databaseLog = debuglogger.New("cao:dashboard:database")

// Database commands take an explicit store and PostgreSQL namespace,
// never administrator credentials from runtime policy.
func newDatabaseCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "database", Short: "explicit PostgreSQL provisioning and maintenance"}
	cmd.AddCommand(newLocalDatabaseCommand())
	for _, action := range []string{"bootstrap", "maintain", "retain-audit"} {
		cmd.AddCommand(newDatabaseAction(action))
	}
	return cmd
}

func newDatabaseAction(action string) *cobra.Command {
	cmd := &cobra.Command{Use: action, Short: "run database " + action + " with separately supplied credentials", Args: cobra.NoArgs}
	storeKind := cmd.Flags().String("store", "", "canonical or operational (required)")
	namespace := cmd.Flags().String("namespace", "", "exact deployment namespace (required)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		scope := databaseScope{
			action: action, store: *storeKind, namespace: *namespace,
		}
		backend, err := scope.validate()
		if err != nil {
			return err
		}
		dsn, err := adminDatabaseURL(os.Getenv)
		if err != nil {
			return err
		}
		removed, err := runDatabaseAction(ctx, dsn, *storeKind, *namespace, action)
		if err != nil {
			return err
		}
		databaseLog.Printf("database action completed: action=%s store=%s backend=%s removed=%d", action, *storeKind, backend, removed)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"action": action, "store": *storeKind, "backend": backend, "audit_events_removed": removed})
	}
	return cmd
}

// adminDatabaseURL returns the separately supplied administrator endpoint. It
// is read only for explicit maintenance commands and never logged.
func adminDatabaseURL(getenv func(string) string) (string, error) {
	dsn := strings.TrimSpace(getenv("CAO_DATABASE_ADMIN_URL"))
	if dsn == "" {
		databaseLog.Printf("database administrator endpoint rejected: reason=missing")
		return "", errors.New("CAO_DATABASE_ADMIN_URL is required; do not mount it in runtime processes")
	}
	return dsn, nil
}

// databaseScope is the validated-before-I/O flag set of a database action.
type databaseScope struct {
	action, store, namespace string
}

// validate rejects unsupported store and maintenance scopes before I/O.
func (s databaseScope) validate() (string, error) {
	if s.store != "canonical" && s.store != "operational" {
		return "", errors.New("--store must be canonical or operational")
	}
	if strings.TrimSpace(s.namespace) == "" {
		return "", errors.New("--namespace is required")
	}
	if (s.action == "maintain" && s.store != "canonical") || (s.action == "retain-audit" && s.store != "operational") {
		return "", errors.New("maintain requires canonical storage; retain-audit requires operational storage")
	}
	databaseLog.Printf("database scope selected: action=%s store=%s backend=postgres", s.action, s.store)
	return "postgres", nil
}

func runDatabaseAction(ctx context.Context, dsn, kind, namespace, action string) (int64, error) {
	if kind == "canonical" {
		// New validates the immutable canonical contract, provisions the full
		// historical/upcoming window, and retires expired daily partitions.
		store, err := postgresx.NewWithNamespace(ctx, dsn, namespace)
		if err != nil {
			return 0, err
		}
		return 0, store.Close()
	}
	config, err := server.PostgresOperationalConfigFromEnv()
	if err != nil {
		return 0, err
	}
	open := postgres.Open
	if action == "bootstrap" {
		open = postgres.New
	}
	store, err := open(ctx, dsn, namespace, config)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()
	if action == "retain-audit" {
		return store.RetainAudit(ctx)
	}
	return 0, nil
}
