package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/githubnext/gh-cao/server/internal/server"
)

func readOperatorConfig(path string) (*server.LocalOperatorConfig, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // An omitted adapter configuration disables native maintenance.
	}
	content, err := os.ReadFile(path) // #nosec G304 -- explicitly selected private native adapter configuration.
	if err != nil {
		commandLog.Printf("native operator configuration rejected: reason=read_failed")
		return nil, errors.New("read native operator configuration")
	}
	return parseOperatorConfig(content)
}

func parseOperatorConfig(content []byte) (*server.LocalOperatorConfig, error) {
	var config server.LocalOperatorConfig
	if err := json.Unmarshal(content, &config); err != nil {
		commandLog.Printf("native operator configuration rejected: reason=invalid_config")
		return nil, errors.New("invalid native operator configuration")
	}
	commandLog.Printf("loaded native operator configuration: actions_configured=%t", config.OperatorActions != nil)
	return &config, nil
}
