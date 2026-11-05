package algorithm

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

func compileConfigSchema(schemaJSON string) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}
