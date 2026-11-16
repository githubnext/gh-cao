package model

const SchemaVersion = 20

type Row map[string]any

type Metadata map[string]any

type Source struct {
	Source            string   `json:"source"`
	Rows              []Row    `json:"rows"`
	Metadata          Metadata `json:"metadata"`
	ContinuationToken string   `json:"continuationToken,omitempty"`
}

type Metrics struct {
	DurationMS          int64    `json:"durationMs"`
	Operations          int      `json:"operations"`
	OutputRows          int      `json:"outputRows"`
	PeakWorkingRows     int      `json:"peakWorkingRows"`
	RetainedRows        int      `json:"retainedRows"`
	PeakWorkingBytes    int64    `json:"peakWorkingBytes"`
	RetainedBytes       int64    `json:"retainedBytes"`
	DependencyDepth     int      `json:"dependencyDepth"`
	RateLimitCost       int      `json:"rateLimitCost"`
	QueryCount          int      `json:"queryCount"`
	UnionCount          int      `json:"unionCount"`
	JoinCount           int      `json:"joinCount"`
	FilterCount         int      `json:"filterCount"`
	ComputeCount        int      `json:"computeCount"`
	AggregateCount      int      `json:"aggregateCount"`
	AggregateValueCount int      `json:"aggregateValueCount"`
	TemporalSeriesCount int      `json:"temporalSeriesCount"`
	SelectCount         int      `json:"selectCount"`
	OrderByCount        int      `json:"orderByCount"`
	LimitCount          int      `json:"limitCount"`
	PushedDown          []string `json:"pushedDown"`
	FallbackOperations  []string `json:"fallbackOperations"`
}

type Diagnostics struct {
	SchemaVersion      int                 `json:"schemaVersion"`
	Counts             map[string]int      `json:"counts"`
	RelationshipErrors []string            `json:"relationshipErrors"`
	DuplicateRecordIDs map[string][]string `json:"duplicateRecordIds"`
}
