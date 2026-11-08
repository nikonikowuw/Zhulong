package task

import (
	"errors"
	"testing"
)

func TestValidateTaskConfig(t *testing.T) {
	const numberSchema = `{
		"type": "object",
		"properties": {"threshold": {"type": "number"}},
		"required": ["threshold"]
	}`

	tests := []struct {
		name       string
		schemaJSON string
		configJSON string
		wantErr    error
	}{
		{
			name:       "both empty defaults to empty document",
			schemaJSON: "",
			configJSON: "",
		},
		{
			name:       "empty schema skips validation",
			schemaJSON: "",
			configJSON: `{"anything": true}`,
		},
		{
			name:       "matching document",
			schemaJSON: numberSchema,
			configJSON: `{"threshold": 0.8}`,
		},
		{
			name:       "config is not json",
			schemaJSON: numberSchema,
			configJSON: `{not-json`,
			wantErr:    errInvalidTaskConfig,
		},
		{
			name:       "schema is not json",
			schemaJSON: `{"bad`,
			configJSON: "{}",
			wantErr:    errInvalidConfigSchema,
		},
		{
			name:       "schema is not resolvable",
			schemaJSON: `{"$ref": "#/definitions/missing"}`,
			configJSON: "{}",
			wantErr:    errInvalidConfigSchema,
		},
		{
			name:       "config violates schema",
			schemaJSON: numberSchema,
			configJSON: `{"threshold": "high"}`,
			wantErr:    errInvalidTaskConfig,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTaskConfig(test.schemaJSON, test.configJSON)
			switch {
			case test.wantErr == nil && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case test.wantErr != nil && !errors.Is(err, test.wantErr):
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
		})
	}
}
