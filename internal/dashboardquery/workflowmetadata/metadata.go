// Package workflowmetadata projects non-secret fields from gh-aw lockfile headers.
package workflowmetadata

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

type Metadata struct {
	CompilerVersion string
	Engine          string
}

func KnownEngine(id string) bool {
	switch id {
	case "copilot", "claude", "codex", "pi":
		return true
	default:
		return false
	}
}

func Parse(input io.Reader) Metadata {
	const maxHeader = 16 << 10
	reader := bufio.NewReaderSize(io.LimitReader(input, maxHeader+1), maxHeader+1)
	size := 0
	for {
		line, err := reader.ReadSlice('\n')
		size += len(line)
		if size > maxHeader || (err != nil && err != io.EOF) {
			return Metadata{}
		}
		text := strings.TrimSpace(string(line))
		if raw, ok := strings.CutPrefix(text, "# gh-aw-metadata: "); ok {
			var fields struct {
				Version json.RawMessage `json:"compiler_version"`
				AgentID json.RawMessage `json:"agent_id"`
			}
			if json.Unmarshal([]byte(raw), &fields) != nil {
				return Metadata{}
			}
			var version, engine string
			_ = json.Unmarshal(fields.Version, &version)
			if json.Unmarshal(fields.AgentID, &engine) != nil || !KnownEngine(engine) {
				engine = ""
			}
			return Metadata{CompilerVersion: version, Engine: engine}
		}
		if err == io.EOF || (text != "" && !strings.HasPrefix(text, "#")) {
			return Metadata{}
		}
	}
}
