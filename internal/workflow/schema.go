package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

var schemaCache sync.Map

func ValidateAgainstSchema(schemaPath string, wf Workflow) error {
	if schemaPath == "" {
		return nil
	}
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return errors.New("invalid schema json")
	}
	compiled, ok := schemaCache.Load(schemaPath)
	if !ok {
		c, err := jsonschema.CompileString(schemaPath, string(data))
		if err != nil {
			return err
		}
		schemaCache.Store(schemaPath, c)
		compiled = c
	}
	raw, _ := json.Marshal(wf)
	var payload interface{}
	_ = json.Unmarshal(raw, &payload)
	return compiled.(*jsonschema.Schema).Validate(payload)
}
