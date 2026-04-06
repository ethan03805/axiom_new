package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// UpsertModel inserts or replaces a model registry entry.
// Per Architecture Section 18.4, the registry is refreshable from multiple sources.
func (d *DB) UpsertModel(m *ModelRegistryEntry) error {
	strengths := encodeJSONArray(m.Strengths)
	weaknesses := encodeJSONArray(m.Weaknesses)
	recommendedFor := encodeJSONArray(m.RecommendedFor)
	notRecommendedFor := encodeJSONArray(m.NotRecommendedFor)

	_, err := d.Exec(`INSERT INTO model_registry
		(id, family, source, tier, context_window, max_output,
		 prompt_per_million, completion_per_million,
		 strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		 recommended_for, not_recommended_for,
		 historical_success_rate, avg_cost_per_task, last_updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		 family = excluded.family,
		 source = excluded.source,
		 tier = excluded.tier,
		 context_window = excluded.context_window,
		 max_output = excluded.max_output,
		 prompt_per_million = excluded.prompt_per_million,
		 completion_per_million = excluded.completion_per_million,
		 strengths = excluded.strengths,
		 weaknesses = excluded.weaknesses,
		 supports_tools = excluded.supports_tools,
		 supports_vision = excluded.supports_vision,
		 supports_grammar = excluded.supports_grammar,
		 recommended_for = excluded.recommended_for,
		 not_recommended_for = excluded.not_recommended_for,
		 historical_success_rate = COALESCE(excluded.historical_success_rate, model_registry.historical_success_rate),
		 avg_cost_per_task = COALESCE(excluded.avg_cost_per_task, model_registry.avg_cost_per_task),
		 last_updated = CURRENT_TIMESTAMP`,
		m.ID, m.Family, m.Source, string(m.Tier),
		m.ContextWindow, m.MaxOutput,
		m.PromptPerMillion, m.CompletionPerMillion,
		strengths, weaknesses,
		boolToInt(m.SupportsTools), boolToInt(m.SupportsVision), boolToInt(m.SupportsGrammar),
		recommendedFor, notRecommendedFor,
		m.HistoricalSuccessRate, m.AvgCostPerTask,
	)
	if err != nil {
		return fmt.Errorf("upserting model %s: %w", m.ID, err)
	}
	return nil
}

// GetModel retrieves a model by ID. Returns ErrNotFound if not present.
func (d *DB) GetModel(id string) (*ModelRegistryEntry, error) {
	row := d.QueryRow(`SELECT
		id, family, source, tier, context_window, max_output,
		prompt_per_million, completion_per_million,
		strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		recommended_for, not_recommended_for,
		historical_success_rate, avg_cost_per_task, last_updated
		FROM model_registry WHERE id = ?`, id)
	return scanModel(row)
}

