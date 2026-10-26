// Code generated from server/spec/trace.tsp. DO NOT EDIT.

package sessionarchive

const ContractVersion = 1

type Event struct {
	Sequence        int64    `json:"Sequence"`
	Type            string   `json:"Type"`
	EventID         string   `json:"EventId"`
	ParentID        string   `json:"ParentId"`
	EpisodeID       string   `json:"EpisodeId"`
	HopID           string   `json:"HopId"`
	ParentHopID     string   `json:"ParentHopId"`
	ToolCallID      string   `json:"ToolCallId"`
	SessionID       string   `json:"SessionId"`
	Timestamp       string   `json:"Timestamp"`
	ContentDigest   string   `json:"ContentDigest"`
	Namespace       string   `json:"Namespace"`
	RunID           string   `json:"RunId"`
	RunAttempt      int64    `json:"RunAttempt"`
	ProjectionID    string   `json:"ProjectionId"`
	ContractVersion int64    `json:"ContractVersion"`
	Repository      string   `json:"Repository"`
	TraceID         string   `json:"TraceId"`
	CompletionDay   string   `json:"CompletionDay"`
	ArtifactDigest  string   `json:"ArtifactDigest"`
	PolicyDigest    string   `json:"PolicyDigest"`
	Gaps            []string `json:"Gaps"`
	CapturedAt      string   `json:"CapturedAt"`
	PayloadJSON     string   `json:"PayloadJSON"`
}

var eventColumns = []string{
	"Sequence",
	"Type",
	"EventId",
	"ParentId",
	"EpisodeId",
	"HopId",
	"ParentHopId",
	"ToolCallId",
	"SessionId",
	"Timestamp",
	"ContentDigest",
	"Namespace",
	"RunId",
	"RunAttempt",
	"ProjectionId",
	"ContractVersion",
	"Repository",
	"TraceId",
	"CompletionDay",
	"ArtifactDigest",
	"PolicyDigest",
	"Gaps",
	"CapturedAt",
	"PayloadJSON",
}

const eventJSONColumns = "{'Sequence': 'BIGINT', 'Type': 'VARCHAR', 'EventId': 'VARCHAR', 'ParentId': 'VARCHAR', 'EpisodeId': 'VARCHAR', 'HopId': 'VARCHAR', 'ParentHopId': 'VARCHAR', 'ToolCallId': 'VARCHAR', 'SessionId': 'VARCHAR', 'Timestamp': 'VARCHAR', 'ContentDigest': 'VARCHAR', 'Namespace': 'VARCHAR', 'RunId': 'VARCHAR', 'RunAttempt': 'BIGINT', 'ProjectionId': 'VARCHAR', 'ContractVersion': 'BIGINT', 'Repository': 'VARCHAR', 'TraceId': 'VARCHAR', 'CompletionDay': 'VARCHAR', 'ArtifactDigest': 'VARCHAR', 'PolicyDigest': 'VARCHAR', 'Gaps': 'VARCHAR[]', 'CapturedAt': 'VARCHAR', 'PayloadJSON': 'VARCHAR'}"

const eventSelect = "\"Sequence\", \"Type\", \"EventId\", \"ParentId\", \"EpisodeId\", \"HopId\", \"ParentHopId\", \"ToolCallId\", \"SessionId\", \"Timestamp\", \"ContentDigest\", \"Namespace\", \"RunId\", \"RunAttempt\", \"ProjectionId\", \"ContractVersion\", \"Repository\", \"TraceId\", \"CompletionDay\", \"ArtifactDigest\", \"PolicyDigest\", \"Gaps\", \"CapturedAt\", \"PayloadJSON\""

var eventSQLTypes = []string{
	"BIGINT",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BIGINT",
	"VARCHAR",
	"BIGINT",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR[]",
	"VARCHAR",
	"VARCHAR",
}
