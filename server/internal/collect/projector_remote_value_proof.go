package collect

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const fileDietProofLimit = 16 << 20

var fileDietInventoryName = regexp.MustCompile(`^inventory-[a-f0-9]{40}\.json\.gz$`)

type fileDietAdoption struct {
	Commit         string `json:"commit"`
	At             string `json:"at"`
	BaselineCommit string `json:"baselineCommit"`
}
type fileDietSnapshot struct {
	At     string `json:"at"`
	Commit string `json:"commit"`
}
type fileDietHistoryRevision struct {
	Commit string `json:"commit"`
	At     string `json:"at"`
}
type fileDietDiscovery struct {
	Query      string `json:"query"`
	TotalCount int    `json:"totalCount"`
	Complete   bool   `json:"complete"`
}
type fileDietTarget struct {
	ID         string `json:"id"`
	Number     int    `json:"number"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
}
type fileDietEdge struct {
	Relation string         `json:"relation"`
	At       string         `json:"at,omitempty"`
	Target   fileDietTarget `json:"target"`
}
type fileDietGraphEvent struct {
	Type     string `json:"type"`
	At       string `json:"at"`
	CloserID string `json:"closerId,omitempty"`
}
type fileDietGraph struct {
	fileDietTarget
	Kind                 string               `json:"kind"`
	State                string               `json:"state"`
	StateReason          *string              `json:"stateReason"`
	CreatedAt            string               `json:"createdAt"`
	UpdatedAt            string               `json:"updatedAt"`
	ClosedAt             *string              `json:"closedAt"`
	MergedAt             *string              `json:"mergedAt"`
	MergeCommit          *string              `json:"mergeCommit"`
	ObservedAt           string               `json:"observedAt"`
	HistoryComplete      bool                 `json:"historyComplete"`
	ClosingLinksComplete bool                 `json:"closingLinksComplete"`
	Edges                []fileDietEdge       `json:"edges"`
	Events               []fileDietGraphEvent `json:"events"`
}
type fileDietTimelineTarget struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
}
type fileDietTimelineEvent struct {
	Type   string                  `json:"type"`
	At     string                  `json:"at"`
	Target *fileDietTimelineTarget `json:"target,omitempty"`
}
type fileDietTimeline struct {
	Complete bool                    `json:"complete"`
	Events   []fileDietTimelineEvent `json:"events"`
}
type fileDietReference struct {
	Repository string  `json:"repository"`
	RunID      *string `json:"runId"`
	Attempt    *int    `json:"attempt"`
	Marker     string  `json:"marker"`
	WorkflowID string  `json:"workflowId"`
	TrackerID  *string `json:"trackerId"`
}
type fileDietValidation struct {
	Status     string `json:"status"`
	HeadSHA    string `json:"headSha"`
	Attempt    int    `json:"attempt"`
	SourcePath string `json:"sourcePath"`
	CreatedAt  string `json:"createdAt"`
	URL        string `json:"url"`
	Limitation string `json:"limitation"`
}
type fileDietRun struct {
	Validation  *fileDietValidation `json:"validation"`
	Unavailable json.RawMessage     `json:"unavailable"`
}
type fileDietReceipt struct {
	Status                   string `json:"status"`
	Reason                   string `json:"reason,omitempty"`
	Content                  string `json:"content,omitempty"`
	EntrySHA256              string `json:"entrySha256,omitempty"`
	SourceEntrySHA256        string `json:"sourceEntrySha256,omitempty"`
	SourceRowIndex           *int   `json:"sourceRowIndex,omitempty"`
	RunID                    int64  `json:"runId,omitempty"`
	HeadSHA                  string `json:"headSha,omitempty"`
	ArtifactID               int64  `json:"artifactId,omitempty"`
	Digest                   string `json:"digest,omitempty"`
	ArchiveSHA256            string `json:"archiveSha256,omitempty"`
	ObservedAt               string `json:"observedAt,omitempty"`
	ReusedFromPreservedProof bool   `json:"reusedFromPreservedProof,omitempty"`
}
type fileDietIssue struct {
	Number            int               `json:"number"`
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	CreatedAt         string            `json:"createdAt"`
	Eligible          *bool             `json:"eligible,omitempty"`
	BodySHA256        string            `json:"bodySha256"`
	TargetPath        *string           `json:"targetPath"`
	Reference         fileDietReference `json:"reference"`
	Run               fileDietRun       `json:"run"`
	RetainedArtifacts json.RawMessage   `json:"retainedArtifacts"`
	Graph             fileDietGraph     `json:"graph"`
	Timeline          fileDietTimeline  `json:"timeline"`
	Receipt           *fileDietReceipt  `json:"receipt,omitempty"`
}
type fileDietPullFile struct {
	Path         string  `json:"path"`
	Status       string  `json:"status"`
	PreviousPath *string `json:"previousPath"`
}
type fileDietPull struct {
	Number      int                `json:"number"`
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	CreatedAt   string             `json:"createdAt"`
	MergedAt    *string            `json:"mergedAt"`
	HeadSHA     string             `json:"headSha"`
	BaseSHA     string             `json:"baseSha"`
	MergeSHA    *string            `json:"mergeSha"`
	Graph       fileDietGraph      `json:"graph"`
	Files       []fileDietPullFile `json:"files"`
	PreMergeSHA string             `json:"preMergeSha,omitempty"`
}
type fileDietPreviousProof struct {
	Digest     string `json:"digest"`
	ObservedAt string `json:"observedAt"`
	Until      string `json:"until"`
}
type fileDietPartition struct {
	File   string `json:"file"`
	Commit string `json:"commit"`
	SHA256 string `json:"sha256"`
}
type fileDietAcquisition struct {
	Kind             string `json:"kind"`
	Endpoint         string `json:"endpoint"`
	InstrumentSHA256 string `json:"instrumentSha256"`
	NewInventories   int    `json:"newInventories"`
	NewBlobs         int    `json:"newBlobs"`
}
type fileDietProof struct {
	SchemaVersion       int                       `json:"schemaVersion"`
	Repository          string                    `json:"repository"`
	RepositoryID        int64                     `json:"repositoryId"`
	Aliases             []string                  `json:"aliases"`
	SourcePath          string                    `json:"sourcePath"`
	Adoption            fileDietAdoption          `json:"adoption"`
	Until               string                    `json:"until"`
	ObservedAt          string                    `json:"observedAt"`
	Discovery           []fileDietDiscovery       `json:"discovery"`
	Issues              []fileDietIssue           `json:"issues"`
	Pulls               []fileDietPull            `json:"pulls"`
	Snapshots           []fileDietSnapshot        `json:"snapshots"`
	History             []fileDietHistoryRevision `json:"history"`
	PreviousProof       *fileDietPreviousProof    `json:"previousProof,omitempty"`
	InventoryPartitions []fileDietPartition       `json:"inventoryPartitions"`
	UniqueBlobs         int                       `json:"uniqueBlobs"`
	Acquisition         *fileDietAcquisition      `json:"acquisition,omitempty"`
}
type fileDietFile struct {
	Path  string `json:"path"`
	Blob  string `json:"blob"`
	Lines int    `json:"lines"`
	Bytes int    `json:"bytes"`
}
type fileDietInventory struct {
	Commit          string         `json:"commit"`
	Files           []fileDietFile `json:"files"`
	InventoryDigest string         `json:"inventoryDigest"`
}
type fileDietArchive struct {
	proof       fileDietProof
	inventories map[string]fileDietInventory
	rawProof    []byte
	files       map[string][]byte
}

func fileDietJSONDigest(value any) string {
	return fileDietDigest(value)
}

func fileDietRawDigest(value []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, value) != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))
}

func fileDietReadArchive(files map[string][]byte, filename string) ([]byte, error) {
	decoded, err := fileDietReadGzip(files, filename)
	if err != nil {
		return nil, err
	}
	var partition struct {
		Kind          string `json:"kind"`
		SchemaVersion int    `json:"schemaVersion"`
		Encoding      string `json:"encoding"`
		Parts         []struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		} `json:"parts"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(decoded, &partition); err != nil {
		return nil, err
	}
	if partition.Kind != "partitioned-census-json" {
		return decoded, nil
	}
	if partition.SchemaVersion != 1 || partition.Encoding != "base64-json-utf8" ||
		len(partition.Parts) == 0 || len(partition.Parts) > 128 {
		return nil, errors.New("invalid partitioned File Diet archive")
	}
	var joined []byte
	for _, part := range partition.Parts {
		if !strings.HasSuffix(part.File, ".json.gz") || strings.Contains(part.File, "/") ||
			strings.Contains(part.File, "\\") || strings.Contains(part.File, "..") ||
			part.File == filename {
			return nil, errors.New("unsafe partitioned File Diet archive path")
		}
		encoded, err := fileDietReadGzip(files, part.File)
		if err != nil {
			return nil, err
		}
		var text string
		if err := json.Unmarshal(encoded, &text); err != nil {
			return nil, err
		}
		payload, err := base64.StdEncoding.Strict().DecodeString(text)
		if err != nil || fileDietRawBytesDigest(payload) != part.SHA256 ||
			len(joined)+len(payload) > fileDietProofLimit {
			return nil, errors.New("partitioned File Diet archive digest or size mismatch")
		}
		joined = append(joined, payload...)
	}
	if fileDietRawBytesDigest(joined) != partition.SHA256 {
		return nil, errors.New("partitioned File Diet archive digest mismatch")
	}
	return joined, nil
}

