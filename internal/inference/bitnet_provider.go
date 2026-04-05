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

// BitNetProvider implements Provider for a local BitNet inference server.
// Per Architecture Section 19, BitNet uses Falcon3 1.58-bit quantized models
// running on the host at a configurable address. Zero API cost.
type BitNetProvider struct {
	baseURL string
	client  *http.Client
}

// NewBitNetProvider creates a provider targeting a local BitNet server.
func NewBitNetProvider(baseURL string) *BitNetProvider {
	return &BitNetProvider{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 300 * time.Second, // local inference can be slow
		},
	}
}

func (p *BitNetProvider) Name() string { return "bitnet" }

// Available checks whether the local BitNet server is reachable.
func (p *BitNetProvider) Available(ctx context.Context) bool {
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

// Complete sends a chat completion request to the local BitNet server.
// BitNet uses an OpenAI-compatible API. Grammar constraints (GBNF) are
// forwarded for structured output per Architecture Section 19.3.
func (p *BitNetProvider) Complete(ctx context.Context, req ProviderRequest) (*ProviderResponse, error) {
	payload := bitnetRequest{
		Model:       req.Model,
		Messages:    toORMessages(req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if req.GrammarConstraints != nil {
		payload.Grammar = req.GrammarConstraints
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("bitnet: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bitnet: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bitnet: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bitnet: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bitnet: server error %d: %s", resp.StatusCode, string(respBody))
	}

	var orResp chatCompletionResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return nil, fmt.Errorf("bitnet: decode response: %w", err)
	}

	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("bitnet: empty choices in response")
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

// bitnetRequest is the request format for the local BitNet server.
// Compatible with OpenAI API with an optional grammar field for GBNF constraints.
type bitnetRequest struct {
	Model       string              `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature"`
	Grammar     *string             `json:"grammar,omitempty"`
}
