package record_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestResponses_NullJSONBecomesEmptySliceAndMap(t *testing.T) {
	// 1. Recognition with explicit "null" JSON fields
	rec := &record.Recognition{
		ID:             "rec_1",
		CameraID:       "cam_1",
		AlgorithmID:    "algo_1",
		CandidatesJSON: "null",
		BoxJSON:        "null",
		BodyBoxJSON:    "null",
		AttributesJSON: "null",
		CapturedAt:     time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}

	recResp := rec.ToResponse()
	if recResp.Candidates == nil {
		t.Errorf("expected non-nil Candidates slice")
	}
	if recResp.Attributes == nil {
		t.Errorf("expected non-nil Attributes map")
	}

	bytesRec, err := json.Marshal(recResp)
	if err != nil {
		t.Fatalf("marshal recResp: %v", err)
	}
	var rawRec map[string]any
	_ = json.Unmarshal(bytesRec, &rawRec)
	if rawRec["candidates"] == nil {
		t.Errorf("expected candidates to serialize to [], got null in JSON: %s", string(bytesRec))
	}
	if rawRec["attributes"] == nil {
		t.Errorf("expected attributes to serialize to {}, got null in JSON: %s", string(bytesRec))
	}

	// 2. Alarm with explicit "null" ExtraJSON
	alarm := &record.Alarm{
		ID:          "alarm_1",
		CameraID:    "cam_1",
		AlgorithmID: "algo_1",
		AlarmType:   "smoke",
		ExtraJSON:   "null",
		CapturedAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	alarmResp := alarm.ToResponse()
	if alarmResp.Extra == nil {
		t.Errorf("expected non-nil Extra map")
	}
	bytesAlarm, _ := json.Marshal(alarmResp)
	var rawAlarm map[string]any
	_ = json.Unmarshal(bytesAlarm, &rawAlarm)
	if rawAlarm["extra"] == nil {
		t.Errorf("expected extra to serialize to {}, got null in JSON: %s", string(bytesAlarm))
	}

	// 3. Capture with explicit "null" AttributesJSON
	capRec := &record.Capture{
		ID:             "cap_1",
		CameraID:       "cam_1",
		TargetType:     "car",
		AttributesJSON: "null",
		CapturedAt:     time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}
	capResp := capRec.ToResponse()
	if capResp.Attributes == nil {
		t.Errorf("expected non-nil Attributes map")
	}
	bytesCap, _ := json.Marshal(capResp)
	var rawCap map[string]any
	_ = json.Unmarshal(bytesCap, &rawCap)
	if rawCap["attributes"] == nil {
		t.Errorf("expected attributes to serialize to {}, got null in JSON: %s", string(bytesCap))
	}
}
