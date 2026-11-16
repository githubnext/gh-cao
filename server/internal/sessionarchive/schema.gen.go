// Code generated from server/spec/trace.tsp. DO NOT EDIT.

package sessionarchive

const ContractVersion = 2

type Event struct {
	Namespace                  string   `json:"Namespace"`
	Repository                 string   `json:"Repository"`
	RunID                      string   `json:"RunId"`
	RunAttempt                 int64    `json:"RunAttempt"`
	TraceID                    string   `json:"TraceId"`
	ProjectionID               string   `json:"ProjectionId"`
	ContractVersion            int64    `json:"ContractVersion"`
	EvidenceVersion            string   `json:"EvidenceVersion"`
	Sequence                   int64    `json:"Sequence"`
	Type                       string   `json:"Type"`
	ContentDigest              string   `json:"ContentDigest"`
	CompletionDay              string   `json:"CompletionDay"`
	ArtifactDigest             string   `json:"ArtifactDigest"`
	PolicyDigest               string   `json:"PolicyDigest"`
	Gaps                       []string `json:"Gaps"`
	CapturedAt                 string   `json:"CapturedAt"`
	PresentFields              []string `json:"PresentFields"`
	NullFields                 []string `json:"NullFields"`
	InvalidFields              []string `json:"InvalidFields"`
	OmissionReasons            []string `json:"OmissionReasons"`
	EventID                    string   `json:"EventId"`
	ParentID                   string   `json:"ParentId"`
	Timestamp                  string   `json:"Timestamp"`
	Component                  string   `json:"Component"`
	Phase                      string   `json:"Phase"`
	ProvenancePath             string   `json:"ProvenancePath"`
	ProvenanceIndex            *int64   `json:"ProvenanceIndex"`
	ProvenanceTimestampMs      *float64 `json:"ProvenanceTimestampMs"`
	ProvenanceAttemptID        string   `json:"ProvenanceAttemptId"`
	ProvenanceStartupAttemptID string   `json:"ProvenanceStartupAttemptId"`
	SessionID                  string   `json:"SessionId"`
	AgentID                    string   `json:"AgentId"`
	ToolCallID                 string   `json:"ToolCallId"`
	ToolName                   string   `json:"ToolName"`
	Success                    *bool    `json:"Success"`
	Status                     string   `json:"Status"`
	IsError                    *bool    `json:"IsError"`
	Method                     string   `json:"Method"`
	ServerName                 string   `json:"ServerName"`
	RpcIdString                string   `json:"RpcIdString"`
	RpcIdNumber                *int64   `json:"RpcIdNumber"`
	RequestIdString            string   `json:"RequestIdString"`
	RequestIdNumber            *int64   `json:"RequestIdNumber"`
	InputTokens                *int64   `json:"InputTokens"`
	OutputTokens               *int64   `json:"OutputTokens"`
	DurationMs                 *float64 `json:"DurationMs"`
	DataEvent                  string   `json:"DataEvent"`
	Message                    string   `json:"Message"`
	Layer                      string   `json:"Layer"`
	DataAttempt                *int64   `json:"DataAttempt"`
	DataAttemptID              string   `json:"DataAttemptId"`
	DataStartupAttemptID       string   `json:"DataStartupAttemptId"`
	Strategy                   string   `json:"Strategy"`
	StartedAt                  *float64 `json:"StartedAt"`
	DataType                   string   `json:"DataType"`
	ProducerVersion            *int64   `json:"ProducerVersion"`
	ProducerVersionString      string   `json:"ProducerVersionString"`
	PayloadJSON                string   `json:"PayloadJSON"`
}

var eventColumns = []string{
	"Namespace",
	"Repository",
	"RunId",
	"RunAttempt",
	"TraceId",
	"ProjectionId",
	"ContractVersion",
	"EvidenceVersion",
	"Sequence",
	"Type",
	"ContentDigest",
	"CompletionDay",
	"ArtifactDigest",
	"PolicyDigest",
	"Gaps",
	"CapturedAt",
	"PresentFields",
	"NullFields",
	"InvalidFields",
	"OmissionReasons",
	"EventId",
	"ParentId",
	"Timestamp",
	"Component",
	"Phase",
	"ProvenancePath",
	"ProvenanceIndex",
	"ProvenanceTimestampMs",
	"ProvenanceAttemptId",
	"ProvenanceStartupAttemptId",
	"SessionId",
	"AgentId",
	"ToolCallId",
	"ToolName",
	"Success",
	"Status",
	"IsError",
	"Method",
	"ServerName",
	"RpcIdString",
	"RpcIdNumber",
	"RequestIdString",
	"RequestIdNumber",
	"InputTokens",
	"OutputTokens",
	"DurationMs",
	"DataEvent",
	"Message",
	"Layer",
	"DataAttempt",
	"DataAttemptId",
	"DataStartupAttemptId",
	"Strategy",
	"StartedAt",
	"DataType",
	"ProducerVersion",
	"ProducerVersionString",
	"PayloadJSON",
}

