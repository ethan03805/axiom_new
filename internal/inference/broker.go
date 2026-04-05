package inference

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// Compile-time interface assertion.
var _ engine.InferenceService = (*Broker)(nil)

// tierOrder defines the hierarchy of model tiers for allowlist checks.
// A task at tier N may use models at tier N or below.
var tierOrder = map[string]int{
	"local":    0,
	"cheap":    1,
	"standard": 2,
	"premium":  3,
}

// BrokerConfig holds the dependencies for creating a Broker.
type BrokerConfig struct {
	Config        *config.Config
	DB            *state.DB
	Bus           *events.Bus
	Log           *slog.Logger
	CloudProvider Provider
	LocalProvider Provider
	ModelPricing  map[string]ModelPricing // model_id → pricing
	ModelTiers    map[string]string       // model_id → tier name
}

// Broker is the central inference brokering service per Architecture Section 19.5.
// It implements engine.InferenceService and mediates ALL model API calls.
// Containers submit requests via IPC; the broker validates, routes, executes,
// and logs every request.
type Broker struct {
	cfg           *config.Config
	db            *state.DB
	bus           *events.Bus
	log           *slog.Logger
	cloud         Provider
	local         Provider
	modelPricing  map[string]ModelPricing
	modelTiers    map[string]string
	budget        *BudgetEnforcer
	rateLimiter   *RateLimiter
}

// NewBroker creates a Broker from its configuration and dependencies.
func NewBroker(bc BrokerConfig) *Broker {
	return &Broker{
		cfg:          bc.Config,
		db:           bc.DB,
		bus:          bc.Bus,
		log:          bc.Log,
		cloud:        bc.CloudProvider,
		local:        bc.LocalProvider,
		modelPricing: bc.ModelPricing,
		modelTiers:   bc.ModelTiers,
		budget:       NewBudgetEnforcer(bc.Config.Budget.MaxUSD, bc.Config.Budget.WarnAtPercent),
		rateLimiter:  NewRateLimiter(bc.Config.Inference.MaxRequestsTask),
	}
}

// Available reports whether at least one provider can accept requests.
func (b *Broker) Available() bool {
	ctx := context.Background()
	if b.cloud != nil && b.cloud.Available(ctx) {
		return true
	}
	if b.local != nil && b.local.Available(ctx) {
		return true
	}
	return false
}

// Infer validates, routes, executes, and logs an inference request.
// This is the single entry point for all model access in the engine.
// Per Architecture Section 19.5, the broker enforces:
//  1. Model allowlist (requested model must be in task's allowed tier)
//  2. Budget pre-authorization (max_tokens * pricing <= remaining budget)
//  3. Per-task rate limits (default 50 requests per task)
//  4. Token cap enforcement
func (b *Broker) Infer(ctx context.Context, req engine.InferenceRequest) (*engine.InferenceResponse, error) {
	// --- 1. Token cap check ---
	if req.MaxTokens > b.cfg.Inference.TokenCapPerReq {
		return nil, ErrTokenCapExceeded
	}

	// --- 2. Model allowlist + tier check ---
	modelTier, known := b.modelTiers[req.ModelID]
	if !known {
		return nil, ErrModelNotAllowed
	}
	if !tierAllowed(req.Tier, modelTier) {
		return nil, ErrModelNotAllowed
	}

	// --- 3. Budget pre-authorization ---
	pricing := b.modelPricing[req.ModelID]
	if err := b.budget.Authorize(req.MaxTokens, pricing); err != nil {
		return nil, err
	}

	// --- 4. Rate limit check ---
	if err := b.rateLimiter.Allow(req.TaskID); err != nil {
		return nil, err
	}

	// --- 5. Build provider request ---
	messages := b.buildMessages(req)
	provReq := ProviderRequest{
		Model:              req.ModelID,
		Messages:           messages,
		MaxTokens:          req.MaxTokens,
		Temperature:        req.Temperature,
		GrammarConstraints: req.GrammarConstraints,
	}

	// --- 6. Emit inference_requested event ---
	b.bus.Publish(events.EngineEvent{
		Type:      events.InferenceRequested,
		RunID:     req.RunID,
		TaskID:    req.TaskID,
		AgentType: req.AgentType,
		Timestamp: time.Now().UTC(),
		Details:   map[string]any{"model_id": req.ModelID, "max_tokens": req.MaxTokens},
	})

	// --- 7. Route to provider ---
	provider, err := b.selectProvider(ctx, modelTier)
	if err != nil {
		b.emitProviderUnavailable(req, modelTier)
		b.emitInferenceFailed(req, err)
		return nil, err
	}

	// --- 8. Execute request ---
	startTime := time.Now()
	provResp, err := provider.Complete(ctx, provReq)
	latencyMs := time.Since(startTime).Milliseconds()
	if err != nil {
		b.emitInferenceFailed(req, err)
		return nil, fmt.Errorf("inference: provider %s: %w", provider.Name(), err)
	}

	// --- 9. Calculate actual cost ---
	actualCost := float64(provResp.InputTokens)*pricing.PromptCostPerToken +
		float64(provResp.OutputTokens)*pricing.CompletionCostPerToken

	// --- 9. Record cost in budget tracker ---
	b.budget.Record(actualCost)

	// --- 10. Log cost to database ---
	b.logCost(req, provResp, actualCost)

	// --- 11. Emit completion event ---
	b.emitInferenceCompleted(req, provResp, provider.Name(), actualCost, latencyMs)

	// --- 12. Check budget warning/exceeded thresholds ---
	if b.budget.Exceeded() {
		b.bus.Publish(events.EngineEvent{
			Type:    events.BudgetExceeded,
			RunID:   req.RunID,
			TaskID:  req.TaskID,
			Details: map[string]any{"spent": b.budget.Spent(), "max": b.cfg.Budget.MaxUSD},
		})
	} else if b.budget.WarnReached() {
		b.bus.Publish(events.EngineEvent{
			Type:    events.BudgetWarning,
			RunID:   req.RunID,
			TaskID:  req.TaskID,
			Details: map[string]any{"spent": b.budget.Spent(), "max": b.cfg.Budget.MaxUSD},
		})
	}

	return &engine.InferenceResponse{
		Content:      provResp.Content,
		InputTokens:  provResp.InputTokens,
		OutputTokens: provResp.OutputTokens,
		CostUSD:      actualCost,
		ModelID:      provResp.Model,
		FinishReason: provResp.FinishReason,
		ProviderName: provider.Name(),
	}, nil
}

