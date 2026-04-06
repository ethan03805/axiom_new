package models

import (
	"context"
	"log/slog"

	"github.com/openaxiom/axiom/internal/inference"
	"github.com/openaxiom/axiom/internal/state"
)

// Registry is the model registry service per Architecture Section 18.
// It aggregates models from shipped data, OpenRouter API, and local BitNet.
type Registry struct {
	db  *state.DB
	log *slog.Logger
}

// NewRegistry creates a new model registry service.
func NewRegistry(db *state.DB, log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{db: db, log: log}
}

// RefreshShipped loads the embedded models.json capability index into the registry.
func (r *Registry) RefreshShipped() error {
	models, err := LoadShippedModels()
	if err != nil {
		return err
	}
	for i := range models {
		if err := r.db.UpsertModel(&models[i]); err != nil {
			return err
		}
	}
	r.log.Info("shipped models loaded", "count", len(models))
	return nil
}

// RefreshOpenRouter fetches the latest model list from OpenRouter and merges
// into the registry. Per Architecture Section 18.4.
func (r *Registry) RefreshOpenRouter(ctx context.Context, baseURL string) error {
	models, err := FetchOpenRouterModels(ctx, baseURL)
	if err != nil {
		return err
	}

	// Merge with shipped data to get strengths/weaknesses/recommendations
	shipped, _ := LoadShippedModels()
	shippedMap := make(map[string]*state.ModelRegistryEntry, len(shipped))
	for i := range shipped {
		shippedMap[shipped[i].ID] = &shipped[i]
	}

	for i := range models {
		if s, ok := shippedMap[models[i].ID]; ok {
			// Merge capability data from shipped into fetched entry
			models[i].Strengths = s.Strengths
			models[i].Weaknesses = s.Weaknesses
			models[i].RecommendedFor = s.RecommendedFor
			models[i].NotRecommendedFor = s.NotRecommendedFor
			models[i].SupportsTools = s.SupportsTools
			models[i].SupportsVision = s.SupportsVision
			models[i].SupportsGrammar = s.SupportsGrammar
			// Use shipped tier classification (more curated) when available
			models[i].Tier = s.Tier
		}
		if err := r.db.UpsertModel(&models[i]); err != nil {
			r.log.Warn("failed to upsert openrouter model", "id", models[i].ID, "error", err)
		}
	}

	r.log.Info("openrouter models refreshed", "count", len(models))
	return nil
}

// RefreshBitNet queries the local BitNet server for loaded models.
func (r *Registry) RefreshBitNet(ctx context.Context, baseURL string) error {
	models, err := FetchBitNetModels(ctx, baseURL)
	if err != nil {
		return err
	}
	for i := range models {
		if err := r.db.UpsertModel(&models[i]); err != nil {
			r.log.Warn("failed to upsert bitnet model", "id", models[i].ID, "error", err)
		}
	}
	r.log.Info("bitnet models refreshed", "count", len(models))
	return nil
}

// List returns models, optionally filtered by tier and/or family.
// Both filters can be combined.
func (r *Registry) List(tier, family string) ([]state.ModelRegistryEntry, error) {
	if tier != "" && family != "" {
		return r.db.ListModelsByTierAndFamily(state.TaskTier(tier), family)
	}
	if tier != "" {
		return r.db.ListModelsByTier(state.TaskTier(tier))
	}
	if family != "" {
		return r.db.ListModelsByFamily(family)
	}
	return r.db.ListModels()
}

// Get retrieves a single model by ID.
func (r *Registry) Get(id string) (*state.ModelRegistryEntry, error) {
	return r.db.GetModel(id)
}

// BrokerMaps extracts pricing and tier maps suitable for the inference broker.
// Per Architecture Section 18, the registry feeds the broker's model allowlist.
func (r *Registry) BrokerMaps() (map[string]inference.ModelPricing, map[string]string) {
	models, err := r.db.ListModels()
	if err != nil {
		r.log.Error("failed to list models for broker maps", "error", err)
		return nil, nil
	}

	pricing := make(map[string]inference.ModelPricing, len(models))
	tiers := make(map[string]string, len(models))

	for _, m := range models {
		pricing[m.ID] = inference.ModelPricing{
			PromptCostPerToken:     m.PromptPerMillion / 1_000_000,
			CompletionCostPerToken: m.CompletionPerMillion / 1_000_000,
		}
		tiers[m.ID] = string(m.Tier)
	}

	return pricing, tiers
}
