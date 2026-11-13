package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var compiledContracts sync.Map

type bicepContractKey struct {
	deployment string
	executable string
}

// compileContract reconstructs a profile's Bicep sources from Go and compiles ARM JSON with
// the official Bicep CLI. Results are memoized per profile because the inputs
// are immutable for the process lifetime.
func compileContract(ctx context.Context, features Features) (template []byte, err error) {
	return (BicepCLI{}).contract(ctx, features)
}

func (b BicepCLI) contract(ctx context.Context, features Features) (template []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := bicepContractKey{features.Deployment, b.Executable}
	if cached, exists := compiledContracts.Load(key); exists {
		return cached.([]byte), nil
	}
	files, err := deploymentFiles(features)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp("", "cao-topology-contract-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(stage, name), content, 0o600); err != nil {
			return nil, err
		}
	}
	template, err = b.compile(ctx, stage, "main.bicep")
	if err != nil {
		return nil, fmt.Errorf("compile %s Azure parameter contract: %w", features.Deployment, err)
	}
	if !json.Valid(template) {
		return nil, fmt.Errorf("bicep compiler returned invalid ARM JSON for %s", features.Deployment)
	}
	compiledContracts.Store(key, template)
	return template, nil
}

// BicepValidator validates emitted templates without deploying resources.
type BicepValidator interface {
	Validate(context.Context, string, []string) error
}

// BicepCLI uses Microsoft's standalone compiler, including its type checker and linter.
type BicepCLI struct {
	Diagnostics io.Writer
	// Executable optionally selects an explicit absolute compiler path.
	// By default the checksum-pinned compiler is acquired into a private cache.
	Executable string
}

func (b BicepCLI) executable(ctx context.Context) (string, error) {
	if b.Executable != "" {
		if !filepath.IsAbs(b.Executable) {
			return "", fmt.Errorf("Bicep compiler path must be absolute")
		}
		return b.Executable, nil
	}
	return managedBicepCompiler(ctx)
}

func (b BicepCLI) compile(ctx context.Context, directory, name string) ([]byte, error) {
	if !fs.ValidPath(name) || !strings.HasSuffix(name, ".bicep") {
		return nil, fmt.Errorf("invalid Bicep template path: %s", name)
	}
	compiler, err := b.executable(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var diagnostics bytes.Buffer
	command := exec.CommandContext(ctx, compiler, "build", filepath.Join(directory, filepath.FromSlash(name)), "--no-restore", "--stdout")
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("bicep validation failed for %s: %w\n%s", name, errors.Join(err, ctx.Err()), strings.TrimSpace(diagnostics.String()))
	}
	writer := b.Diagnostics
	if writer == nil {
		writer = os.Stderr
	}
	if _, err := diagnostics.WriteTo(writer); err != nil {
		return nil, fmt.Errorf("write Bicep diagnostics: %w", err)
	}
	return output, nil
}

func (b BicepCLI) Validate(ctx context.Context, directory string, templates []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(templates) == 0 {
		return fmt.Errorf("bicep validation requires at least one template")
	}
	compiler, err := b.executable(ctx)
	if err != nil {
		return err
	}
	b.Executable = compiler
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, name := range templates {
		if _, err := b.compile(ctx, directory, name); err != nil {
			return err
		}
	}
	return nil
}
