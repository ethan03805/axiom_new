// Package inference implements the Inference Broker per Architecture Section 19.5.
// It centralizes all model access behind engine policy including provider routing,
// budget enforcement, rate limiting, model allowlists, and cost logging.
package inference

import (
	"context"
	"errors"
)

// Provider abstracts a model inference backend (OpenRouter, BitNet).
// Containers never call providers directly; the broker mediates all requests.
type Provider interface {
	// Name returns the provider identifier (e.g. "openrouter", "bitnet").
	Name() string

	// Available reports whether the provider can accept requests.
	Available(ctx context.Context) bool

	// Complete sends a chat completion request and returns the response.
	Complete(ctx context.Context, req ProviderRequest) (*ProviderResponse, error)
}

// ProviderRequest is the provider-level request format.
type ProviderRequest struct {
	Model              string
	Messages           []Message
	MaxTokens          int
	Temperature        float64
	GrammarConstraints *string // GBNF for BitNet structured output
	Stream             bool
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ProviderResponse is the provider-level response format.
type ProviderResponse struct {
	Content      string
	FinishReason string
	InputTokens  int64
	OutputTokens int64
	Model        string
}

// ModelPricing holds per-token costs for a model.
type ModelPricing struct {
	PromptCostPerToken     float64 // USD per input token
	CompletionCostPerToken float64 // USD per output token
}

// Sentinel errors for the inference package.
var (
	ErrBudgetExceeded    = errors.New("inference: budget exceeded")
	ErrRateLimitExceeded = errors.New("inference: per-task rate limit exceeded")
	ErrModelNotAllowed   = errors.New("inference: model not in allowed tier")
	ErrTokenCapExceeded  = errors.New("inference: max_tokens exceeds cap")
	ErrProviderDown      = errors.New("inference: provider unavailable")
	ErrNoProvider        = errors.New("inference: no provider available for request")
)
