// Package models implements the Model Registry per Architecture Section 18.
// It provides model-aware scheduling data and aggregates models from
// shipped capability index, OpenRouter API, and local BitNet server.
package models

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/openaxiom/axiom/internal/state"
)

//go:embed models.json
var shippedFS embed.FS

// shippedModel is the JSON schema for entries in models.json.
type shippedModel struct {
	ID                   string   `json:"id"`
	Family               string   `json:"family"`
	Source               string   `json:"source"`
	Tier                 string   `json:"tier"`
	ContextWindow        int      `json:"context_window"`
	MaxOutput            int      `json:"max_output"`
	PromptPerMillion     float64  `json:"prompt_per_million"`
	CompletionPerMillion float64  `json:"completion_per_million"`
	Strengths            []string `json:"strengths"`
	Weaknesses           []string `json:"weaknesses"`
	SupportsTools        bool     `json:"supports_tools"`
	SupportsVision       bool     `json:"supports_vision"`
	SupportsGrammar      bool     `json:"supports_grammar"`
	RecommendedFor       []string `json:"recommended_for"`
	NotRecommendedFor    []string `json:"not_recommended_for"`
}

// LoadShippedModels reads the embedded models.json and returns parsed entries.
func LoadShippedModels() ([]state.ModelRegistryEntry, error) {
	data, err := shippedFS.ReadFile("models.json")
	if err != nil {
		return nil, fmt.Errorf("reading shipped models: %w", err)
	}

	var raw []shippedModel
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing shipped models: %w", err)
	}

	entries := make([]state.ModelRegistryEntry, len(raw))
	for i, m := range raw {
		entries[i] = state.ModelRegistryEntry{
			ID:                   m.ID,
			Family:               m.Family,
			Source:               m.Source,
			Tier:                 state.TaskTier(m.Tier),
			ContextWindow:        m.ContextWindow,
			MaxOutput:            m.MaxOutput,
			PromptPerMillion:     m.PromptPerMillion,
			CompletionPerMillion: m.CompletionPerMillion,
			Strengths:            m.Strengths,
			Weaknesses:           m.Weaknesses,
			SupportsTools:        m.SupportsTools,
			SupportsVision:       m.SupportsVision,
			SupportsGrammar:      m.SupportsGrammar,
			RecommendedFor:       m.RecommendedFor,
			NotRecommendedFor:    m.NotRecommendedFor,
		}
	}
	return entries, nil
}
