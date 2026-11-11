package topology

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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
		return fmt.Errorf("Bicep validation requires at least one template")
	}
	compiler, err := exec.LookPath("bicep")
	if err != nil {
		return fmt.Errorf("Bicep validation requires Microsoft's official Bicep CLI on PATH: %w; install it from https://learn.microsoft.com/azure/azure-resource-manager/bicep/install", err)
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
			return fmt.Errorf("Bicep validation failed for %s: %w\n%s", name, errors.Join(err, ctx.Err()), strings.TrimSpace(diagnostics.String()))
		}
		if _, err := diagnostics.WriteTo(output); err != nil {
			return fmt.Errorf("write Bicep diagnostics: %w", err)
		}
	}
	return nil
}
