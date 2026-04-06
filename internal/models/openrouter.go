package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

// openRouterModelsResponse is the API response from GET /api/v1/models.
type openRouterModelsResponse struct {
	Data []openRouterModel `json:"data"`
}

type openRouterModel struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Pricing       openRouterPricing    `json:"pricing"`
	ContextLength int                  `json:"context_length"`
	TopProvider   openRouterTopProvider `json:"top_provider"`
}

type openRouterPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

type openRouterTopProvider struct {
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

// FetchOpenRouterModels fetches the model list from the OpenRouter API.
// The baseURL should be the API base (e.g. "https://openrouter.ai/api/v1").
func FetchOpenRouterModels(ctx context.Context, baseURL string) ([]state.ModelRegistryEntry, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openrouter: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: API error %d: %s", resp.StatusCode, string(body))
	}

	var orResp openRouterModelsResponse
	if err := json.Unmarshal(body, &orResp); err != nil {
		return nil, fmt.Errorf("openrouter: decode response: %w", err)
	}

	entries := make([]state.ModelRegistryEntry, 0, len(orResp.Data))
	for _, m := range orResp.Data {
		promptPrice := parsePrice(m.Pricing.Prompt)
		completionPrice := parsePrice(m.Pricing.Completion)

		entry := state.ModelRegistryEntry{
			ID:                   m.ID,
			Family:               extractFamily(m.ID),
			Source:               "openrouter",
			Tier:                 classifyTier(promptPrice, completionPrice),
			ContextWindow:        m.ContextLength,
			MaxOutput:            m.TopProvider.MaxCompletionTokens,
			PromptPerMillion:     promptPrice * 1_000_000,
			CompletionPerMillion: completionPrice * 1_000_000,
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// parsePrice converts a per-token price string to float64.
func parsePrice(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// extractFamily derives the model family from the OpenRouter model ID.
// e.g. "anthropic/claude-opus-4.6" -> "anthropic"
func extractFamily(id string) string {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return "unknown"
}

// classifyTier assigns a tier based on pricing heuristics.
// These thresholds are approximate and align with Architecture Section 10.2.
func classifyTier(promptPerToken, completionPerToken float64) state.TaskTier {
	promptPerMillion := promptPerToken * 1_000_000
	completionPerMillion := completionPerToken * 1_000_000

	if promptPerMillion == 0 && completionPerMillion == 0 {
		return state.TierLocal
	}
	if promptPerMillion >= 2.0 && completionPerMillion >= 10.0 {
		return state.TierPremium
	}
	if promptPerMillion >= 0.20 {
		return state.TierStandard
	}
	return state.TierCheap
}
