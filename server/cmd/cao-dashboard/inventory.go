package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func readNativeInventory(input io.Reader, digest string) (collectnative.InventorySnapshot, error) {
	content, err := io.ReadAll(io.LimitReader(input, collectnative.InventoryMaxBytes+1))
	if err != nil {
		return collectnative.InventorySnapshot{}, err
	}
	if len(content) > collectnative.InventoryMaxBytes {
		return collectnative.InventorySnapshot{}, collectnative.ErrCapacity
	}
	var provenance collectnative.InventoryProvenance
	if err := json.Unmarshal(content, &provenance); err != nil {
		return collectnative.InventorySnapshot{}, errors.New("invalid native inventory document")
	}
	snapshot, err := collectnative.NewInventorySnapshot(content, digest, provenance)
	if err != nil {
		return collectnative.InventorySnapshot{}, err
	}
	return snapshot, snapshot.Validate()
}

func newProvisionInventoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision-inventory",
		Short: "pin an explicitly reviewed native inventory in existing PostgreSQL storage",
		Args:  cobra.NoArgs,
	}
	source := cmd.Flags().String("source", "", "reviewed immutable inventory JSON file (required)")
	digest := cmd.Flags().String("sha256", "", "independently reviewed SHA-256 of the exact source bytes (required)")
	namespace := cmd.Flags().String("namespace", "", "exact canonical PostgreSQL namespace (required)")
	postgresURL := cmd.Flags().String("postgres-url", "", "PostgreSQL connection; defaults to CAO_POSTGRES_URL")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(*source) == "" || strings.TrimSpace(*namespace) == "" || *digest == "" {
			return errors.New("--source, --sha256 and --namespace are required")
		}
		file, err := os.Open(*source)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		snapshot, err := readNativeInventory(file, *digest)
		if err != nil {
			return err
		}
		store, err := newPostgresStore(cmd.Context(), *postgresURL, *namespace)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		if err := store.Acquisition().PinInventoryConfiguration(cmd.Context(), snapshot); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"inventoryDigest": snapshot.Digest})
	}
	return cmd
}
