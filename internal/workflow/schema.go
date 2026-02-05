package workflow

import (
	"encoding/json"
	"errors"
	"os"
)

func ValidateAgainstSchema(schemaPath string, wf Workflow) error {
	if schemaPath == "" {
		return nil
	}
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	// Minimal schema validation placeholder: ensure JSON parses.
	// Can be replaced by a JSON schema validator later.
	if !json.Valid(data) {
		return errors.New("invalid schema json")
	}
	return nil
}
