package dashboardquery

import "context"

// CanonicalReader owns a consistent canonical read session. Storage adapters
// enforce selection and budgets before returning public canonical records.
type CanonicalReader interface {
	ReadCanonical(context.Context, CanonicalReadRequest) (CanonicalReadResult, error)
}

type SnapshotReader = CanonicalReader

type CanonicalReadRequest struct {
	Kind       string              `json:"kind"`
	Collection string              `json:"collection,omitempty"`
	Fields     []string            `json:"fields"`
	RecordIDs  []string            `json:"recordIds,omitempty"`
	Selections []EvidenceSelection `json:"selections,omitempty"`
	Watermark  string              `json:"watermark,omitempty"`
	MaxRows    int                 `json:"maxRows,omitempty"`
	MaxBytes   int64               `json:"maxBytes,omitempty"`
}

type CanonicalReadResult struct {
	Rows      []map[string]any `json:"rows"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
	Available bool             `json:"available"`
	Undated   int64            `json:"undated,omitempty"`
	Watermark string           `json:"watermark,omitempty"`
	Error     string           `json:"error,omitempty"`
}
