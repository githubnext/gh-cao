package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
)

var inventoryLog = debuglogger.New("cao:dashboard:inventory")

// readBoundedInventory reads at most the inventory capacity and rejects larger input.
func readBoundedInventory(input io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(input, collectnative.InventoryMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > collectnative.InventoryMaxBytes {
		inventoryLog.Printf("native inventory rejected: reason=capacity limit_bytes=%d", collectnative.InventoryMaxBytes)
		return nil, collectnative.ErrCapacity
	}
	return content, nil
}

// decodeNativeInventory binds the exact reviewed bytes to the expected digest
// and validates the resulting snapshot.
func decodeNativeInventory(content []byte, digest string) (collectnative.InventorySnapshot, error) {
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

func readNativeInventory(input io.Reader, digest string) (collectnative.InventorySnapshot, error) {
	content, err := readBoundedInventory(input)
	if err != nil {
		return collectnative.InventorySnapshot{}, err
	}
	snapshot, err := decodeNativeInventory(content, digest)
	if err != nil {
		inventoryLog.Printf("native inventory rejected: reason=invalid_or_unreviewed bytes=%d", len(content))
		return collectnative.InventorySnapshot{}, err
	}
	inventoryLog.Printf("native inventory accepted: bytes=%d", len(content))
	return snapshot, nil
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