// buildMessages converts engine request to provider messages.
// Messages field takes precedence over legacy Prompt field.
func (b *Broker) buildMessages(req engine.InferenceRequest) []Message {
	if len(req.Messages) > 0 {
		msgs := make([]Message, len(req.Messages))
		for i, m := range req.Messages {
			msgs[i] = Message{Role: m.Role, Content: m.Content}
		}
		return msgs
	}
	if req.Prompt != "" {
		return []Message{{Role: "user", Content: req.Prompt}}
	}
	return nil
}

// selectProvider picks the right provider based on model tier and availability.
// Local-tier tasks always use BitNet. Other tiers prefer cloud but fall back
// to local for local-eligible models when cloud is down.
func (b *Broker) selectProvider(ctx context.Context, modelTier string) (Provider, error) {
	if modelTier == "local" {
		if b.local != nil && b.local.Available(ctx) {
			return b.local, nil
		}
		return nil, ErrProviderDown
	}

	// Cloud tiers: try cloud first
	if b.cloud != nil && b.cloud.Available(ctx) {
		return b.cloud, nil
	}

	// Cloud down — no fallback for non-local tiers
	return nil, ErrProviderDown
}

// tierAllowed checks whether a model at modelTier is usable by a task at taskTier.
// A task can use models at its tier or below.
func tierAllowed(taskTier, modelTier string) bool {
	tl, tok := tierOrder[taskTier]
	ml, mok := tierOrder[modelTier]
	if !tok || !mok {
		return false
	}
	return ml <= tl
}

// logCost persists a cost entry to the database.
func (b *Broker) logCost(req engine.InferenceRequest, resp *ProviderResponse, costUSD float64) {
	taskID := req.TaskID
	var attemptID *int64
	if req.AttemptID > 0 {
		a := req.AttemptID
		attemptID = &a
	}

	entry := &state.CostLogEntry{
		RunID:        req.RunID,
		TaskID:       &taskID,
		AttemptID:    attemptID,
		AgentType:    req.AgentType,
		ModelID:      req.ModelID,
		InputTokens:  &resp.InputTokens,
		OutputTokens: &resp.OutputTokens,
		CostUSD:      costUSD,
	}

	if _, err := b.db.CreateCostLog(entry); err != nil {
		b.log.Error("failed to log cost", "error", err, "run_id", req.RunID, "task_id", req.TaskID)
	}
}

func (b *Broker) emitInferenceCompleted(req engine.InferenceRequest, resp *ProviderResponse, providerName string, cost float64, latencyMs int64) {
	b.bus.Publish(events.EngineEvent{
		Type:      events.InferenceCompleted,
		RunID:     req.RunID,
		TaskID:    req.TaskID,
		AgentType: req.AgentType,
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"model_id":      req.ModelID,
			"provider":      providerName,
			"input_tokens":  resp.InputTokens,
			"output_tokens": resp.OutputTokens,
			"cost_usd":      cost,
			"finish_reason": resp.FinishReason,
			"latency_ms":    latencyMs,
		},
	})
}

func (b *Broker) emitProviderUnavailable(req engine.InferenceRequest, tier string) {
	b.bus.Publish(events.EngineEvent{
		Type:      events.ProviderUnavailable,
		RunID:     req.RunID,
		TaskID:    req.TaskID,
		AgentType: req.AgentType,
		Timestamp: time.Now().UTC(),
		Details:   map[string]any{"tier": tier, "model_id": req.ModelID},
	})
}

func (b *Broker) emitInferenceFailed(req engine.InferenceRequest, err error) {
	b.bus.Publish(events.EngineEvent{
		Type:      events.InferenceFailed,
		RunID:     req.RunID,
		TaskID:    req.TaskID,
		AgentType: req.AgentType,
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"model_id": req.ModelID,
			"error":    err.Error(),
		},
	})
}
