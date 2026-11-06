package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

type fileDietMeasurements struct {
	inventories    map[string]fileDietInventory
	newInventories int
	newBlobs       int
	uniqueBlobs    int
}

const fileDietAcquisitionInstrumentSHA = "d7f314ce0e23e2e3a1e5a8f5f059f974a13bf98f737a251ced2b93fee2e89a23"

func fileDietAcquireInventories(
	ctx context.Context, workspace string, proof *fileDietProof, previous *fileDietArchive,
) (fileDietMeasurements, error) {
	result := fileDietMeasurements{inventories: map[string]fileDietInventory{}}
	blobs := map[string][2]int{}
	if previous != nil {
		for commit, inventory := range previous.inventories {
			result.inventories[commit] = inventory
			for _, file := range inventory.Files {
				if prior, exists := blobs[file.Blob]; exists && prior != [2]int{file.Lines, file.Bytes} {
					return result, errors.New("conflicting preserved File Diet blob measurements")
				}
				blobs[file.Blob] = [2]int{file.Lines, file.Bytes}
			}
		}
	}
	git, err := newNativeFileDietGit(ctx, workspace, dailyFileDietRepository)
	if err != nil {
		return result, err
	}
	revisions := map[string]bool{proof.Adoption.BaselineCommit: true, proof.Adoption.Commit: true}
	for _, row := range proof.Snapshots {
		revisions[row.Commit] = true
	}
	for _, row := range proof.History {
		revisions[row.Commit] = true
	}
	for _, issue := range proof.Issues {
		if issue.Run.Validation != nil {
			revisions[issue.Run.Validation.HeadSHA] = true
		}
	}
	for _, pull := range proof.Pulls {
		if pull.MergeSHA != nil {
			revisions[pull.HeadSHA], revisions[*pull.MergeSHA] = true, true
		}
	}
	missing := make([]string, 0)
	for revision := range revisions {
		if !validFileDietSHA(revision) {
			return result, errors.New("invalid File Diet immutable revision")
		}
		if _, exists := result.inventories[revision]; !exists {
			missing = append(missing, revision)
		}
	}
	if err := git.fetch(ctx, missing, false); err != nil {
		return result, err
	}
	priorPulls := map[string]fileDietPull{}
	if previous != nil {
		for _, pull := range previous.proof.Pulls {
			priorPulls[pull.ID] = pull
		}
	}
	for index := range proof.Pulls {
		pull := &proof.Pulls[index]
		if pull.MergeSHA == nil || !fileDietTime(*pull.MergedAt).Before(fileDietTime(proof.Until)) {
			continue
		}
		if retained, ok := priorPulls[pull.ID]; ok && retained.MergeSHA != nil &&
			*retained.MergeSHA == *pull.MergeSHA && retained.PreMergeSHA != "" {
			pull.PreMergeSHA = retained.PreMergeSHA
		} else {
			// A retained merge inventory does not imply the commit object is
			// present in this invocation's disposable Git store.
			if _, exists := result.inventories[*pull.MergeSHA]; exists {
				if err := git.fetch(ctx, []string{*pull.MergeSHA}, false); err != nil {
					return result, err
				}
			}
			pull.PreMergeSHA, err = git.firstParent(ctx, *pull.MergeSHA)
			if err != nil {
				return result, err
			}
		}
		revisions[pull.PreMergeSHA] = true
	}
	missing = missing[:0]
	for revision := range revisions {
		if !validFileDietSHA(revision) {
			return result, errors.New("invalid File Diet merge parent revision")
		}
		if _, exists := result.inventories[revision]; !exists {
			missing = append(missing, revision)
		}
	}
	if err := git.fetch(ctx, missing, false); err != nil {
		return result, err
	}
	newFiles := map[string][]fileDietFile{}
	newBlobs := map[string]bool{}
	for _, revision := range missing {
		tree, err := git.tree(ctx, revision, "pkg", 50000)
		if err != nil {
			return result, err
		}
		files := []fileDietFile{}
		paths := map[string]bool{}
		for _, entry := range tree {
			if entry.kind != "blob" || entry.mode != "100644" && entry.mode != "100755" ||
				!fileDietEligible(entry.path) {
				continue
			}
			if paths[entry.path] {
				return result, errors.New("duplicate native file in File Diet tree")
			}
			paths[entry.path] = true
			files = append(files, fileDietFile{Path: entry.path, Blob: entry.sha})
			if _, exists := blobs[entry.sha]; !exists {
				newBlobs[entry.sha] = true
			}
		}
		newFiles[revision] = files
	}
	if len(blobs)+len(newBlobs) > 50000 {
		return result, errors.New("File Diet native blob measurement budget exceeded")
	}
	newSHA := make([]string, 0, len(newBlobs))
	for blob := range newBlobs {
		newSHA = append(newSHA, blob)
	}
	slices.Sort(newSHA)
	for offset := 0; offset < len(newSHA); offset += 50 {
		batch := newSHA[offset:min(offset+50, len(newSHA))]
		if err := git.fetch(ctx, batch, true); err != nil {
			return result, err
		}
		measured, err := git.measureBlobs(ctx, batch)
		if err != nil {
			return result, err
		}
		for sha, count := range measured {
			blobs[sha] = count
		}
	}
	result.newBlobs, result.newInventories = len(newBlobs), len(newFiles)
	for revision, files := range newFiles {
		for index := range files {
			count := blobs[files[index].Blob]
			files[index].Lines, files[index].Bytes = count[0], count[1]
		}
		result.inventories[revision] = fileDietInventory{Commit: revision,
			Files: files, InventoryDigest: fileDietJSONDigest(files)}
	}
	allBlobs := map[string]bool{}
	for _, inventory := range result.inventories {
		for _, file := range inventory.Files {
			allBlobs[file.Blob] = true
		}
	}
	result.uniqueBlobs = len(allBlobs)
	return result, nil
}

