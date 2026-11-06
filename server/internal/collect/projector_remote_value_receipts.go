package collect

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func fileDietPreserveReceipt(issue *fileDietIssue, previous fileDietIssue) (bool, error) {
	if previous.Receipt == nil || previous.Receipt.Status != "available" {
		return false, nil
	}
	old := previous.Receipt
	if previous.ID != issue.ID || previous.Number != issue.Number ||
		previous.Reference.RunID == nil || issue.Reference.RunID == nil ||
		*previous.Reference.RunID != *issue.Reference.RunID ||
		fileDietRawBytesDigest([]byte(old.Content)) != old.EntrySHA256 ||
		old.Digest != "sha256:"+old.ArchiveSHA256 ||
		strconv.FormatInt(old.RunID, 10) != *issue.Reference.RunID {
		return false, errors.New("preserved File Diet receipt identity or digest changed")
	}
	if issue.Run.Validation != nil && old.HeadSHA != issue.Run.Validation.HeadSHA {
		return false, errors.New("preserved File Diet receipt producer revision changed")
	}
	if issue.Run.Validation == nil {
		issue.Run.Validation = previous.Run.Validation
	}
	retained := *old
	retained.ReusedFromPreservedProof = true
	issue.Receipt = &retained
	return true, nil
}

func fileDietAppliedReceipt(content string, issue fileDietIssue) (fileDietReceipt, error) {
	var matches []struct {
		raw   []byte
		index int
	}
	for index, line := range strings.Split(strings.TrimSpace(content), "\n") {
		var row struct {
			Type     string `json:"type"`
			Number   int    `json:"number"`
			Repo     string `json:"repo"`
			URL      string `json:"url"`
			Metadata *struct {
				NodeID string `json:"node_id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return fileDietReceipt{}, errors.New("invalid File Diet safe-output receipt JSON")
		}
		if row.Type != "create_issue" || row.Number != issue.Number ||
			(row.Repo != dailyFileDietRepository && row.Repo != "githubnext/gh-aw") ||
			row.URL != fmt.Sprintf("https://github.com/%s/issues/%d", row.Repo, row.Number) {
			continue
		}
		if row.Metadata != nil && row.Metadata.NodeID != issue.ID {
			return fileDietReceipt{}, errors.New("applied File Diet output node identity mismatch")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(line)); err != nil {
			return fileDietReceipt{}, err
		}
		matches = append(matches, struct {
			raw   []byte
			index int
		}{compact.Bytes(), index})
	}
	if len(matches) != 1 {
		reason := "Producer artifact does not record creation of this output"
		if len(matches) != 0 {
			reason = "Ambiguous applied creation receipts"
		}
		return fileDietReceipt{Status: "not-applied-to-output", Reason: reason,
			SourceEntrySHA256: fileDietRawBytesDigest([]byte(content))}, nil
	}
	return fileDietReceipt{Status: "available", Content: string(matches[0].raw),
		EntrySHA256:       fileDietRawBytesDigest(matches[0].raw),
		SourceEntrySHA256: fileDietRawBytesDigest([]byte(content)),
		SourceRowIndex:    &matches[0].index}, nil
}

func fileDietZIPReceipt(content []byte) (string, error) {
	if len(content) > 2<<20 {
		return "", errors.New("File Diet receipt archive exceeds its byte budget")
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", errors.New("invalid File Diet receipt ZIP")
	}
	var selected *zip.File
	for _, file := range reader.File {
		if file.Name == "safe-output-items.jsonl" {
			if selected != nil || file.UncompressedSize64 > 64<<10 || !file.Mode().IsRegular() {
				return "", errors.New("File Diet receipt ZIP contains ambiguous or oversized safe output")
			}
			selected = file
		}
	}
	if selected == nil {
		return "", errors.New("File Diet receipt ZIP lacks its applied safe output")
	}
	input, err := selected.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(input, 64<<10+1))
	if err != nil || len(decoded) > 64<<10 {
		return "", errors.New("File Diet receipt exceeds its decoded byte budget")
	}
	return string(decoded), nil
}

func fileDietCollectReceipts(
	ctx context.Context, read fileDietRead, proof *fileDietProof, previous *fileDietArchive,
) error {
	priorIssues := map[string]fileDietIssue{}
	if previous != nil {
		for _, issue := range previous.proof.Issues {
			priorIssues[issue.ID] = issue
		}
	}
	for index := range proof.Issues {
		issue := &proof.Issues[index]
		if prior, exists := priorIssues[issue.ID]; exists {
			retained, err := fileDietPreserveReceipt(issue, prior)
			if err != nil {
				return err
			}
			if retained {
				continue
			}
		}
		var artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
			Digest  string `json:"digest"`
		}
		if len(issue.RetainedArtifacts) > 0 && !bytes.Equal(issue.RetainedArtifacts, []byte("null")) {
			if err := json.Unmarshal(issue.RetainedArtifacts, &artifacts); err != nil {
				return errors.New("invalid File Diet retained artifact inventory")
			}
		}
		reason := "No retained applied safe-output receipt"
		if len(issue.Run.Unavailable) > 0 && !bytes.Equal(issue.Run.Unavailable, []byte("null")) {
			var unavailable struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(issue.Run.Unavailable, &unavailable); err != nil {
				return err
			}
			if unavailable.Reason != "" {
				reason = unavailable.Reason
			}
		}
		issue.Receipt = &fileDietReceipt{Status: "missing", Reason: reason}
		for _, artifact := range artifacts {
			if artifact.Name != "safe-outputs-items" || artifact.Expired {
				continue
			}
			if issue.Reference.RunID == nil || issue.Run.Validation == nil || artifact.ID <= 0 {
				return errors.New("File Diet receipt lacks verified producer identity")
			}
			endpoint := fmt.Sprintf("repos/%s/actions/artifacts/%d", dailyFileDietRepository, artifact.ID)
			var metadata struct {
				SizeInBytes int64  `json:"size_in_bytes"`
				Digest      string `json:"digest"`
				WorkflowRun struct {
					ID      int64  `json:"id"`
					HeadSHA string `json:"head_sha"`
				} `json:"workflow_run"`
			}
			var err error
			metadata, err = fileDietReadJSON[struct {
				SizeInBytes int64  `json:"size_in_bytes"`
				Digest      string `json:"digest"`
				WorkflowRun struct {
					ID      int64  `json:"id"`
					HeadSHA string `json:"head_sha"`
				} `json:"workflow_run"`
			}](ctx, read, endpoint, "")
			if err != nil || metadata.SizeInBytes < 0 || metadata.SizeInBytes >= 2<<20 ||
				metadata.Digest != artifact.Digest ||
				strconv.FormatInt(metadata.WorkflowRun.ID, 10) != *issue.Reference.RunID ||
				metadata.WorkflowRun.HeadSHA != issue.Run.Validation.HeadSHA {
				return errors.New("File Diet receipt metadata identity mismatch")
			}
			content, err := read(ctx, endpoint+"/zip", "", true)
			if err != nil || "sha256:"+fileDietRawBytesDigest(content) != metadata.Digest {
				return errors.New("File Diet receipt archive digest mismatch")
			}
			text, err := fileDietZIPReceipt(content)
			if err != nil {
				return err
			}
			receipt, err := fileDietAppliedReceipt(text, *issue)
			if err != nil {
				return err
			}
			receipt.RunID, receipt.HeadSHA = metadata.WorkflowRun.ID, metadata.WorkflowRun.HeadSHA
			receipt.ArtifactID, receipt.Digest = artifact.ID, metadata.Digest
			receipt.ArchiveSHA256 = fileDietRawBytesDigest(content)
			receipt.ObservedAt = time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
			issue.Receipt = &receipt
			break
		}
	}
	return nil
}
