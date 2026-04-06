package state

import (
	"testing"
	"time"
)

func TestUpsertModel(t *testing.T) {
	db := testDB(t)

	m := &ModelRegistryEntry{
		ID:                    "anthropic/claude-opus-4.6",
		Family:                "anthropic",
		Source:                "openrouter",
		Tier:                  TierPremium,
		ContextWindow:         1000000,
		MaxOutput:             128000,
		PromptPerMillion:      5.00,
		CompletionPerMillion:  25.00,
		Strengths:             []string{"code-generation", "reasoning"},
		Weaknesses:            []string{"expensive"},
		SupportsTools:         true,
		SupportsVision:        true,
		SupportsGrammar:       false,
		RecommendedFor:        []string{"complex-algorithms", "architectural-patterns"},
		NotRecommendedFor:     []string{"simple-renames"},
	}

	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	// Retrieve and verify
	got, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}

	if got.ID != m.ID {
		t.Errorf("ID = %q, want %q", got.ID, m.ID)
	}
	if got.Family != m.Family {
		t.Errorf("Family = %q, want %q", got.Family, m.Family)
	}
	if got.Source != m.Source {
		t.Errorf("Source = %q, want %q", got.Source, m.Source)
	}
	if got.Tier != m.Tier {
		t.Errorf("Tier = %q, want %q", got.Tier, m.Tier)
	}
	if got.ContextWindow != m.ContextWindow {
		t.Errorf("ContextWindow = %d, want %d", got.ContextWindow, m.ContextWindow)
	}
	if got.MaxOutput != m.MaxOutput {
		t.Errorf("MaxOutput = %d, want %d", got.MaxOutput, m.MaxOutput)
	}
	if got.PromptPerMillion != m.PromptPerMillion {
		t.Errorf("PromptPerMillion = %f, want %f", got.PromptPerMillion, m.PromptPerMillion)
	}
	if got.CompletionPerMillion != m.CompletionPerMillion {
		t.Errorf("CompletionPerMillion = %f, want %f", got.CompletionPerMillion, m.CompletionPerMillion)
	}
	if got.SupportsTools != m.SupportsTools {
		t.Errorf("SupportsTools = %v, want %v", got.SupportsTools, m.SupportsTools)
	}
	if got.SupportsVision != m.SupportsVision {
		t.Errorf("SupportsVision = %v, want %v", got.SupportsVision, m.SupportsVision)
	}
	if got.SupportsGrammar != m.SupportsGrammar {
		t.Errorf("SupportsGrammar = %v, want %v", got.SupportsGrammar, m.SupportsGrammar)
	}
	if len(got.Strengths) != 2 || got.Strengths[0] != "code-generation" {
		t.Errorf("Strengths = %v, want %v", got.Strengths, m.Strengths)
	}
	if len(got.RecommendedFor) != 2 {
		t.Errorf("RecommendedFor len = %d, want 2", len(got.RecommendedFor))
	}
	if got.LastUpdated.IsZero() {
		t.Error("LastUpdated should not be zero")
	}
}

func TestUpsertModelUpdate(t *testing.T) {
	db := testDB(t)

	m := &ModelRegistryEntry{
		ID:                   "anthropic/claude-opus-4.6",
		Family:               "anthropic",
		Source:               "openrouter",
		Tier:                 TierPremium,
		ContextWindow:        1000000,
		MaxOutput:            128000,
		PromptPerMillion:     5.00,
		CompletionPerMillion: 25.00,
	}

	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel (insert): %v", err)
	}

	// Update the pricing
	m.PromptPerMillion = 4.00
	m.CompletionPerMillion = 20.00
	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel (update): %v", err)
	}

	got, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.PromptPerMillion != 4.00 {
		t.Errorf("PromptPerMillion = %f, want 4.00", got.PromptPerMillion)
	}
	if got.CompletionPerMillion != 20.00 {
		t.Errorf("CompletionPerMillion = %f, want 20.00", got.CompletionPerMillion)
	}
}

func TestGetModelNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetModel("nonexistent/model")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListModels(t *testing.T) {
	db := testDB(t)

	models := []*ModelRegistryEntry{
		{ID: "anthropic/claude-opus-4.6", Family: "anthropic", Source: "openrouter", Tier: TierPremium, ContextWindow: 1000000},
		{ID: "openai/gpt-5.4", Family: "openai", Source: "openrouter", Tier: TierPremium, ContextWindow: 1050000},
		{ID: "anthropic/claude-sonnet-4.6", Family: "anthropic", Source: "openrouter", Tier: TierStandard, ContextWindow: 1000000},
		{ID: "bitnet/falcon3-7b-instruct", Family: "falcon", Source: "bitnet", Tier: TierLocal, ContextWindow: 32768},
	}
	for _, m := range models {
		if err := db.UpsertModel(m); err != nil {
			t.Fatalf("UpsertModel(%s): %v", m.ID, err)
		}
	}

	all, err := db.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("ListModels count = %d, want 4", len(all))
	}
}

func TestListModelsByTier(t *testing.T) {
	db := testDB(t)

	models := []*ModelRegistryEntry{
		{ID: "anthropic/claude-opus-4.6", Family: "anthropic", Source: "openrouter", Tier: TierPremium},
		{ID: "openai/gpt-5.4", Family: "openai", Source: "openrouter", Tier: TierPremium},
		{ID: "anthropic/claude-sonnet-4.6", Family: "anthropic", Source: "openrouter", Tier: TierStandard},
		{ID: "bitnet/falcon3-7b-instruct", Family: "falcon", Source: "bitnet", Tier: TierLocal},
	}
	for _, m := range models {
		if err := db.UpsertModel(m); err != nil {
			t.Fatalf("UpsertModel(%s): %v", m.ID, err)
		}
	}

	premium, err := db.ListModelsByTier(TierPremium)
	if err != nil {
		t.Fatalf("ListModelsByTier(premium): %v", err)
	}
	if len(premium) != 2 {
		t.Errorf("premium count = %d, want 2", len(premium))
	}

	local, err := db.ListModelsByTier(TierLocal)
	if err != nil {
		t.Fatalf("ListModelsByTier(local): %v", err)
	}
	if len(local) != 1 {
		t.Errorf("local count = %d, want 1", len(local))
	}
}

func TestListModelsByFamily(t *testing.T) {
	db := testDB(t)

	models := []*ModelRegistryEntry{
		{ID: "anthropic/claude-opus-4.6", Family: "anthropic", Source: "openrouter", Tier: TierPremium},
		{ID: "anthropic/claude-sonnet-4.6", Family: "anthropic", Source: "openrouter", Tier: TierStandard},
		{ID: "openai/gpt-5.4", Family: "openai", Source: "openrouter", Tier: TierPremium},
	}
	for _, m := range models {
		if err := db.UpsertModel(m); err != nil {
			t.Fatalf("UpsertModel(%s): %v", m.ID, err)
		}
	}

	anthropic, err := db.ListModelsByFamily("anthropic")
	if err != nil {
		t.Fatalf("ListModelsByFamily(anthropic): %v", err)
	}
	if len(anthropic) != 2 {
		t.Errorf("anthropic count = %d, want 2", len(anthropic))
	}
}

func TestDeleteModel(t *testing.T) {
	db := testDB(t)

	m := &ModelRegistryEntry{
		ID:     "anthropic/claude-opus-4.6",
		Family: "anthropic",
		Source: "openrouter",
		Tier:   TierPremium,
	}
	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	if err := db.DeleteModel("anthropic/claude-opus-4.6"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}

	_, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteAllModelsBySource(t *testing.T) {
	db := testDB(t)

	models := []*ModelRegistryEntry{
		{ID: "anthropic/claude-opus-4.6", Family: "anthropic", Source: "openrouter", Tier: TierPremium},
		{ID: "openai/gpt-5.4", Family: "openai", Source: "openrouter", Tier: TierPremium},
		{ID: "bitnet/falcon3-7b-instruct", Family: "falcon", Source: "bitnet", Tier: TierLocal},
	}
	for _, m := range models {
		if err := db.UpsertModel(m); err != nil {
			t.Fatalf("UpsertModel(%s): %v", m.ID, err)
		}
	}

	if err := db.DeleteModelsBySource("openrouter"); err != nil {
		t.Fatalf("DeleteModelsBySource: %v", err)
	}

	all, err := db.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("after delete: count = %d, want 1", len(all))
	}
	if all[0].ID != "bitnet/falcon3-7b-instruct" {
		t.Errorf("remaining model = %q, want bitnet/falcon3-7b-instruct", all[0].ID)
	}
}

