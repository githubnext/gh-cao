package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func fileDietCadenceSatisfied(previous model.Row, at time.Time) (bool, error) {
	if previous == nil {
		return false, nil
	}
	facts, ok := previous["facts"].([]any)
	if !ok {
		return false, errors.New("retained File Diet evidence is malformed")
	}
	adoption := fileDietTime("2025-11-15T13:36:21Z")
	days := int(at.Sub(adoption) / (24 * time.Hour))
	due := adoption.Add(time.Duration(days) * 24 * time.Hour)
	var cadence bool
	var complete bool
	for _, row := range facts {
		fact, ok := row.(map[string]any)
		if !ok {
			return false, errors.New("retained File Diet fact is malformed")
		}
		switch fact["fact-kind"] {
		case "contract":
			cadence = fmt.Sprint(fact["cadence-days"]) == "1"
		case "coverage":
			complete = complete || fact["coverage-status"] == "complete" &&
				!fileDietTime(fmt.Sprint(fact["at"])).After(due.Add(-24*time.Hour)) &&
				!fileDietTime(fmt.Sprint(fact["end-at"])).Before(due)
		}
	}
	if !cadence {
		return false, errors.New("frozen File Diet cadence changed")
	}
	return !fileDietTime(fmt.Sprint(previous["windowEnd"])).Before(due) && complete, nil
}

func fileDietHistoryPreserved(previous, next model.Row) error {
	if previous == nil {
		return nil
	}
	if previous["id"] != next["id"] || previous["contractDigest"] != next["contractDigest"] ||
		!fileDietTime(fmt.Sprint(next["observedAt"])).After(fileDietTime(fmt.Sprint(previous["observedAt"]))) ||
		fileDietTime(fmt.Sprint(next["windowStart"])).After(fileDietTime(fmt.Sprint(previous["windowStart"]))) ||
		fileDietTime(fmt.Sprint(next["windowEnd"])).Before(fileDietTime(fmt.Sprint(previous["windowEnd"]))) {
		return errors.New("file Diet frozen contract, capture or history conflict")
	}
	newFacts := map[string]map[string]any{}
	for _, item := range next["facts"].([]any) {
		row := item.(map[string]any)
		newFacts[fmt.Sprint(row["fact-kind"], ":", row["parent-id"], ":", row["fact-id"])] = row
	}
	for _, item := range previous["facts"].([]any) {
		old := item.(map[string]any)
		kind := fmt.Sprint(old["fact-kind"])
		if kind != "contract" && kind != "implementation" &&
			kind != "snapshot" && kind != "snapshot-item" && kind != "output" &&
			!strings.HasPrefix(kind, "implementation-") {
			continue
		}
		value := newFacts[fmt.Sprint(old["fact-kind"], ":", old["parent-id"], ":", old["fact-id"])]
		if value == nil {
			return errors.New("accepted File Diet evidence history was removed")
		}
		left, right := maps.Clone(old), maps.Clone(value)
		if kind == "contract" {
			for _, field := range []string{"outputs-available", "repository-state-available", "checks-available"} {
				if old[field] == true && value[field] != true {
					return errors.New("file Diet evidence availability was revoked")
				}
				delete(left, field)
				delete(right, field)
			}
			delete(left, "output-coverage")
			delete(right, "output-coverage")
		}
		if kind == "output" || kind == "implementation" {
			if old["origin"] != value["origin"] &&
				(old["origin"] != "corroborated" || value["origin"] != "receipt-verified") {
				return errors.New("accepted File Diet origin changed")
			}
			delete(left, "origin")
			delete(right, "origin")
		}
		if kind == "output" {
			delete(left, "state")
			delete(right, "state")
		}
		a, _ := json.Marshal(left)
		b, _ := json.Marshal(right)
		if !bytes.Equal(a, b) {
			return errors.New("immutable accepted File Diet evidence changed")
		}
	}
	return nil
}

func fileDietDeliveriesPreserved(before, after fileDietLedger) error {
	if before.Metric != after.Metric {
		return errors.New("frozen File Diet measurement changed")
	}
	byID := map[string]fileDietEvent{}
	for _, event := range after.Events {
		byID[event.ID] = event
	}
	for _, old := range before.Events {
		next, ok := byID[old.ID]
		if !ok || old.At != next.At || old.Value != next.Value || old.Reference != next.Reference {
			return errors.New("preserved File Diet accepted delivery changed")
		}
	}
	return nil
}

func (p Projector) acquireNativeDailyFileDiet(
	ctx context.Context, entry remoteValueEntry, api *remoteValueAPI,
	previous model.Row, restored []postgresx.ValueProofFile, observedAt time.Time,
) (model.Row, []postgresx.ValueProofFile, bool, error) {
	if err := reviewedDailyFileDiet(entry); err != nil {
		return nil, nil, false, err
	}
	satisfied, err := fileDietCadenceSatisfied(previous, observedAt)
	if err != nil || satisfied {
		return nil, nil, satisfied, err
	}
	var prior *fileDietArchive
	if len(restored) != 0 {
		proof, err := fileDietRestore(restored)
		if err != nil {
			return nil, nil, false, err
		}
		prior = &proof
	} else {
		seed, err := fileDietLoadStartingProof(ctx, p.CatalogRoot, nil)
		if err != nil {
			return nil, nil, false, err
		}
		prior = &seed
	}
	if err := p.nativeValueCurrent(ctx); err != nil {
		return nil, nil, false, err
	}
	reader := &nativeFileDietReader{api: api}
	until := observedAt.UTC().Truncate(time.Second).Format(time.RFC3339)
	current, err := fileDietAcquireHistory(ctx, reader.read, until, &prior.proof)
	if err != nil {
		return nil, nil, false, err
	}
	if current.Adoption != prior.proof.Adoption {
		return nil, nil, false, errors.New("file Diet frozen adoption changed")
	}
	if err := fileDietCollectReceipts(ctx, reader.read, &current, prior); err != nil {
		return nil, nil, false, err
	}
	workspace, err := fileDietGitWorkspace(p.CatalogRoot)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	measured, err := fileDietAcquireInventories(ctx, workspace, &current, prior)
	if err != nil {
		return nil, nil, false, err
	}
	oldMeasured := fileDietMeasurements{inventories: prior.inventories}
	oldLedger, err := fileDietReplay(prior.proof, oldMeasured)
	if err != nil {
		return nil, nil, false, err
	}
	files, err := fileDietBuildProofFiles(&current, measured, prior)
	if err != nil {
		return nil, nil, false, err
	}
	ledger, err := fileDietReplay(current, measured)
	if err != nil {
		return nil, nil, false, err
	}
	if err := fileDietDeliveriesPreserved(oldLedger, ledger); err != nil {
		return nil, nil, false, err
	}
	packet, err := fileDietExport(ledger)
	if err != nil {
		return nil, nil, false, err
	}
	if !strings.EqualFold(fmt.Sprint(packet["repository"]), entry.Repository) ||
		packet["workflow"] != entry.Workflow || fileDietTime(fmt.Sprint(packet["observedAt"])).After(time.Now()) ||
		fileDietTime(fmt.Sprint(packet["windowEnd"])).After(observedAt) {
		return nil, nil, false, errors.New("file Diet evidence exceeds reviewed identity or capture cutoff")
	}
	if err := fileDietHistoryPreserved(previous, packet); err != nil {
		return nil, nil, false, err
	}
	return packet, files, false, nil
}
