package task

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

var (
	ErrInvalidTaskConfig   = errors.New("analysis task config does not match algorithm schema")
	ErrInvalidConfigSchema = errors.New("invalid algorithm config schema")
)

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
		return apperr.Invalid("INVALID_TASK_CONFIG", fmt.Sprintf("invalid JSON: %v", err), ErrInvalidTaskConfig)
	}
	if schemaJSON == "" {
		return nil
	}

	schema, err := compileConfigSchema(schemaJSON)
	if err != nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", fmt.Sprintf("invalid schema: %v", err), ErrInvalidConfigSchema)
	}
	if err := schema.Validate(config); err != nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", err.Error(), ErrInvalidTaskConfig)
	}
	return nil
}
