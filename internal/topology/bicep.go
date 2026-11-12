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

// compileContract compiles a profile's embedded Bicep sources to ARM JSON with
// the official Bicep CLI. Results are memoized per profile because the inputs
// are embedded and immutable for the process lifetime.
func compileContract(ctx context.Context, features Features) (template []byte, err error) {
	if cached, exists := compiledContracts.Load(features.Deployment); exists {
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
	compiler, err := exec.LookPath("bicep")
	if err != nil {
		return nil, fmt.Errorf("compiling the %s Azure parameter contract requires Microsoft's official Bicep CLI on PATH: %w; install it from https://learn.microsoft.com/azure/azure-resource-manager/bicep/install", features.Deployment, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var diagnostics bytes.Buffer
	command := exec.CommandContext(ctx, compiler, "build", filepath.Join(stage, "main.bicep"), "--no-restore", "--stdout")
	command.Stderr = &diagnostics
	template, err = command.Output()
	if err != nil {
		return nil, fmt.Errorf("compile %s Azure parameter contract: %w\n%s", features.Deployment, errors.Join(err, ctx.Err()), strings.TrimSpace(diagnostics.String()))
	}
	if !json.Valid(template) {
		return nil, fmt.Errorf("bicep compiler returned invalid ARM JSON for %s", features.Deployment)
	}
	compiledContracts.Store(features.Deployment, template)
	return template, nil
}

// BicepValidator validates emitted templates without deploying resources.
type BicepValidator interface {
	Validate(context.Context, string, []string) error
}

// BicepCLI uses Microsoft's standalone compiler, including its type checker and linter.
type BicepCLI struct {
	Diagnostics io.Writer
}

func (b BicepCLI) Validate(ctx context.Context, directory string, templates []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(templates) == 0 {
		return fmt.Errorf("bicep validation requires at least one template")
	}
	compiler, err := exec.LookPath("bicep")
	if err != nil {
		return fmt.Errorf("bicep validation requires Microsoft's official Bicep CLI on PATH: %w; install it from https://learn.microsoft.com/azure/azure-resource-manager/bicep/install", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output := b.Diagnostics
	if output == nil {
		output = os.Stderr
	}
	for _, name := range templates {
		if !fs.ValidPath(name) || !strings.HasSuffix(name, ".bicep") {
			return fmt.Errorf("invalid Bicep template path: %s", name)
		}
		var diagnostics bytes.Buffer
		command := exec.CommandContext(ctx, compiler, "build", filepath.Join(directory, filepath.FromSlash(name)), "--no-restore", "--stdout")
		command.Stdout = io.Discard
		command.Stderr = &diagnostics
		if err := command.Run(); err != nil {
			return fmt.Errorf("bicep validation failed for %s: %w\n%s", name, errors.Join(err, ctx.Err()), strings.TrimSpace(diagnostics.String()))
		}
		if _, err := diagnostics.WriteTo(output); err != nil {
			return fmt.Errorf("write Bicep diagnostics: %w", err)
		}
	}
	return nil
}
