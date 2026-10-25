// Code generated from server/spec/trace.tsp. DO NOT EDIT.

package adxtrace

import _ "embed"

const ContractVersion = 1

var Tables = map[string][]string{
	"TraceEvents":           {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "Sequence", "Type", "EventId", "ParentId", "EpisodeId", "HopId", "ParentHopId", "ToolCallId", "SessionId", "Timestamp", "PartCount", "ContentDigest"},
	"TraceContentParts":     {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "Sequence", "Part", "Content"},
	"TraceManifest":         {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "Repository", "ArtifactDigest", "PolicyDigest", "EventCount", "PartCount", "ContentDigest", "State", "Gaps", "CapturedAt"},
	"TraceSampleFrames":     {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "Repository", "Day", "Cutoff", "Mode", "SeedDigest", "Algorithm", "Population", "Selected", "SourceGaps", "Excluded", "MembershipDigest", "State"},
	"TraceSampleMembership": {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "FrameId", "TraceId", "TraceVersion", "Probability", "Weight", "State"},
	"TraceDatasets":         {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "DatasetId", "MemberCount", "DatasetDigest", "Transform", "Split", "TaskGroup", "Evaluator", "QualityLabel", "State"},
}

//go:embed schema.gen.kql
var schema string

// Schema returns the operator-applied trace KQL; runtime never provisions ADX.
func Schema() string { return schema }
