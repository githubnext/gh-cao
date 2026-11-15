package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
)

func TestAppKeyCheckCommandOfflineRollover(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	content := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	// These must not select credentials, initialize telemetry, or cause calls.
	t.Setenv("CAO_HOSTED_CLI_PRIVATE_KEY", "sensitive-env-sentinel")
	t.Setenv("CAO_HOSTED_CLI_GITHUB_API_URL", "https://invalid.invalid")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://invalid.invalid")
	execute := func(args ...string) (githubapp.KeyCheck, string, error) {
		t.Helper()
		cmd := newRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(append([]string{"app-key-check"}, args...))
		err := cmd.Execute()
		var result githubapp.KeyCheck
		if err == nil {
			if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
				t.Fatal(decodeErr)
			}
		}
		return result, output.String(), err
	}
	result, output, err := execute("--key-file", path)
	if err != nil || len(result.PublicKeySHA256) != 64 || result.ExpectedKeyChecked || result.RotationKeyChecked {
		t.Fatalf("offline check: %+v, %v", result, err)
	}
	if strings.Contains(output, "PRIVATE") || strings.Contains(output, "sentinel") || strings.Contains(output, path) {
		t.Fatal("report must contain only public metadata")
	}
	pinned, _, err := execute("--key-file", path, "--expected-public-key-sha256", result.PublicKeySHA256,
		"--previous-public-key-sha256", strings.Repeat("0", 64))
	if err != nil || !pinned.ExpectedKeyChecked || !pinned.RotationKeyChecked {
		t.Fatalf("pinned rollover check: %+v, %v", pinned, err)
	}
	malformedPath := filepath.Join(t.TempDir(), "malformed.pem")
	if err := os.WriteFile(malformedPath, append([]byte("-----BEGIN RSA PRIVATE KEY-----\ninvalid!base64\n-----END RSA PRIVATE KEY-----\n"), content...), 0600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no implicit env key": {},
		"same key rollover":   {"--key-file", path, "--previous-public-key-sha256", result.PublicKeySHA256},
		"mismatch":            {"--key-file", path, "--expected-public-key-sha256", strings.Repeat("0", 64)},
		"empty pin":           {"--key-file", path, "--expected-public-key-sha256="},
		"empty previous":      {"--key-file", path, "--previous-public-key-sha256="},
		"missing file":        {"--key-file", filepath.Join(t.TempDir(), "sensitive-missing-key.pem")},
		"directory":           {"--key-file", t.TempDir()},
		"malformed prefix":    {"--key-file", malformedPath},
	} {
		t.Run(name, func(t *testing.T) {
			_, output, err := execute(args...)
			if err == nil || output != "" {
				t.Fatal("refusal must produce an error without a success report")
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("file error exposed the input")
			}
		})
	}
}

func TestAppKeyCheckRejectsOversizedOrEmptyFile(t *testing.T) {
	for _, size := range []int{0, githubapp.MaxPrivateKeyBytes + 1} {
		path := filepath.Join(t.TempDir(), "key.pem")
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAppKeyCheckFile(path); err == nil {
			t.Fatal("file bounds must fail closed")
		}
	}
}

func TestAppKeyCheckHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"app-key-check", "--key-file", "must-not-be-read"})
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatalf("canceled check must not read or emit a report: %v", err)
	}
}

func TestValidateAppKeyCheckFlags(t *testing.T) {
	for name, tc := range map[string]struct {
		args    []string
		path    string
		wantErr string
	}{
		"key file only":           {args: []string{"--key-file=k.pem"}, path: "k.pem"},
		"pinned fingerprints":     {args: []string{"--key-file=k.pem", "--expected-public-key-sha256=ab", "--previous-public-key-sha256=cd"}, path: "k.pem"},
		"missing key file":        {args: nil, path: "", wantErr: "requires --key-file"},
		"empty expected pin":      {args: []string{"--key-file=k.pem", "--expected-public-key-sha256="}, path: "k.pem", wantErr: "must not be empty"},
		"empty previous pin":      {args: []string{"--key-file=k.pem", "--previous-public-key-sha256="}, path: "k.pem", wantErr: "must not be empty"},
		"missing file beats pins": {args: []string{"--expected-public-key-sha256="}, path: "", wantErr: "requires --key-file"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := newAppKeyCheckCommand()
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			err := validateAppKeyCheckFlags(cmd, tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckAppKeyFile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	content := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	plain, err := checkAppKeyFile(path, "", "")
	if err != nil || plain.Bits != 2048 || len(plain.PublicKeySHA256) != 64 || plain.ExpectedKeyChecked || plain.RotationKeyChecked {
		t.Fatalf("plain check: %+v, %v", plain, err)
	}
	pinned, err := checkAppKeyFile(path, plain.PublicKeySHA256, strings.Repeat("0", 64))
	if err != nil || !pinned.ExpectedKeyChecked || !pinned.RotationKeyChecked {
		t.Fatalf("pinned check: %+v, %v", pinned, err)
	}
	if _, err := checkAppKeyFile(path, strings.Repeat("0", 64), ""); err == nil {
		t.Fatal("mismatched expected fingerprint must fail")
	}
	if _, err := checkAppKeyFile(path, "", plain.PublicKeySHA256); err == nil {
		t.Fatal("unchanged key must fail rollover check")
	}
	if _, err := checkAppKeyFile(filepath.Join(t.TempDir(), "missing.pem"), "", ""); err == nil {
		t.Fatal("missing file must fail")
	}
}