const eventJSONColumns = "{'Namespace': 'VARCHAR', 'Repository': 'VARCHAR', 'RunId': 'VARCHAR', 'RunAttempt': 'BIGINT', 'TraceId': 'VARCHAR', 'ProjectionId': 'VARCHAR', 'ContractVersion': 'BIGINT', 'EvidenceVersion': 'VARCHAR', 'Sequence': 'BIGINT', 'Type': 'VARCHAR', 'ContentDigest': 'VARCHAR', 'CompletionDay': 'VARCHAR', 'ArtifactDigest': 'VARCHAR', 'PolicyDigest': 'VARCHAR', 'Gaps': 'VARCHAR[]', 'CapturedAt': 'VARCHAR', 'PresentFields': 'VARCHAR[]', 'NullFields': 'VARCHAR[]', 'InvalidFields': 'VARCHAR[]', 'OmissionReasons': 'VARCHAR[]', 'EventId': 'VARCHAR', 'ParentId': 'VARCHAR', 'Timestamp': 'VARCHAR', 'Component': 'VARCHAR', 'Phase': 'VARCHAR', 'ProvenancePath': 'VARCHAR', 'ProvenanceIndex': 'BIGINT', 'ProvenanceTimestampMs': 'DOUBLE', 'ProvenanceAttemptId': 'VARCHAR', 'ProvenanceStartupAttemptId': 'VARCHAR', 'SessionId': 'VARCHAR', 'AgentId': 'VARCHAR', 'ToolCallId': 'VARCHAR', 'ToolName': 'VARCHAR', 'Success': 'BOOLEAN', 'Status': 'VARCHAR', 'IsError': 'BOOLEAN', 'Method': 'VARCHAR', 'ServerName': 'VARCHAR', 'RpcIdString': 'VARCHAR', 'RpcIdNumber': 'BIGINT', 'RequestIdString': 'VARCHAR', 'RequestIdNumber': 'BIGINT', 'InputTokens': 'BIGINT', 'OutputTokens': 'BIGINT', 'DurationMs': 'DOUBLE', 'DataEvent': 'VARCHAR', 'Message': 'VARCHAR', 'Layer': 'VARCHAR', 'DataAttempt': 'BIGINT', 'DataAttemptId': 'VARCHAR', 'DataStartupAttemptId': 'VARCHAR', 'Strategy': 'VARCHAR', 'StartedAt': 'DOUBLE', 'DataType': 'VARCHAR', 'ProducerVersion': 'BIGINT', 'ProducerVersionString': 'VARCHAR', 'PayloadJSON': 'VARCHAR'}"

const eventSelect = "\"Namespace\", \"Repository\", \"RunId\", \"RunAttempt\", \"TraceId\", \"ProjectionId\", \"ContractVersion\", \"EvidenceVersion\", \"Sequence\", \"Type\", \"ContentDigest\", \"CompletionDay\", \"ArtifactDigest\", \"PolicyDigest\", \"Gaps\", \"CapturedAt\", \"PresentFields\", \"NullFields\", \"InvalidFields\", \"OmissionReasons\", \"EventId\", \"ParentId\", \"Timestamp\", \"Component\", \"Phase\", \"ProvenancePath\", \"ProvenanceIndex\", \"ProvenanceTimestampMs\", \"ProvenanceAttemptId\", \"ProvenanceStartupAttemptId\", \"SessionId\", \"AgentId\", \"ToolCallId\", \"ToolName\", \"Success\", \"Status\", \"IsError\", \"Method\", \"ServerName\", \"RpcIdString\", \"RpcIdNumber\", \"RequestIdString\", \"RequestIdNumber\", \"InputTokens\", \"OutputTokens\", \"DurationMs\", \"DataEvent\", \"Message\", \"Layer\", \"DataAttempt\", \"DataAttemptId\", \"DataStartupAttemptId\", \"Strategy\", \"StartedAt\", \"DataType\", \"ProducerVersion\", \"ProducerVersionString\", \"PayloadJSON\""

var eventSQLTypes = []string{
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BIGINT",
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
	"VARCHAR[]",
	"VARCHAR[]",
	"VARCHAR[]",
	"VARCHAR[]",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BIGINT",
	"DOUBLE",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BOOLEAN",
	"VARCHAR",
	"BOOLEAN",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BIGINT",
	"VARCHAR",
	"BIGINT",
	"BIGINT",
	"BIGINT",
	"DOUBLE",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"BIGINT",
	"VARCHAR",
	"VARCHAR",
	"VARCHAR",
	"DOUBLE",
	"VARCHAR",
	"BIGINT",
	"VARCHAR",
	"VARCHAR",
}

