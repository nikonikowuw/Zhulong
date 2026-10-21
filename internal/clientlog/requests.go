// Package clientlog receives browser-side error events through an
// unauthenticated endpoint.
//
// The endpoint is public because browser errors peak exactly when a session has
// expired. That makes it an ingestion surface reachable by anyone who can talk
// to the device, so every layer bounds what it accepts: entry count, event-ID
// shape, field count, field size, body size, per-IP rate and process-wide
// request/byte/concurrency budgets.
//
// Two rules keep the surface from becoming a liability:
//
//   - Client input is validated and then discarded; it is never echoed back to
//     the caller and never written verbatim to a log. Only allowlisted event
//     IDs, allowlisted typed field values, the server request ID and a masked
//     client IP reach the log.
//   - Nothing here creates an audit record. Audit is reserved for operations
//     with a responsible actor; an anonymous error report has none.
package clientlog

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxEntriesPerRequest bounds a single batch.
	MaxEntriesPerRequest = 50
	// MaxEventIDBytes bounds the stable event identifier.
	MaxEventIDBytes = 64
	// MaxFieldsPerEntry bounds allowlisted field count.
	MaxFieldsPerEntry = 16
	// MaxFieldValueBytes bounds a single string field value.
	MaxFieldValueBytes = 256
	// MaxRequestBodyBytes bounds the whole request body.
	MaxRequestBodyBytes int64 = 64 << 10
	// MaxDroppedCount is the largest client-reported drop counter.
	MaxDroppedCount uint64 = 1<<32 - 1

	levelWarn  = "warn"
	levelError = "error"
)

// eventIDPattern accepts stable event identifiers only. It deliberately forbids
// spaces and punctuation beyond dot/dash/underscore so prose cannot be sent:
// "server.error" is an event, "failed to load cameras" is not.
var eventIDPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// routePattern accepts an allowlisted route category such as "cameras.list".
// Query strings, fragments and dynamic path segments (slashes) are rejected.
var routePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// allowedFields is the field-key allowlist. Anything else is rejected rather
// than dropped silently, so a new client field cannot start flowing without a
// server-side review.
var allowedFields = map[string]struct{}{
	"status":        {},
	"code":          {},
	"method":        {},
	"attempt":       {},
	"durationMs":    {},
	"boundary":      {},
	"component":     {},
	"queryKey":      {},
	"eventKind":     {},
	"retryCount":    {},
	"eventSource":   {},
	"isRetryable":   {},
	"httpStatus":    {},
	"reasonClass":   {},
	"routeCategory": {},
}

// entry is one client event as accepted by the schema.
type entry struct {
	Level  string                     `json:"level"`
	Msg    string                     `json:"msg"`
	TS     string                     `json:"ts,omitempty"`
	Route  string                     `json:"route,omitempty"`
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
}

// request is the wire payload.
type request struct {
	Dropped *uint64 `json:"dropped,omitempty"`
	Entries []entry `json:"entries"`
}

// validatedEntry is an accepted event reduced to log-safe scalars.
type validatedEntry struct {
	Level  string
	Msg    string
	Route  string
	Fields []Field
}

// Field is one allowlisted, log-safe scalar.
type Field struct {
	Key   string
	Value string
}

// validationError reports a rejected request. The handler turns it into a 422
// envelope; the message never repeats client content.
type validationError struct {
	field string
	code  string
}

func (e *validationError) Error() string { return e.field + ": " + e.code }

