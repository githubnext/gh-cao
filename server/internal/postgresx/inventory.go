package postgresx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/internal/dashboardquery/workflowmetadata"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var inventoryLog = logger.New("cao:postgresx:inventory")

type inventoryBinding struct {
	field                           string
	inputs                          []string
	defaultPresent                  bool
	defaultValue                    any
	trim, omitEmpty                 bool
	equalsPresent, notEqualsPresent bool
	equals, notEquals               any
}

// resolveInventoryField applies one binding's input-selection, default,
// trim, omit-empty, and equality-projection rules against a single
// observation row. It is a pure function extracted from
// NormalizeInventoryFields's loop body so each binding rule combination is
// independently testable against plain values, without assembling a full
// observation kind or row.
func resolveInventoryField(binding inventoryBinding, input model.Row) (value any, present bool) {
	for _, field := range binding.inputs {
		if candidate, exists := input[field]; exists && candidate != nil {
			value, present = candidate, true
			break
		}
	}
	if !present && binding.defaultPresent {
		value, present = binding.defaultValue, true
	}
	if binding.trim && value != nil {
		value = strings.TrimSpace(fmt.Sprint(value))
	}
	if value == "" && binding.defaultPresent {
		value = binding.defaultValue
	}
	if binding.omitEmpty && value == "" {
		return nil, false
	}
	if binding.equalsPresent {
		value, present = value == binding.equals, true
	}
	if binding.notEqualsPresent {
		value, present = value != binding.notEquals, true
	}
	return value, present
}

// NormalizeInventoryFields applies the scalar observation mappings generated
// from ingestion.json, restricted to fields in the TypeSpec native model.
func NormalizeInventoryFields(name string, input model.Row) (model.Row, error) {
	bindings, exists := inventoryBindings[name]
	if !exists {
		inventoryLog.Printf("inventory normalization rejected kind=%s reason=unregistered", name)
		return nil, errors.New("inventory observation kind is not registered")
	}
	row := model.Row{}
	for _, binding := range bindings {
		if value, present := resolveInventoryField(binding, input); present {
			row[binding.field] = value
		}
	}
	if name == "workflows" {
		if engine, _ := row["workflowEngine"].(string); !workflowmetadata.KnownEngine(engine) {
			delete(row, "workflowEngine")
		}
	}
	inventoryLog.Printf("inventory normalized kind=%s input_fields=%d output_fields=%d", name, len(input), len(row))
	return row, nil
}
