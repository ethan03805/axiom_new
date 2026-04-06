package models

import (
	"context"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// Compile-time interface assertion.
var _ engine.ModelService = (*RegistryAdapter)(nil)

// RegistryAdapter adapts the Registry to the engine.ModelService interface.
type RegistryAdapter struct {
	reg *Registry
}

// NewRegistryAdapter creates an adapter that satisfies engine.ModelService.
func NewRegistryAdapter(reg *Registry) *RegistryAdapter {
	return &RegistryAdapter{reg: reg}
}

func (a *RegistryAdapter) RefreshShipped() error {
	return a.reg.RefreshShipped()
}

func (a *RegistryAdapter) RefreshOpenRouter(ctx context.Context, baseURL string) error {
	return a.reg.RefreshOpenRouter(ctx, baseURL)
}

func (a *RegistryAdapter) RefreshBitNet(ctx context.Context, baseURL string) error {
	return a.reg.RefreshBitNet(ctx, baseURL)
}

func (a *RegistryAdapter) List(tier, family string) ([]engine.ModelInfo, error) {
	entries, err := a.reg.List(tier, family)
	if err != nil {
		return nil, err
	}
	return toEngineModels(entries), nil
}

func (a *RegistryAdapter) Get(id string) (*engine.ModelInfo, error) {
	entry, err := a.reg.Get(id)
	if err != nil {
		return nil, err
	}
	info := toEngineModel(*entry)
	return &info, nil
}

func toEngineModels(entries []state.ModelRegistryEntry) []engine.ModelInfo {
	result := make([]engine.ModelInfo, len(entries))
	for i, e := range entries {
		result[i] = toEngineModel(e)
	}
	return result
}

func toEngineModel(e state.ModelRegistryEntry) engine.ModelInfo {
	var lastUpdated string
	if !e.LastUpdated.IsZero() {
		lastUpdated = e.LastUpdated.Format("2006-01-02T15:04:05Z")
	}
	return engine.ModelInfo{
		ID:                    e.ID,
		Family:                e.Family,
		Source:                e.Source,
		Tier:                  string(e.Tier),
		ContextWindow:         e.ContextWindow,
		MaxOutput:             e.MaxOutput,
		PromptPerMillion:      e.PromptPerMillion,
		CompletionPerMillion:  e.CompletionPerMillion,
		Strengths:             e.Strengths,
		Weaknesses:            e.Weaknesses,
		SupportsTools:         e.SupportsTools,
		SupportsVision:        e.SupportsVision,
		SupportsGrammar:       e.SupportsGrammar,
		RecommendedFor:        e.RecommendedFor,
		NotRecommendedFor:     e.NotRecommendedFor,
		HistoricalSuccessRate: e.HistoricalSuccessRate,
		AvgCostPerTask:        e.AvgCostPerTask,
		LastUpdated:           lastUpdated,
	}
}
