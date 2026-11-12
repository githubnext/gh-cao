package collect

import (
	"context"
	"path/filepath"

	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
)

func (p NativeValueService) resolvedNativeValuePolicy(ctx context.Context) ([]byte, error) {
	root, err := p.nativeLocalValueRoot()
	if err != nil {
		return nil, err
	}
	policy, err := filepath.Abs(p.PolicyPath)
	if err != nil {
		return nil, err
	}
	snapshot, err := operationalvalue.ResolveWorkflowPolicy(ctx,
		operationalvalue.Config{Root: root}, policy)
	if err != nil {
		return nil, err
	}
	return snapshot.Document, nil
}

func (p NativeValueService) nativeLocalValueRoot() (string, error) {
	root, err := filepath.Abs(p.CatalogRoot)
	if err != nil {
		return "", err
	}
	// Canonicalize aliases before resolving confined reviewed evidence files.
	return filepath.EvalSymlinks(root)
}
