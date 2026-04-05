package ipc

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessageTypeConstants(t *testing.T) {
	// Verify all message types from Architecture Section 20.4
	types := []MessageType{
		MsgTaskSpec,
		MsgReviewSpec,
		MsgRevisionRequest,
		MsgTaskOutput,
		MsgReviewResult,
		MsgInferenceRequest,
		MsgInferenceResponse,
		MsgLateralMessage,
		MsgActionRequest,
		MsgActionResponse,
		MsgRequestScopeExpansion,
		MsgScopeExpansionResponse,
		MsgContextInvalidationWarning,
		MsgShutdown,
	}

	seen := make(map[MessageType]bool)
	for _, mt := range types {
		if mt == "" {
			t.Error("message type is empty")
		}
		if seen[mt] {
			t.Errorf("duplicate message type: %s", mt)
		}
		seen[mt] = true
	}

	if len(types) != 14 {
		t.Errorf("expected 14 message types, got %d", len(types))
	}
}

func TestEnvelopeMarshalRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	payload := map[string]string{"model_id": "anthropic/claude-4-sonnet"}
	payloadBytes, _ := json.Marshal(payload)

	env := Envelope{
		Type:      MsgInferenceRequest,
		TaskID:    "task-042",
		Timestamp: now,
		Payload:   payloadBytes,
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != MsgInferenceRequest {
		t.Errorf("type = %q, want %q", got.Type, MsgInferenceRequest)
	}
	if got.TaskID != "task-042" {
		t.Errorf("task_id = %q, want %q", got.TaskID, "task-042")
	}
	if !got.Timestamp.Equal(now) {
		t.Errorf("timestamp = %v, want %v", got.Timestamp, now)
	}

	// Verify payload round-trips
	var gotPayload map[string]string
	if err := json.Unmarshal(got.Payload, &gotPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if gotPayload["model_id"] != "anthropic/claude-4-sonnet" {
		t.Errorf("payload model_id = %q", gotPayload["model_id"])
	}
}

func TestEnvelopeJSONFieldNames(t *testing.T) {
	env := Envelope{
		Type:   MsgShutdown,
		TaskID: "task-001",
	}
	data, _ := json.Marshal(env)

	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	required := []string{"type", "task_id", "timestamp"}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}

func TestNewEnvelope(t *testing.T) {
	payload := map[string]string{"reason": "test"}
	env, err := NewEnvelope(MsgShutdown, "task-099", payload)
	if err != nil {
		t.Fatal(err)
	}

	if env.Type != MsgShutdown {
		t.Errorf("type = %q", env.Type)
	}
	if env.TaskID != "task-099" {
		t.Errorf("task_id = %q", env.TaskID)
	}
	if env.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
	if env.Payload == nil {
		t.Error("payload should be set")
	}
}

func TestNewEnvelopeNilPayload(t *testing.T) {
	env, err := NewEnvelope(MsgShutdown, "task-001", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(env.Payload) != "null" {
		t.Errorf("nil payload should marshal to null, got %s", env.Payload)
	}
}

func TestScopeExpansionRequestPayload(t *testing.T) {
	// Architecture Section 10.7 defines this specific message format
	req := ScopeExpansionRequest{
		TaskID:          "task-042",
		AdditionalFiles: []string{"src/routes/api.go", "src/middleware/cors.go"},
		Reason:          "Need to update API route registration to match new handler signature",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var got ScopeExpansionRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.TaskID != "task-042" {
		t.Errorf("task_id = %q", got.TaskID)
	}
	if len(got.AdditionalFiles) != 2 {
		t.Fatalf("additional_files len = %d", len(got.AdditionalFiles))
	}
	if got.AdditionalFiles[0] != "src/routes/api.go" {
		t.Errorf("additional_files[0] = %q", got.AdditionalFiles[0])
	}
}

func TestScopeExpansionResponsePayload(t *testing.T) {
	resp := ScopeExpansionResponse{
		TaskID:        "task-042",
		Status:        "approved",
		ExpandedFiles: []string{"src/routes/api.go", "src/middleware/cors.go"},
		LocksAcquired: true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var got ScopeExpansionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Status != "approved" {
		t.Errorf("status = %q", got.Status)
	}
	if !got.LocksAcquired {
		t.Error("locks_acquired should be true")
	}
}

func TestInferenceRequestPayload(t *testing.T) {
	// Architecture Section 19.2
	req := InferenceRequestPayload{
		TaskID:      "task-042",
		ModelID:     "anthropic/claude-4-sonnet",
		Messages:    []InferenceMessage{{Role: "system", Content: "..."}, {Role: "user", Content: "..."}},
		MaxTokens:   8192,
		Temperature: 0.2,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var got InferenceRequestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.ModelID != "anthropic/claude-4-sonnet" {
		t.Errorf("model_id = %q", got.ModelID)
	}
	if got.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d", got.MaxTokens)
	}
	if len(got.Messages) != 2 {
		t.Errorf("messages len = %d", len(got.Messages))
	}
}