// ListModels returns all registered models ordered by tier then ID.
func (d *DB) ListModels() ([]ModelRegistryEntry, error) {
	rows, err := d.Query(`SELECT
		id, family, source, tier, context_window, max_output,
		prompt_per_million, completion_per_million,
		strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		recommended_for, not_recommended_for,
		historical_success_rate, avg_cost_per_task, last_updated
		FROM model_registry ORDER BY
		CASE tier WHEN 'premium' THEN 0 WHEN 'standard' THEN 1 WHEN 'cheap' THEN 2 WHEN 'local' THEN 3 END,
		id`)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// ListModelsByTier returns models matching a specific tier.
func (d *DB) ListModelsByTier(tier TaskTier) ([]ModelRegistryEntry, error) {
	rows, err := d.Query(`SELECT
		id, family, source, tier, context_window, max_output,
		prompt_per_million, completion_per_million,
		strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		recommended_for, not_recommended_for,
		historical_success_rate, avg_cost_per_task, last_updated
		FROM model_registry WHERE tier = ? ORDER BY id`, string(tier))
	if err != nil {
		return nil, fmt.Errorf("listing models by tier %s: %w", tier, err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// ListModelsByTierAndFamily returns models matching both a tier and a family.
func (d *DB) ListModelsByTierAndFamily(tier TaskTier, family string) ([]ModelRegistryEntry, error) {
	rows, err := d.Query(`SELECT
		id, family, source, tier, context_window, max_output,
		prompt_per_million, completion_per_million,
		strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		recommended_for, not_recommended_for,
		historical_success_rate, avg_cost_per_task, last_updated
		FROM model_registry WHERE tier = ? AND family = ? ORDER BY id`, string(tier), family)
	if err != nil {
		return nil, fmt.Errorf("listing models by tier %s and family %s: %w", tier, family, err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// ListModelsByFamily returns models matching a specific family.
func (d *DB) ListModelsByFamily(family string) ([]ModelRegistryEntry, error) {
	rows, err := d.Query(`SELECT
		id, family, source, tier, context_window, max_output,
		prompt_per_million, completion_per_million,
		strengths, weaknesses, supports_tools, supports_vision, supports_grammar,
		recommended_for, not_recommended_for,
		historical_success_rate, avg_cost_per_task, last_updated
		FROM model_registry WHERE family = ? ORDER BY id`, family)
	if err != nil {
		return nil, fmt.Errorf("listing models by family %s: %w", family, err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// DeleteModel removes a model from the registry.
func (d *DB) DeleteModel(id string) error {
	_, err := d.Exec("DELETE FROM model_registry WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting model %s: %w", id, err)
	}
	return nil
}

// DeleteModelsBySource removes all models from a given source.
func (d *DB) DeleteModelsBySource(source string) error {
	_, err := d.Exec("DELETE FROM model_registry WHERE source = ?", source)
	if err != nil {
		return fmt.Errorf("deleting models by source %s: %w", source, err)
	}
	return nil
}

// ModelCountByTier returns model counts grouped by tier.
func (d *DB) ModelCountByTier() (map[TaskTier]int, error) {
	rows, err := d.Query("SELECT tier, COUNT(*) FROM model_registry GROUP BY tier")
	if err != nil {
		return nil, fmt.Errorf("counting models by tier: %w", err)
	}
	defer rows.Close()

	counts := make(map[TaskTier]int)
	for rows.Next() {
		var tier string
		var count int
		if err := rows.Scan(&tier, &count); err != nil {
			return nil, fmt.Errorf("scanning tier count: %w", err)
		}
		counts[TaskTier(tier)] = count
	}
	return counts, rows.Err()
}

// UpdateModelPerformance updates the historical performance metrics for a model.
// Per Architecture Section 18.5.
func (d *DB) UpdateModelPerformance(id string, successRate, avgCost *float64) error {
	_, err := d.Exec(`UPDATE model_registry
		SET historical_success_rate = ?, avg_cost_per_task = ?, last_updated = CURRENT_TIMESTAMP
		WHERE id = ?`, successRate, avgCost, id)
	if err != nil {
		return fmt.Errorf("updating model performance %s: %w", id, err)
	}
	return nil
}

// --- scan helpers ---

func scanModel(row *sql.Row) (*ModelRegistryEntry, error) {
	var m ModelRegistryEntry
	var tier, lastUpdated string
	var strengths, weaknesses, recommendedFor, notRecommendedFor *string
	var supportsTools, supportsVision, supportsGrammar int

	err := row.Scan(
		&m.ID, &m.Family, &m.Source, &tier,
		&m.ContextWindow, &m.MaxOutput,
		&m.PromptPerMillion, &m.CompletionPerMillion,
		&strengths, &weaknesses,
		&supportsTools, &supportsVision, &supportsGrammar,
		&recommendedFor, &notRecommendedFor,
		&m.HistoricalSuccessRate, &m.AvgCostPerTask,
		&lastUpdated,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning model: %w", err)
	}

	m.Tier = TaskTier(tier)
	m.SupportsTools = supportsTools != 0
	m.SupportsVision = supportsVision != 0
	m.SupportsGrammar = supportsGrammar != 0
	m.Strengths = decodeJSONArray(strengths)
	m.Weaknesses = decodeJSONArray(weaknesses)
	m.RecommendedFor = decodeJSONArray(recommendedFor)
	m.NotRecommendedFor = decodeJSONArray(notRecommendedFor)
	m.LastUpdated = parseTime(lastUpdated)

	return &m, nil
}

func scanModels(rows *sql.Rows) ([]ModelRegistryEntry, error) {
	var models []ModelRegistryEntry
	for rows.Next() {
		var m ModelRegistryEntry
		var tier, lastUpdated string
		var strengths, weaknesses, recommendedFor, notRecommendedFor *string
		var supportsTools, supportsVision, supportsGrammar int

		err := rows.Scan(
			&m.ID, &m.Family, &m.Source, &tier,
			&m.ContextWindow, &m.MaxOutput,
			&m.PromptPerMillion, &m.CompletionPerMillion,
			&strengths, &weaknesses,
			&supportsTools, &supportsVision, &supportsGrammar,
			&recommendedFor, &notRecommendedFor,
			&m.HistoricalSuccessRate, &m.AvgCostPerTask,
			&lastUpdated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning model row: %w", err)
		}

		m.Tier = TaskTier(tier)
		m.SupportsTools = supportsTools != 0
		m.SupportsVision = supportsVision != 0
		m.SupportsGrammar = supportsGrammar != 0
		m.Strengths = decodeJSONArray(strengths)
		m.Weaknesses = decodeJSONArray(weaknesses)
		m.RecommendedFor = decodeJSONArray(recommendedFor)
		m.NotRecommendedFor = decodeJSONArray(notRecommendedFor)
		m.LastUpdated = parseTime(lastUpdated)

		models = append(models, m)
	}
	return models, rows.Err()
}

// --- JSON array helpers ---

func encodeJSONArray(ss []string) *string {
	if ss == nil {
		return nil
	}
	data, _ := json.Marshal(ss)
	s := string(data)
	return &s
}

func decodeJSONArray(s *string) []string {
	if s == nil {
		return nil
	}
	var ss []string
	if err := json.Unmarshal([]byte(*s), &ss); err != nil {
		return nil
	}
	return ss
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
