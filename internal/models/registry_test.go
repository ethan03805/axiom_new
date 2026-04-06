package models

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openaxiom/axiom/internal/state"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Shipped models.json loading ---

func TestLoadShippedModels(t *testing.T) {
	models, err := LoadShippedModels()
	if err != nil {
		t.Fatalf("LoadShippedModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("LoadShippedModels returned 0 models")
	}

	// Verify we have models in each tier
	tierCounts := make(map[string]int)
	for _, m := range models {
		tierCounts[string(m.Tier)]++
	}
	for _, tier := range []string{"premium", "standard", "cheap", "local"} {
		if tierCounts[tier] == 0 {
			t.Errorf("no models in tier %s", tier)
		}
	}
}

func TestLoadShippedModelsHasExpectedModels(t *testing.T) {
	models, err := LoadShippedModels()
	if err != nil {
		t.Fatalf("LoadShippedModels: %v", err)
	}

	// Build lookup
	byID := make(map[string]*state.ModelRegistryEntry)
	for i := range models {
		byID[models[i].ID] = &models[i]
	}

	// Check a known premium model exists
	opus, ok := byID["anthropic/claude-opus-4.6"]
	if !ok {
		t.Fatal("missing anthropic/claude-opus-4.6 in shipped models")
	}
	if opus.Tier != state.TierPremium {
		t.Errorf("opus tier = %s, want premium", opus.Tier)
	}
	if opus.ContextWindow < 100000 {
		t.Errorf("opus context_window = %d, expected >= 100000", opus.ContextWindow)
	}

	// Check a local BitNet model exists
	hasLocal := false
	for _, m := range models {
		if m.Tier == state.TierLocal {
			hasLocal = true
			break
		}
	}
	if !hasLocal {
		t.Error("no local tier models in shipped models")
	}
}

func TestLoadShippedModelsFieldsPopulated(t *testing.T) {
	models, err := LoadShippedModels()
	if err != nil {
		t.Fatalf("LoadShippedModels: %v", err)
	}

	for _, m := range models {
		if m.ID == "" {
			t.Error("model has empty ID")
		}
		if m.Family == "" {
			t.Errorf("model %s has empty Family", m.ID)
		}
		if m.Tier == "" {
			t.Errorf("model %s has empty Tier", m.ID)
		}
		if m.Source == "" {
			t.Errorf("model %s has empty Source", m.ID)
		}
	}
}

// --- OpenRouter model fetching ---

func TestFetchOpenRouterModels(t *testing.T) {
	// Mock OpenRouter /api/v1/models endpoint
	orResp := openRouterModelsResponse{
		Data: []openRouterModel{
			{
				ID:   "anthropic/claude-opus-4.6",
				Name: "Claude Opus 4.6",
				Pricing: openRouterPricing{
					Prompt:     "0.000005",
					Completion: "0.000025",
				},
				ContextLength:  1000000,
				TopProvider:    openRouterTopProvider{MaxCompletionTokens: 128000},
			},
			{
				ID:   "openai/gpt-5.4",
				Name: "GPT-5.4",
				Pricing: openRouterPricing{
					Prompt:     "0.0000025",
					Completion: "0.000015",
				},
				ContextLength: 1050000,
				TopProvider:   openRouterTopProvider{MaxCompletionTokens: 128000},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orResp)
	}))
	defer server.Close()

	models, err := FetchOpenRouterModels(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("FetchOpenRouterModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "anthropic/claude-opus-4.6" {
		t.Errorf("first model ID = %q", models[0].ID)
	}
	if models[0].PromptPerMillion != 5.00 {
		t.Errorf("prompt pricing = %f, want 5.00", models[0].PromptPerMillion)
	}
	if models[0].ContextWindow != 1000000 {
		t.Errorf("context window = %d, want 1000000", models[0].ContextWindow)
	}
}

func TestFetchOpenRouterModelsServerDown(t *testing.T) {
	_, err := FetchOpenRouterModels(context.Background(), "http://localhost:1")
	if err == nil {
		t.Fatal("expected error when server is down")
	}
}

// --- BitNet model scanning ---

func TestFetchBitNetModels(t *testing.T) {
	// Mock BitNet /v1/models endpoint
	resp := bitnetModelsResponse{
		Data: []bitnetModelEntry{
			{
				ID:      "Falcon3-7B-Instruct-1.58bit",
				OwnedBy: "tiiuae",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	models, err := FetchBitNetModels(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("FetchBitNetModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	if models[0].ID != "bitnet/falcon3-7b-instruct" {
		t.Errorf("model ID = %q, want bitnet/falcon3-7b-instruct", models[0].ID)
	}
	if models[0].Tier != state.TierLocal {
		t.Errorf("tier = %s, want local", models[0].Tier)
	}
	if models[0].Source != "bitnet" {
		t.Errorf("source = %s, want bitnet", models[0].Source)
	}
}

func TestFetchBitNetModelsServerDown(t *testing.T) {
	_, err := FetchBitNetModels(context.Background(), "http://localhost:1")
	if err == nil {
		t.Fatal("expected error when server is down")
	}
}

// --- Registry service ---

func TestRegistryRefreshFromShipped(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	// Refresh with only shipped (no OpenRouter, no BitNet)
	err := reg.RefreshShipped()
	if err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	models, err := db.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected models after shipped refresh")
	}
}

func TestRegistryRefreshFromOpenRouter(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	orResp := openRouterModelsResponse{
		Data: []openRouterModel{
			{
				ID:            "test/model-a",
				Name:          "Model A",
				ContextLength: 100000,
				Pricing:       openRouterPricing{Prompt: "0.000001", Completion: "0.000005"},
				TopProvider:   openRouterTopProvider{MaxCompletionTokens: 50000},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orResp)
	}))
	defer server.Close()

	err := reg.RefreshOpenRouter(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("RefreshOpenRouter: %v", err)
	}

	got, err := db.GetModel("test/model-a")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Source != "openrouter" {
		t.Errorf("source = %s, want openrouter", got.Source)
	}
}

func TestRegistryRefreshFromBitNet(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	resp := bitnetModelsResponse{
		Data: []bitnetModelEntry{
			{ID: "Falcon3-3B-Instruct-1.58bit", OwnedBy: "tiiuae"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	err := reg.RefreshBitNet(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("RefreshBitNet: %v", err)
	}

	got, err := db.GetModel("bitnet/falcon3-3b-instruct")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Tier != state.TierLocal {
		t.Errorf("tier = %s, want local", got.Tier)
	}
}

func TestRegistryList(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	models, err := reg.List("", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("List returned 0 models")
	}
}

func TestRegistryListByTier(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	premium, err := reg.List("premium", "")
	if err != nil {
		t.Fatalf("List premium: %v", err)
	}
	for _, m := range premium {
		if m.Tier != state.TierPremium {
			t.Errorf("model %s has tier %s, expected premium", m.ID, m.Tier)
		}
	}
}

func TestRegistryListByFamily(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	anthropic, err := reg.List("", "anthropic")
	if err != nil {
		t.Fatalf("List anthropic: %v", err)
	}
	for _, m := range anthropic {
		if m.Family != "anthropic" {
			t.Errorf("model %s has family %s, expected anthropic", m.ID, m.Family)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	m, err := reg.Get("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.ID != "anthropic/claude-opus-4.6" {
		t.Errorf("ID = %q, want anthropic/claude-opus-4.6", m.ID)
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	_, err := reg.Get("nonexistent/model")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}

func TestRegistryMergePreservesPerformanceHistory(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	// Load shipped first
	if err := reg.RefreshShipped(); err != nil {
		t.Fatal(err)
	}

	// Set performance data
	rate := 0.90
	cost := 0.50
	if err := db.UpdateModelPerformance("anthropic/claude-opus-4.6", &rate, &cost); err != nil {
		t.Fatal(err)
	}

	// Refresh shipped again — performance data should survive
	if err := reg.RefreshShipped(); err != nil {
		t.Fatal(err)
	}

	m, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatal(err)
	}
	if m.HistoricalSuccessRate == nil || *m.HistoricalSuccessRate != 0.90 {
		t.Errorf("HistoricalSuccessRate = %v, want 0.90", m.HistoricalSuccessRate)
	}
	if m.AvgCostPerTask == nil || *m.AvgCostPerTask != 0.50 {
		t.Errorf("AvgCostPerTask = %v, want 0.50", m.AvgCostPerTask)
	}
}

func TestRegistryListByTierAndFamily(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatalf("RefreshShipped: %v", err)
	}

	// List premium anthropic models only
	models, err := reg.List("premium", "anthropic")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, m := range models {
		if m.Tier != state.TierPremium {
			t.Errorf("model %s has tier %s, expected premium", m.ID, m.Tier)
		}
		if m.Family != "anthropic" {
			t.Errorf("model %s has family %s, expected anthropic", m.ID, m.Family)
		}
	}
	if len(models) == 0 {
		t.Error("expected at least 1 premium anthropic model")
	}
}

func TestRegistryOpenRouterMergeEnrichment(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	// Simulate OpenRouter returning a model that matches a shipped model.
	// The shipped capability data should be merged into the fetched entry.
	orResp := openRouterModelsResponse{
		Data: []openRouterModel{
			{
				ID:            "anthropic/claude-opus-4.6",
				Name:          "Claude Opus 4.6",
				ContextLength: 999999, // OpenRouter may have different values
				Pricing:       openRouterPricing{Prompt: "0.000006", Completion: "0.000030"},
				TopProvider:   openRouterTopProvider{MaxCompletionTokens: 100000},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orResp)
	}))
	defer server.Close()

	err := reg.RefreshOpenRouter(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("RefreshOpenRouter: %v", err)
	}

	got, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}

	// Pricing should come from OpenRouter (live data)
	if got.PromptPerMillion != 6.00 {
		t.Errorf("PromptPerMillion = %f, want 6.00 (from OpenRouter)", got.PromptPerMillion)
	}

	// Tier should come from shipped data (curated)
	if got.Tier != state.TierPremium {
		t.Errorf("Tier = %s, want premium (from shipped)", got.Tier)
	}

	// Capability data should be merged from shipped
	if len(got.Strengths) == 0 {
		t.Error("Strengths should be populated from shipped data")
	}
	if !got.SupportsTools {
		t.Error("SupportsTools should be true from shipped data")
	}
	if !got.SupportsVision {
		t.Error("SupportsVision should be true from shipped data")
	}
	if len(got.RecommendedFor) == 0 {
		t.Error("RecommendedFor should be populated from shipped data")
	}
}

func TestRegistryModelPricingExtraction(t *testing.T) {
	db := testDB(t)
	reg := NewRegistry(db, testLogger())

	if err := reg.RefreshShipped(); err != nil {
		t.Fatal(err)
	}

	// Extract pricing and tier maps for the inference broker
	pricing, tiers := reg.BrokerMaps()
	if len(pricing) == 0 {
		t.Fatal("BrokerMaps returned empty pricing")
	}
	if len(tiers) == 0 {
		t.Fatal("BrokerMaps returned empty tiers")
	}

	// Verify a known model
	p, ok := pricing["anthropic/claude-opus-4.6"]
	if !ok {
		t.Fatal("missing opus in pricing map")
	}
	if p.PromptCostPerToken <= 0 {
		t.Errorf("opus prompt cost = %f, expected > 0", p.PromptCostPerToken)
	}

	tier, ok := tiers["anthropic/claude-opus-4.6"]
	if !ok {
		t.Fatal("missing opus in tiers map")
	}
	if tier != "premium" {
		t.Errorf("opus tier = %s, want premium", tier)
	}
}
