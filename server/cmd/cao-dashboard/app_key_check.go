package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
)

var appKeyCheckLog = debuglogger.New("cao:dashboard:app-key-check")

func newAppKeyCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app-key-check",
		Short: "validate a local GitHub App key and rollover fingerprints offline; never contacts GitHub",
		Args:  cobra.NoArgs,
	}
	path := cmd.Flags().String("key-file", "", "explicit private-key PEM file; never read from environment defaults")
	expected := cmd.Flags().String("expected-public-key-sha256", "", "expected lowercase SHA-256 hex of DER SubjectPublicKeyInfo")
	previous := cmd.Flags().String("previous-public-key-sha256", "", "previous public-key fingerprint; fail if the replacement is unchanged")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if err := validateAppKeyCheckFlags(cmd, *path); err != nil {
			return err
		}
		result, err := checkAppKeyFile(*path, *expected, *previous)
		if err != nil {
			return err
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	return cmd
}

// validateAppKeyCheckFlags requires an explicit key file and rejects a
// fingerprint flag that was set to an empty value, which would otherwise
// silently skip the pin it was meant to enforce.
func validateAppKeyCheckFlags(cmd *cobra.Command, path string) error {
	if path == "" {
		appKeyCheckLog.Printf("app key check rejected: reason=missing_key_file")
		return errors.New("app key check requires --key-file")
	}
	for _, name := range []string{"expected-public-key-sha256", "previous-public-key-sha256"} {
		if cmd.Flags().Changed(name) && cmd.Flags().Lookup(name).Value.String() == "" {
			appKeyCheckLog.Printf("app key check rejected: reason=empty_fingerprint flag=%s", name)
			return errors.New("app key fingerprint checks must not be empty when explicitly requested")
		}
	}
	return nil
}

// checkAppKeyFile reads the bounded key file, validates it, and wipes the key
// material before returning only public metadata.
func checkAppKeyFile(path, expected, previous string) (githubapp.KeyCheck, error) {
	content, err := readAppKeyCheckFile(path)
	if err != nil {
		appKeyCheckLog.Printf("app key file rejected")
		return githubapp.KeyCheck{}, err
	}
	defer clear(content)
	result, err := githubapp.CheckPrivateKey(content, expected, previous)
	if err != nil {
		appKeyCheckLog.Printf("app key validation failed: expected_pinned=%t rotation_checked=%t", expected != "", previous != "")
		return githubapp.KeyCheck{}, err
	}
	appKeyCheckLog.Printf("app key validated: bits=%d expected_checked=%t rotation_checked=%t", result.Bits, result.ExpectedKeyChecked, result.RotationKeyChecked)
	return result, nil
}

func readAppKeyCheckFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > githubapp.MaxPrivateKeyBytes {
		return nil, errors.New("app key check requires a readable regular file of at most 64 KiB")
	}
	file, err := os.Open(path) // #nosec G304 G703 -- explicit operator-selected offline credential file, never request input.
	if err != nil {
		return nil, errors.New("app key check file is unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("app key check file must remain a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, githubapp.MaxPrivateKeyBytes+1))
	if err != nil || len(content) == 0 || len(content) > githubapp.MaxPrivateKeyBytes {
		clear(content)
		return nil, errors.New("app key check file is unreadable, empty, or exceeds 64 KiB")
	}
	return content, nil
}