const EventKQLSchema = "Namespace:string,Repository:string,RunId:string,RunAttempt:long,TraceId:string,ProjectionId:string,ContractVersion:long,EvidenceVersion:string,Sequence:long,Type:string,ContentDigest:string,CompletionDay:string,ArtifactDigest:string,PolicyDigest:string,Gaps:dynamic,CapturedAt:string,PresentFields:dynamic,NullFields:dynamic,InvalidFields:dynamic,OmissionReasons:dynamic,EventId:string,ParentId:string,Timestamp:string,Component:string,Phase:string,ProvenancePath:string,ProvenanceIndex:long,ProvenanceTimestampMs:real,ProvenanceAttemptId:string,ProvenanceStartupAttemptId:string,SessionId:string,AgentId:string,ToolCallId:string,ToolName:string,Success:bool,Status:string,IsError:bool,Method:string,ServerName:string,RpcIdString:string,RpcIdNumber:long,RequestIdString:string,RequestIdNumber:long,InputTokens:long,OutputTokens:long,DurationMs:real,DataEvent:string,Message:string,Layer:string,DataAttempt:long,DataAttemptId:string,DataStartupAttemptId:string,Strategy:string,StartedAt:real,DataType:string,ProducerVersion:long,ProducerVersionString:string,PayloadJSON:string"

type evidenceField struct {
	Name, Path, Kind, Validation string
	Index                        int
}

var evidenceFields = []evidenceField{
	{"EventId", "id", "string", "", 20},
	{"ParentId", "parentId", "string", "", 21},
	{"Timestamp", "timestamp", "string", "", 22},
	{"Component", "provenance.component", "string", "", 23},
	{"Phase", "provenance.phase", "string", "", 24},
	{"ProvenancePath", "provenance.path", "string", "", 25},
	{"ProvenanceIndex", "provenance.index", "long", "count", 26},
	{"ProvenanceTimestampMs", "provenance.timestampMs", "real", "duration", 27},
	{"ProvenanceAttemptId", "provenance.attemptId", "string", "", 28},
	{"ProvenanceStartupAttemptId", "provenance.startupAttemptId", "string", "", 29},
	{"SessionId", "data.sessionId", "string", "", 30},
	{"AgentId", "data.agentId", "string", "", 31},
	{"ToolCallId", "data.toolCallId", "string", "", 32},
	{"ToolName", "data.toolName", "string", "", 33},
	{"Success", "data.success", "bool", "", 34},
	{"Status", "data.status", "string", "", 35},
	{"IsError", "data.isError", "bool", "", 36},
	{"Method", "data.method", "string", "", 37},
	{"ServerName", "data.serverName", "string", "", 38},
	{"RpcIdString", "data.rpcId", "string", "identifier-string", 39},
	{"RpcIdNumber", "data.rpcId", "long", "identifier-number", 40},
	{"RequestIdString", "data.requestId", "string", "identifier-string", 41},
	{"RequestIdNumber", "data.requestId", "long", "identifier-number", 42},
	{"InputTokens", "data.usage.inputTokens", "long", "count", 43},
	{"OutputTokens", "data.usage.outputTokens", "long", "count", 44},
	{"DurationMs", "data.durationMs", "real", "duration", 45},
	{"DataEvent", "data.event", "string", "", 46},
	{"Message", "data.message", "string", "", 47},
	{"Layer", "data.layer", "string", "", 48},
	{"DataAttempt", "data.attempt", "long", "count", 49},
	{"DataAttemptId", "data.attemptId", "string", "", 50},
	{"DataStartupAttemptId", "data.startupAttemptId", "string", "", 51},
	{"Strategy", "data.strategy", "string", "", 52},
	{"StartedAt", "data.startedAt", "real", "duration", 53},
	{"DataType", "data.type", "string", "", 54},
	{"ProducerVersion", "data.version", "long", "identifier-number", 55},
	{"ProducerVersionString", "data.version", "string", "identifier-string", 56},
	{"Provenance", "provenance", "object", "object", -1},
	{"Usage", "data.usage", "object", "object", -1},
	{"Error", "data.error", "any", "any", -1},
	{"RetryHint", "data.retryHint", "any", "any", -1},
	{"AttemptResults", "data.attemptResults", "array", "array", -1},
	{"StartupAttemptResults", "data.startupAttemptResults", "array", "array", -1},
	{"Objectives", "data.objectives", "array", "array", -1},
	{"Actions", "data.actions", "array", "array", -1},
	{"States", "data.states", "array", "array", -1},
	{"Events", "data.events", "array", "array", -1},
	{"ToolCalls", "data.toolCalls", "array", "array", -1},
	{"Observations", "data.observations", "array", "array", -1},
	{"Resources", "data.resources", "array", "array", -1},
	{"ProvenanceEdges", "data.provenanceEdges", "array", "array", -1},
	{"Reference", "data.reference", "object", "object", -1},
}
