package collect

import (
	"context"
	"path/filepath"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
)

func (p Projector) resolvedNativeValuePolicy(ctx context.Context) ([]byte, error) {
	root, err := filepath.Abs(p.CatalogRoot)
	if err != nil {
		return nil, err
	}
	policy, err := filepath.Abs(p.PolicyPath)
	if err != nil {
		return nil, err
	}
	snapshot, err := operationalvalue.ResolveWorkflowPolicy(ctx, operationalvalue.Config{
		Root: root, NodeBinary: p.node(), Environment: collectionEnvironment(""), WorkerTimeout: 30 * time.Second,
	}, policy)
	if err != nil {
		return nil, err
	}
	return snapshot.Document, nil
}
