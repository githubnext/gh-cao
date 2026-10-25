package collect

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func (l Lake) forgetInventoryRepository(repository string) error {
	content, err := os.ReadFile(l.InventoryPath()) // #nosec G304 -- fixed evidence-lake inventory path.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var sources map[string]json.RawMessage
	if err := json.Unmarshal(content, &sources); err != nil {
		return err
	}
	changed := false
	for _, name := range []string{"repositories", "workflows"} {
		raw, exists := sources[name]
		if !exists {
			continue
		}
		var envelope map[string]json.RawMessage
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return err
			}
			if err := json.Unmarshal(envelope["rows"], &rows); err != nil {
				return err
			}
		}
		kept := make([]map[string]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			var owner, repo string
			if err := json.Unmarshal(row["organization"], &owner); err != nil {
				return err
			}
			if err := json.Unmarshal(row["repository"], &repo); err != nil {
				return err
			}
			if strings.EqualFold(strings.TrimSpace(owner)+"/"+strings.TrimSpace(repo), repository) {
				changed = true
				continue
			}
			kept = append(kept, row)
		}
		raw, err = json.Marshal(kept)
		if err != nil {
			return err
		}
		if envelope != nil {
			envelope["rows"] = raw
			raw, err = json.Marshal(envelope)
			if err != nil {
				return err
			}
		}
		sources[name] = raw
	}
	if !changed {
		return nil
	}
	content, err = json.Marshal(sources)
	if err != nil {
		return err
	}
	return WriteFileAtomic(l.InventoryPath(), content)
}
