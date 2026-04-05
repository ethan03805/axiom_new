package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenRouterProvider implements Provider for the OpenRouter API.
// Per Architecture Section 19.5, credentials are managed by the engine
// and never exposed to containers.
type OpenRouterProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenRouterProvider creates a provider targeting the given base URL.
// If timeout is zero, defaults to 120 seconds.
func NewOpenRouterProvider(baseURL, apiKey string, opts ...OpenRouterOption) *OpenRouterProvider {
	p := &OpenRouterProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// OpenRouterOption configures the OpenRouter provider.
type OpenRouterOption func(*OpenRouterProvider)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) OpenRouterOption {
	return func(p *OpenRouterProvider) {
		if d > 0 {
			p.client.Timeout = d
		}
	}
}

func (p *OpenRouterProvider) Name() string { return "openrouter" }

// Available checks whether the OpenRouter API is reachable.
func (p *OpenRouterProvider) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL, nil)
	if err != nil {
		return false
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// Complete sends a chat completion request to OpenRouter.
func (p *OpenRouterProvider) Complete(ctx context.Context, req ProviderRequest) (*ProviderResponse, error) {
	orReq := chatCompletionRequest{
		Model:       req.Model,
		Messages:    toORMessages(req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      false,
	}

	body, err := json.Marshal(orReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openrouter: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openrouter: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: API error %d: %s", resp.StatusCode, string(respBody))
	}

	var orResp chatCompletionResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return nil, fmt.Errorf("openrouter: decode response: %w", err)
	}

	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("openrouter: empty choices in response")
	}

	choice := orResp.Choices[0]
	return &ProviderResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		InputTokens:  orResp.Usage.PromptTokens,
		OutputTokens: orResp.Usage.CompletionTokens,
		Model:        orResp.Model,
	}, nil
}

func toORMessages(msgs []Message) []chatMessage {
	out := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = chatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// --- OpenRouter API types (matching real API schema) ---

type chatCompletionRequest struct {
	Model       string              `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature"`
	Stream      bool                `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   chatCompletionUsage    `json:"usage"`
}

type chatCompletionChoice struct {
	Index        int               `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type chatCompletionUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}
