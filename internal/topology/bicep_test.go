package topology

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type validatorFunc func(context.Context, string, []string) error

func (f validatorFunc) Validate(ctx context.Context, directory string, templates []string) error {
	return f(ctx, directory, templates)
}

func bundleTestValidator(t *testing.T) BicepValidator {
	t.Helper()
	// Bundle unit tests exercise the validation boundary, not a replacement compiler.
	return validatorFunc(func(ctx context.Context, directory string, templates []string) error {
		t.Helper()
		if err := ctx.Err(); err != nil {
			return err
		}
		emitted := []string{}
		if err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(name, ".bicep") {
				relative, err := filepath.Rel(directory, name)
				if err != nil {
					return err
				}
				emitted = append(emitted, filepath.ToSlash(relative))
			}
			return nil
		}); err != nil {
			return err
		}
		slices.Sort(emitted)
		if len(emitted) == 0 || !reflect.DeepEqual(emitted, templates) {
			t.Fatalf("validator missed emitted templates: got %v, want %v", templates, emitted)
		}
		return nil
	})
}

func TestGenerateRequiresSuccessfulBicepValidation(t *testing.T) {
	input := withHosts(t)
	content, err := yaml.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	output := filepath.Join(parent, "bundle")
	rejected := errors.New("BCP036: incompatible property type")
	calls := 0
	validator := validatorFunc(func(ctx context.Context, stage string, templates []string) error {
		calls++
		if stage == output {
			t.Fatal("published output before validation")
		}
		if err := bundleTestValidator(t).Validate(ctx, stage, templates); err != nil {
			return err
		}
		if !slices.Contains(templates, "optional/lake.bicep") || !slices.Contains(templates, "optional/host.bicep") {
			t.Fatal("optional infrastructure bypassed validation")
		}
		return rejected
	})
	if _, err := Generate(context.Background(), content, output, validator); !errors.Is(err, rejected) {
		t.Fatalf("expected compiler failure, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("validation calls: %d", calls)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("failed validation published output or left staging files: %v, %v", entries, err)
	}
	if _, err := Generate(context.Background(), content, output, nil); err == nil {
		t.Fatal("generation bypassed its required validator")
	}
	if _, err := Generate(context.Background(), content, output, validatorFunc(func(_ context.Context, stage string, _ []string) error {
		return os.WriteFile(filepath.Join(stage, "main.bicep"), []byte("modified by validator"), 0o600)
	})); err == nil {
		t.Fatal("published a bundle modified during validation")
	}
}

func installCompilerFixture(t *testing.T, body string) string {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "bicep"), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return bin
}

func TestBicepCLIValidationContract(t *testing.T) {
	directory := t.TempDir()
	log := filepath.Join(directory, "arguments")
	t.Setenv("BICEP_TEST_ARGUMENTS", log)
	installCompilerFixture(t, `printf '%s\n' "$@" >> "$BICEP_TEST_ARGUMENTS"
printf 'ARM output must not leak\n'
printf 'review this linter warning\n' >&2`)
	var diagnostics bytes.Buffer
	if err := (BicepCLI{Diagnostics: &diagnostics}).Validate(context.Background(), directory, []string{"main.bicep", "optional/host.bicep"}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	expected := "build\n" + filepath.Join(directory, "main.bicep") + "\n--no-restore\n--stdout\nbuild\n" + filepath.Join(directory, "optional/host.bicep") + "\n--no-restore\n--stdout\n"
	if string(arguments) != expected {
		t.Fatalf("unexpected official CLI invocation: %s", arguments)
	}
	if diagnostics.String() != strings.Repeat("review this linter warning\n", 2) {
		t.Fatalf("missing diagnostics or leaked ARM output: %s", &diagnostics)
	}
}

func TestBicepCLIRejectsMissingCompilerErrorsAndCancellation(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := (BicepCLI{}).Validate(context.Background(), t.TempDir(), []string{"main.bicep"})
		if err == nil || !strings.Contains(err.Error(), "official Bicep CLI on PATH") {
			t.Fatalf("missing compiler was not actionable: %v", err)
		}
	})
	t.Run("compiler-error", func(t *testing.T) {
		installCompilerFixture(t, "printf 'BCP036: incompatible property type\\n' >&2\nexit 1")
		err := (BicepCLI{}).Validate(context.Background(), t.TempDir(), []string{"optional/lake.bicep"})
		if err == nil || !strings.Contains(err.Error(), "BCP036") || !strings.Contains(err.Error(), "optional/lake.bicep") {
			t.Fatalf("lost compiler diagnostics: %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := (BicepCLI{}).Validate(ctx, t.TempDir(), []string{"main.bicep"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	})
}

func TestGeneratedValidationScriptFailsOnCompilerError(t *testing.T) {
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "bundle")
	if _, err := Generate(context.Background(), content, directory, bundleTestValidator(t)); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	bin := installCompilerFixture(t, "printf 'BCP036: rejected template\\n' >&2\nexit 9")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+originalPath)
	command := exec.Command("sh", filepath.Join(directory, "scripts", "validate.sh"))
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 9 || !bytes.Contains(output, []byte("BCP036")) {
		t.Fatalf("validation script hid compiler failure: %s: %v", output, err)
	}
}