func TestModelCountByTier(t *testing.T) {
	db := testDB(t)

	models := []*ModelRegistryEntry{
		{ID: "a/1", Family: "a", Source: "openrouter", Tier: TierPremium},
		{ID: "a/2", Family: "a", Source: "openrouter", Tier: TierPremium},
		{ID: "b/1", Family: "b", Source: "openrouter", Tier: TierStandard},
		{ID: "c/1", Family: "c", Source: "bitnet", Tier: TierLocal},
	}
	for _, m := range models {
		if err := db.UpsertModel(m); err != nil {
			t.Fatalf("UpsertModel(%s): %v", m.ID, err)
		}
	}

	counts, err := db.ModelCountByTier()
	if err != nil {
		t.Fatalf("ModelCountByTier: %v", err)
	}
	if counts[TierPremium] != 2 {
		t.Errorf("premium = %d, want 2", counts[TierPremium])
	}
	if counts[TierStandard] != 1 {
		t.Errorf("standard = %d, want 1", counts[TierStandard])
	}
	if counts[TierLocal] != 1 {
		t.Errorf("local = %d, want 1", counts[TierLocal])
	}
}

func TestUpdateModelPerformance(t *testing.T) {
	db := testDB(t)

	m := &ModelRegistryEntry{
		ID:     "anthropic/claude-opus-4.6",
		Family: "anthropic",
		Source: "openrouter",
		Tier:   TierPremium,
	}
	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	successRate := 0.85
	avgCost := 0.42
	if err := db.UpdateModelPerformance("anthropic/claude-opus-4.6", &successRate, &avgCost); err != nil {
		t.Fatalf("UpdateModelPerformance: %v", err)
	}

	got, err := db.GetModel("anthropic/claude-opus-4.6")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.HistoricalSuccessRate == nil || *got.HistoricalSuccessRate != 0.85 {
		t.Errorf("HistoricalSuccessRate = %v, want 0.85", got.HistoricalSuccessRate)
	}
	if got.AvgCostPerTask == nil || *got.AvgCostPerTask != 0.42 {
		t.Errorf("AvgCostPerTask = %v, want 0.42", got.AvgCostPerTask)
	}
}

func TestModelRegistryTableInMigration(t *testing.T) {
	db := testDB(t)

	// Verify the model_registry table exists after migration
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM model_registry").Scan(&count); err != nil {
		t.Fatalf("model_registry table should exist: %v", err)
	}
}

func TestUpsertModelNilSlices(t *testing.T) {
	db := testDB(t)

	m := &ModelRegistryEntry{
		ID:     "test/model",
		Family: "test",
		Source: "shipped",
		Tier:   TierCheap,
	}
	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	got, err := db.GetModel("test/model")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.Strengths != nil {
		t.Errorf("Strengths = %v, want nil", got.Strengths)
	}
	if got.Weaknesses != nil {
		t.Errorf("Weaknesses = %v, want nil", got.Weaknesses)
	}
}

func TestUpsertModelLastUpdatedIsSet(t *testing.T) {
	db := testDB(t)

	before := time.Now().Add(-time.Second)
	m := &ModelRegistryEntry{
		ID:     "test/model",
		Family: "test",
		Source: "shipped",
		Tier:   TierCheap,
	}
	if err := db.UpsertModel(m); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	got, err := db.GetModel("test/model")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.LastUpdated.Before(before) {
		t.Errorf("LastUpdated %v is before test start %v", got.LastUpdated, before)
	}
}
