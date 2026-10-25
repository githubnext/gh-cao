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
	"TraceDatasets":         {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "DatasetId", "MemberCount", "DatasetDigest", "Transform", "Split", "TaskGroup", "Evaluator", "QualityLabel", "State", "MiningVersion", "OutcomeId", "Disposition", "LabelRuleVersion", "LabelCutoff"},
	"TraceEpisodes":         {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "CanonicalNamespace", "CanonicalRunId", "Repository", "AuditId", "Kind", "EpisodeId", "HopId", "ParentHopId", "ReferencedRunId", "ReferencedRepository", "ReferencedWorkflow", "ObservedAt", "Scope", "EvidenceState", "BindingVersion"},
	"TraceOutcomes":         {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "CanonicalNamespace", "CanonicalRunId", "Repository", "OutputId", "OutputUrl", "OutputType", "AuditId", "ProducingAttempt", "Status", "Signal", "ObjectState", "ObjectUpdatedAt", "ObservedAt", "AcquisitionSource", "Scope", "EvidenceState", "LabelRuleVersion"},
	"TraceMiningManifest":   {"Namespace", "RunId", "RunAttempt", "ProjectionId", "RowDigest", "ContractVersion", "SourceRevision", "CanonicalNamespace", "CanonicalRunId", "CanonicalRevision", "Repository", "TraceVersion", "EpisodeCount", "OutcomeCount", "State", "Gaps", "CapturedAt", "FreshUntil"},
}

//go:embed schema.gen.kql
var schema string

// Schema returns the operator-applied trace KQL; runtime never provisions ADX.
func Schema() string { return schema }
