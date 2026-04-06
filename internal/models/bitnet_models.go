package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

// bitnetModelsResponse is the response from GET /v1/models on the BitNet server.
type bitnetModelsResponse struct {
	Data []bitnetModelEntry `json:"data"`
}

type bitnetModelEntry struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// FetchBitNetModels queries the local BitNet server for loaded models.
func FetchBitNetModels(ctx context.Context, baseURL string) ([]state.ModelRegistryEntry, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("bitnet: create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bitnet: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bitnet: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bitnet: server error %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp bitnetModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("bitnet: decode response: %w", err)
	}

	entries := make([]state.ModelRegistryEntry, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		entry := toBitNetRegistryEntry(m)
		entries = append(entries, entry)
	}
	return entries, nil
}

// toBitNetRegistryEntry converts a BitNet model API entry to a registry entry.
func toBitNetRegistryEntry(m bitnetModelEntry) state.ModelRegistryEntry {
	// Normalize model ID to bitnet/ prefix and lowercase
	modelName := strings.ToLower(m.ID)
	// Remove the 1.58bit suffix for a cleaner ID
	modelName = strings.TrimSuffix(modelName, "-1.58bit")
	id := "bitnet/" + modelName

	// Estimate context window based on model size
	contextWindow := 8192
	maxOutput := 2048
	if strings.Contains(modelName, "7b") || strings.Contains(modelName, "10b") {
		contextWindow = 32768
		maxOutput = 4096
	}

	return state.ModelRegistryEntry{
		ID:              id,
		Family:          "falcon",
		Source:          "bitnet",
		Tier:            state.TierLocal,
		ContextWindow:   contextWindow,
		MaxOutput:       maxOutput,
		SupportsGrammar: true,
	}
}