// validate parses and validates the raw body. It returns the accepted entries
// plus the client-reported drop count (nil when absent).
func validate(body []byte) ([]validatedEntry, *uint64, error) {
	var payload request
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, &validationError{field: "body", code: "INVALID_FORMAT"}
	}
	// Reject trailing content so one body cannot smuggle a second document.
	if decoder.More() {
		return nil, nil, &validationError{field: "body", code: "INVALID_FORMAT"}
	}

	if len(payload.Entries) == 0 {
		return nil, nil, &validationError{field: "entries", code: "REQUIRED"}
	}
	if len(payload.Entries) > MaxEntriesPerRequest {
		return nil, nil, &validationError{field: "entries", code: "MAX_ITEMS"}
	}
	if payload.Dropped != nil && *payload.Dropped > MaxDroppedCount {
		return nil, nil, &validationError{field: "dropped", code: "MAX_VALUE"}
	}

	accepted := make([]validatedEntry, 0, len(payload.Entries))
	for index, raw := range payload.Entries {
		validated, err := validateEntry(raw, index)
		if err != nil {
			return nil, nil, err
		}
		accepted = append(accepted, validated)
	}
	return accepted, payload.Dropped, nil
}

func validateEntry(raw entry, index int) (validatedEntry, error) {
	if raw.Level != levelWarn && raw.Level != levelError {
		return validatedEntry{}, &validationError{
			field: "entries[" + strconv.Itoa(index) + "].level",
			code:  "INVALID_VALUE",
		}
	}

	if len(raw.Msg) == 0 {
		return validatedEntry{}, &validationError{
			field: "entries[" + strconv.Itoa(index) + "].msg",
			code:  "REQUIRED",
		}
	}
	if len(raw.Msg) > MaxEventIDBytes || !eventIDPattern.MatchString(raw.Msg) {
		return validatedEntry{}, &validationError{
			field: "entries[" + strconv.Itoa(index) + "].msg",
			code:  "INVALID_EVENT_ID",
		}
	}

	if raw.Route != "" && !routePattern.MatchString(raw.Route) {
		return validatedEntry{}, &validationError{
			field: "entries[" + strconv.Itoa(index) + "].route",
			code:  "INVALID_ROUTE_CATEGORY",
		}
	}

	if len(raw.Fields) > MaxFieldsPerEntry {
		return validatedEntry{}, &validationError{
			field: "entries[" + strconv.Itoa(index) + "].fields",
			code:  "MAX_ITEMS",
		}
	}

	fields := make([]Field, 0, len(raw.Fields))
	for key, rawValue := range raw.Fields {
		if _, ok := allowedFields[key]; !ok {
			return validatedEntry{}, &validationError{
				field: "entries[" + strconv.Itoa(index) + "].fields." + key,
				code:  "UNKNOWN_FIELD",
			}
		}
		value, err := scalarValue(rawValue)
		if err != nil {
			return validatedEntry{}, &validationError{
				field: "entries[" + strconv.Itoa(index) + "].fields." + key,
				code:  err.(*validationError).code,
			}
		}
		fields = append(fields, Field{Key: key, Value: value})
	}

	return validatedEntry{Level: raw.Level, Msg: raw.Msg, Route: raw.Route, Fields: fields}, nil
}

// scalarValue accepts only a bounded string or a JSON number and returns its
// textual form. Objects and arrays are rejected: they are unbounded and have no
// reviewed log representation.
func scalarValue(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", &validationError{code: "INVALID_VALUE"}
	}
	if trimmed == "null" {
		return "", &validationError{code: "INVALID_VALUE"}
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", &validationError{code: "INVALID_VALUE"}
		}
		if len(text) > MaxFieldValueBytes {
			return "", &validationError{code: "MAX_LENGTH"}
		}
		if strings.ContainsAny(text, "\r\n") {
			return "", &validationError{code: "INVALID_VALUE"}
		}
		return text, nil
	}

	if trimmed[0] == '{' || trimmed[0] == '[' {
		return "", &validationError{code: "INVALID_VALUE"}
	}

	// Number or boolean literal: bounded length is the only requirement.
	if len(trimmed) > MaxFieldValueBytes {
		return "", &validationError{code: "MAX_LENGTH"}
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		// Not a number; allow JSON booleans as their literal text.
		if trimmed == "true" || trimmed == "false" {
			return trimmed, nil
		}
		return "", &validationError{code: "INVALID_VALUE"}
	}
	return number.String(), nil
}
