package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/security"
)

func TestPromptLoggerWrite_SanitizesSecretsAndPersistsMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("prompt-log", "prompt-log")
	policy := security.NewPolicy(cfg.Security)
	logger := NewPromptLogger(root, true, policy)

	entry := Entry{
		RunID:     "run-123",
		TaskID:    "task-123",
		AttemptID: 2,
		ModelID:   "openai/gpt-5.4-mini",
		Provider:  "openrouter",
		Messages: []Message{
			{Role: "user", Content: `OPENROUTER_API_KEY="sk-or-v1-supersecretvalue1234567890"`},
		},
		Response:    "use token AKIAIOSFODNN7EXAMPLE for deployment",
		Timestamp:   time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		InputTokens: 100,
	}

	path, err := logger.Write(entry)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	expectedPath := filepath.Join(root, ".axiom", "logs", "prompts", "task-123-2.json")
	if path != expectedPath {
		t.Fatalf("path = %q, want %q", path, expectedPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "sk-or-v1-supersecretvalue1234567890") {
		t.Fatal("prompt log persisted raw OpenRouter key")
	}
	if strings.Contains(string(data), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("prompt log persisted raw AWS key")
	}

	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.RunID != "run-123" {
		t.Fatalf("RunID = %q, want run-123", decoded.RunID)
	}
	if decoded.Messages[0].Content == entry.Messages[0].Content {
		t.Fatal("expected message content to be sanitized")
	}
	if !strings.Contains(decoded.Messages[0].Content, "[REDACTED]") {
		t.Fatalf("sanitized message missing redaction marker: %q", decoded.Messages[0].Content)
	}
	if !strings.Contains(decoded.Response, "[REDACTED]") {
		t.Fatalf("sanitized response missing redaction marker: %q", decoded.Response)
	}
}

func TestPromptLoggerWrite_DisabledDoesNothing(t *testing.T) {
	root := t.TempDir()
	logger := NewPromptLogger(root, false, nil)

	path, err := logger.Write(Entry{TaskID: "task-disabled"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty string", path)
	}

	if _, err := os.Stat(filepath.Join(root, ".axiom", "logs", "prompts")); !os.IsNotExist(err) {
		t.Fatalf("expected no prompt log directory, got err=%v", err)
	}
}
