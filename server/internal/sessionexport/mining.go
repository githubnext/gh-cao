package sessionexport

import (
	"context"

	"github.com/githubnext/gh-cao/server/internal/model"
)

// MiningSnapshot is a bounded, single-revision read of retained evidence for
// one exact run attempt. It contains no trace bodies or acquisition authority.
type MiningSnapshot struct {
	Namespace string
	Revision  int64
	Run       model.Row
	Audits    []model.Row
	Outputs   []model.Row
}

type MiningReader interface {
	ReadMining(context.Context, string, string, int64) (MiningSnapshot, error)
}