func fileDietBuildProofFiles(
	proof *fileDietProof, measured fileDietMeasurements, previous *fileDietArchive,
) ([]postgresx.ValueProofFile, error) {
	// Prefix retained native proof identically to the reviewed Actions collector.
	files := map[string][]byte{}
	keys := make([]string, 0, len(measured.inventories))
	for commit := range measured.inventories {
		keys = append(keys, commit)
	}
	slices.Sort(keys)
	proof.InventoryPartitions = []fileDietPartition{}
	for _, commit := range keys {
		inventory := measured.inventories[commit]
		name := "inventory-" + commit + ".json.gz"
		digest := fileDietJSONDigest(inventory)
		if previous != nil {
			if archive, ok := previous.files[name]; ok {
				files[name] = archive
				for _, part := range previous.proof.InventoryPartitions {
					if part.File == name {
						digest = part.SHA256
						break
					}
				}
			}
		}
		if files[name] == nil {
			var err error
			files[name], err = fileDietArchiveProof(inventory)
			if err != nil {
				return nil, err
			}
		}
		proof.InventoryPartitions = append(proof.InventoryPartitions, fileDietPartition{
			File: name, Commit: commit, SHA256: digest,
		})
	}
	proof.UniqueBlobs = measured.uniqueBlobs
	kind := "fresh-github-and-immutable-git"
	if previous != nil {
		kind = "incremental-github-and-immutable-git"
	}
	proof.Acquisition = &fileDietAcquisition{Kind: kind, Endpoint: proof.Until,
		InstrumentSHA256: fileDietAcquisitionInstrumentSHA,
		NewInventories:   measured.newInventories, NewBlobs: measured.newBlobs}
	proof.ObservedAt = time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	if previous != nil {
		proof.PreviousProof = &fileDietPreviousProof{Digest: fileDietRawDigest(previous.rawProof),
			ObservedAt: previous.proof.ObservedAt, Until: previous.proof.Until}
	}
	proofArchive, err := fileDietArchiveProof(proof)
	if err != nil {
		return nil, err
	}
	files["proof.json.gz"] = proofArchive
	projection := make([]postgresx.ValueProofFile, 0, len(files))
	var size int64
	for name, content := range files {
		size += int64(len(content))
		if len(content) > postgresx.ValueProofFileMax || size > postgresx.ValueProofMaxBytes ||
			len(files) > postgresx.ValueProofMaxFiles {
			return nil, errors.New("File Diet retained proof exceeds PostgreSQL file budget")
		}
		projection = append(projection, postgresx.ValueProofFile{Path: "report/proof/" + name, Content: content})
	}
	slices.SortFunc(projection, func(left, right postgresx.ValueProofFile) int {
		return strings.Compare(left.Path, right.Path)
	})
	if _, err := fileDietLoadProof(files); err != nil {
		return nil, fmt.Errorf("new File Diet proof failed independent replay: %w", err)
	}
	return projection, nil
}

// A frozen historical seed is proof of already observed facts, not an
// acquisition endpoint with mutable repository authority.
func fileDietLoadStartingProof(ctx context.Context, root string, restored []postgresx.ValueProofFile) (fileDietArchive, error) {
	if len(restored) > 0 {
		return fileDietRestore(restored)
	}
	workspace, err := fileDietGitWorkspace(root)
	if err != nil {
		return fileDietArchive{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	seed, err := fileDietSeed(ctx, workspace)
	if err != nil {
		return fileDietArchive{}, err
	}
	if fileDietTime(seed.proof.Until).After(time.Now()) {
		return fileDietArchive{}, errors.New("historical File Diet seed is in the future")
	}
	return seed, nil
}

func fileDietInventoryByRevision(measured fileDietMeasurements, revision string) ([]fileDietFile, error) {
	if !validFileDietSHA(revision) {
		return nil, errors.New("invalid immutable File Diet inventory revision")
	}
	inventory, ok := measured.inventories[revision]
	if !ok {
		return nil, errors.New("missing immutable File Diet inventory")
	}
	return inventory.Files, nil
}