func fileDietReadGzip(files map[string][]byte, filename string) ([]byte, error) {
	raw, ok := files[filename]
	if !ok || len(raw) > fileDietProofLimit {
		return nil, errors.New("missing or oversized File Diet archive")
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(reader, fileDietProofLimit+1))
	if err != nil || len(decoded) > fileDietProofLimit {
		return nil, errors.New("File Diet archive exceeds its decoded size budget")
	}
	return decoded, nil
}

func fileDietRawBytesDigest(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func fileDietLoadProof(files map[string][]byte) (fileDietArchive, error) {
	var result fileDietArchive
	proofRaw, err := fileDietReadArchive(files, "proof.json.gz")
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(proofRaw, &result.proof); err != nil {
		return result, err
	}
	proof := result.proof
	if proof.SchemaVersion != 1 || proof.Repository != dailyFileDietRepository ||
		proof.SourcePath != ".github/workflows/daily-file-diet.md" ||
		!slices.Equal(proof.Aliases, []string{dailyFileDietRepository, "githubnext/gh-aw"}) ||
		!validFileDietSHA(proof.Adoption.Commit) || !validFileDietSHA(proof.Adoption.BaselineCommit) ||
		proof.Adoption.Commit != "1186030a620f4113f655c156bedf70cf2c164f79" ||
		proof.Adoption.At != "2025-11-15T13:36:21Z" ||
		!fileDietTime(proof.Adoption.At).Before(fileDietTime(proof.Until)) ||
		fileDietTime(proof.ObservedAt).Before(fileDietTime(proof.Until)) {
		return result, errors.New("File Diet proof identity or frozen adoption changed")
	}
	issues, issueNumbers, pulls, pullNumbers := map[string]bool{}, map[int]bool{}, map[string]bool{}, map[int]bool{}
	for _, issue := range proof.Issues {
		if issue.ID == "" || issue.Number <= 0 || issues[issue.ID] || issueNumbers[issue.Number] {
			return result, errors.New("duplicate File Diet output identity")
		}
		issues[issue.ID], issueNumbers[issue.Number] = true, true
	}
	for _, pull := range proof.Pulls {
		if pull.ID == "" || pull.Number <= 0 || pulls[pull.ID] || pullNumbers[pull.Number] {
			return result, errors.New("duplicate File Diet implementation identity")
		}
		pulls[pull.ID], pullNumbers[pull.Number] = true, true
	}
	result.inventories = make(map[string]fileDietInventory, len(proof.InventoryPartitions))
	measurements := map[string][2]int{}
	for _, partition := range proof.InventoryPartitions {
		if !fileDietInventoryName.MatchString(partition.File) ||
			partition.File != "inventory-"+partition.Commit+".json.gz" ||
			!validFileDietSHA(partition.Commit) {
			return result, errors.New("invalid File Diet inventory identity")
		}
		raw, err := fileDietReadArchive(files, partition.File)
		if err != nil {
			return result, err
		}
		var envelope struct {
			Commit          string          `json:"commit"`
			Files           json.RawMessage `json:"files"`
			InventoryDigest string          `json:"inventoryDigest"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil ||
			fileDietRawDigest(raw) != partition.SHA256 ||
			fileDietRawDigest(envelope.Files) != envelope.InventoryDigest ||
			envelope.Commit != partition.Commit {
			return result, errors.New("File Diet inventory partition digest mismatch")
		}
		var inventory fileDietInventory
		if err := json.Unmarshal(raw, &inventory); err != nil {
			return result, err
		}
		paths := map[string]bool{}
		for _, file := range inventory.Files {
			count := [2]int{file.Lines, file.Bytes}
			if paths[file.Path] || !fileDietEligible(file.Path) || !validFileDietSHA(file.Blob) ||
				file.Lines < 0 || file.Bytes < file.Lines {
				return result, errors.New("invalid File Diet native file inventory")
			}
			if previous, ok := measurements[file.Blob]; ok && previous != count {
				return result, errors.New("conflicting File Diet blob measurements")
			}
			measurements[file.Blob], paths[file.Path] = count, true
		}
		if _, ok := result.inventories[partition.Commit]; ok {
			return result, errors.New("duplicate File Diet inventory revision")
		}
		result.inventories[partition.Commit] = inventory
	}
	if len(measurements) != proof.UniqueBlobs {
		return result, errors.New("File Diet unique blob count changed")
	}
	result.rawProof, result.files = proofRaw, files
	return result, nil
}

func fileDietEligible(path string) bool {
	return strings.HasPrefix(path, "pkg/") && strings.HasSuffix(path, ".go") &&
		!strings.HasSuffix(path, "_test.go")
}

func fileDietTime(text string) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, text)
	return at
}

func fileDietSeed(ctx context.Context, workspace string) (fileDietArchive, error) {
	git, err := newNativeFileDietGit(ctx, workspace, "githubnext/gh-cao")
	if err != nil {
		return fileDietArchive{}, err
	}
	if err := git.fetch(ctx, []string{fileDietHistoricalRevision}, false); err != nil {
		return fileDietArchive{}, err
	}
	tree, err := git.tree(ctx, fileDietHistoricalRevision, fileDietHistoricalPath, 1024)
	if err != nil {
		return fileDietArchive{}, err
	}
	files := map[string][]byte{}
	var total int
	for _, entry := range tree {
		name := strings.TrimPrefix(entry.path, fileDietHistoricalPath+"/")
		if entry.mode != "100644" || entry.kind != "blob" ||
			!(name == "proof.json.gz" || fileDietInventoryName.MatchString(name)) ||
			len(files) >= postgresx.ValueProofMaxFiles {
			return fileDietArchive{}, errors.New("historical File Diet proof contains unreviewed content")
		}
		if err := git.fetch(ctx, []string{entry.sha}, true); err != nil {
			return fileDietArchive{}, err
		}
		content, err := git.blob(ctx, entry.sha, fileDietProofLimit-total)
		if err != nil {
			return fileDietArchive{}, err
		}
		total += len(content)
		if total > fileDietProofLimit {
			return fileDietArchive{}, errors.New("historical File Diet proof exceeds its byte budget")
		}
		files[name] = content
	}
	return fileDietLoadProof(files)
}

func fileDietRestore(files []postgresx.ValueProofFile) (fileDietArchive, error) {
	candidates := map[string][]byte{}
	for _, file := range files {
		name := strings.TrimPrefix(file.Path, "report/proof/")
		if name == file.Path {
			name = strings.TrimPrefix(file.Path, "proof/")
		}
		if name != "proof.json.gz" && !fileDietInventoryName.MatchString(name) {
			continue
		}
		if _, exists := candidates[name]; exists {
			return fileDietArchive{}, errors.New("duplicate restored File Diet proof archive")
		}
		candidates[name] = file.Content
	}
	return fileDietLoadProof(candidates)
}

func fileDietArchiveProof(value any) ([]byte, error) {
	content := fileDietEncodeJSON(value)
	if len(content) == 0 || len(content)+1 > fileDietProofLimit {
		return nil, errors.New("File Diet proof exceeds decoded byte budget")
	}
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(append(content, '\n')); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func fileDietSHA(content string) bool {
	if len(content) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(content)
	return err == nil
}
