package algorithm

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

var ErrInvalidTaskConfig = errors.New("analysis task config does not match algorithm schema")

func compileConfigSchema(schemaJSON string) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

func validateTaskConfig(schemaJSON, configJSON string) error {
	if configJSON == "" {
		configJSON = "{}"
	}
	var config any
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("%w: invalid JSON: %v", ErrInvalidTaskConfig, err)
	}
	if schemaJSON == "" {
		return nil
	}

	schema, err := compileConfigSchema(schemaJSON)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfigSchema, err)
	}
	if err := schema.Validate(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTaskConfig, err)
	}
	return nil
}
