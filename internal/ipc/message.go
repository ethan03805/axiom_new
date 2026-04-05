// Package ipc implements filesystem-based IPC between the Axiom engine and
// Docker containers per Architecture Section 20.3. Communication uses JSON
// message envelopes written to per-task directories.
package ipc

import (
	"encoding/json"
	"time"
)

// MessageType identifies the kind of IPC message per Architecture Section 20.4.
type MessageType string

const (
	MsgTaskSpec                    MessageType = "task_spec"
	MsgReviewSpec                  MessageType = "review_spec"
	MsgRevisionRequest             MessageType = "revision_request"
	MsgTaskOutput                  MessageType = "task_output"
	MsgReviewResult                MessageType = "review_result"
	MsgInferenceRequest            MessageType = "inference_request"
	MsgInferenceResponse           MessageType = "inference_response"
	MsgLateralMessage              MessageType = "lateral_message"
	MsgActionRequest               MessageType = "action_request"
	MsgActionResponse              MessageType = "action_response"
	MsgRequestScopeExpansion       MessageType = "request_scope_expansion"
	MsgScopeExpansionResponse      MessageType = "scope_expansion_response"
	MsgContextInvalidationWarning  MessageType = "context_invalidation_warning"
	MsgShutdown                    MessageType = "shutdown"
)

// Envelope wraps all IPC messages with a type discriminator and metadata.
// Per Architecture Section 20.3, communication uses JSON files.
type Envelope struct {
	Type      MessageType     `json:"type"`
	TaskID    string          `json:"task_id"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// NewEnvelope creates an Envelope with the current timestamp and a JSON-encoded payload.
func NewEnvelope(msgType MessageType, taskID string, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Type:      msgType,
		TaskID:    taskID,
		Timestamp: time.Now().UTC(),
		Payload:   data,
	}, nil
}

// ScopeExpansionRequest is the payload for request_scope_expansion messages.
// Per Architecture Section 10.7.
type ScopeExpansionRequest struct {
	TaskID          string   `json:"task_id"`
	AdditionalFiles []string `json:"additional_files"`
	Reason          string   `json:"reason"`
}

// ScopeExpansionResponse is the payload for scope_expansion_response messages.
// Per Architecture Section 10.7.
type ScopeExpansionResponse struct {
	TaskID        string   `json:"task_id"`
	Status        string   `json:"status"`
	ExpandedFiles []string `json:"expanded_files,omitempty"`
	LocksAcquired bool     `json:"locks_acquired,omitempty"`
	BlockedBy     string   `json:"blocked_by,omitempty"`
	Message       string   `json:"message,omitempty"`
}

// InferenceRequestPayload is the payload for inference_request messages.
// Per Architecture Section 19.2.
type InferenceRequestPayload struct {
	TaskID             string             `json:"task_id"`
	ModelID            string             `json:"model_id"`
	Messages           []InferenceMessage `json:"messages"`
	MaxTokens          int                `json:"max_tokens"`
	Temperature        float64            `json:"temperature"`
	GrammarConstraints *string            `json:"grammar_constraints"`
}

// InferenceMessage represents a single message in an inference request.
type InferenceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
