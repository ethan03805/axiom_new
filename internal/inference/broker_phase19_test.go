package inference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/observability"
	"github.com/openaxiom/axiom/internal/security"
)

func TestBroker_Infer_WritesRedactedPromptLogAndUpdatesAttemptMetrics(t *testing.T) {
	broker, cloud, _, runID, taskID := setupTestBroker(t, 10.0)
	root := t.TempDir()
	broker.cfg.Observability.LogPrompts = true
	broker.promptLogger = observability.NewPromptLogger(root, true, security.NewPolicy(broker.cfg.Security))

	cloud.response.Content = `response secret ghp_123456789012345678901234567890123456`

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:                     runID,
		TaskID:                    taskID,
		AttemptID:                 1,
		AgentType:                 "meeseeks",
		ModelID:                   "anthropic/claude-4-sonnet",
		Tier:                      "standard",
		AllowExternalForSensitive: true,
		Messages: []engine.InferenceMessage{{
			Role:    "user",
			Content: `request secret sk-or-v1-supersecretvalue1234567890`,
		}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	logPath := filepath.Join(root, ".axiom", "logs", "prompts", taskID+"-1.json")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading prompt log: %v", err)
	}

	logBody := string(data)
	if strings.Contains(logBody, "sk-or-v1-supersecretvalue1234567890") {
		t.Fatal("prompt log should not contain the raw request secret")
	}
	if strings.Contains(logBody, "ghp_123456789012345678901234567890123456") {
		t.Fatal("prompt log should not contain the raw response secret")
	}
	if !strings.Contains(logBody, "[REDACTED]") {
		t.Fatal("prompt log should contain redaction markers")
	}
	if !strings.Contains(logBody, `"provider":"openrouter"`) {
		t.Fatal("prompt log should include provider metadata")
	}

	attempts, err := broker.db.ListAttemptsByTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].InputTokens == nil || *attempts[0].InputTokens != 10 {
		t.Fatalf("input_tokens = %v, want 10", attempts[0].InputTokens)
	}
	if attempts[0].OutputTokens == nil || *attempts[0].OutputTokens != 20 {
		t.Fatalf("output_tokens = %v, want 20", attempts[0].OutputTokens)
	}
	if attempts[0].CostUSD <= 0 {
		t.Fatalf("cost_usd = %f, want positive", attempts[0].CostUSD)
	}
}

func TestBroker_Infer_SkipsPromptLogWhenDisabled(t *testing.T) {
	broker, _, _, runID, taskID := setupTestBroker(t, 10.0)
	root := t.TempDir()
	broker.cfg.Observability.LogPrompts = false
	broker.promptLogger = observability.NewPromptLogger(root, false, security.NewPolicy(broker.cfg.Security))

	_, err := broker.Infer(context.Background(), engine.InferenceRequest{
		RunID:     runID,
		TaskID:    taskID,
		AttemptID: 1,
		AgentType: "meeseeks",
		ModelID:   "anthropic/claude-4-sonnet",
		Tier:      "standard",
		Messages:  []engine.InferenceMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	logPath := filepath.Join(root, ".axiom", "logs", "prompts", taskID+"-1.json")
	if _, err := os.Stat(logPath); err == nil {
		t.Fatalf("expected no prompt log at %s when logging is disabled", logPath)
	}
}
