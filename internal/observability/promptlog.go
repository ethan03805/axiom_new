package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openaxiom/axiom/internal/security"
)

// Message is the persisted view of a prompt message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Entry is a single prompt-log record.
type Entry struct {
	RunID              string     `json:"run_id,omitempty"`
	TaskID             string     `json:"task_id,omitempty"`
	AttemptID          int64      `json:"attempt_id,omitempty"`
	ModelID            string     `json:"model_id"`
	Provider           string     `json:"provider"`
	MaxTokens          int        `json:"max_tokens,omitempty"`
	Temperature        float64    `json:"temperature,omitempty"`
	GrammarConstraints *string    `json:"grammar_constraints,omitempty"`
	Messages           []Message  `json:"messages"`
	Response           string     `json:"response"`
	FinishReason       string     `json:"finish_reason,omitempty"`
	InputTokens        int64      `json:"input_tokens,omitempty"`
	OutputTokens       int64      `json:"output_tokens,omitempty"`
	CostUSD            float64    `json:"cost_usd,omitempty"`
	LatencyMs          int64      `json:"latency_ms,omitempty"`
	Timestamp          time.Time  `json:"timestamp"`
}

// PromptLogger persists sanitized prompt logs in the project-local logs directory.
type PromptLogger struct {
	rootDir string
	enabled bool
	policy  *security.Policy
}

// NewPromptLogger creates a new prompt logger for a project root.
func NewPromptLogger(rootDir string, enabled bool, policy *security.Policy) *PromptLogger {
	return &PromptLogger{
		rootDir: rootDir,
		enabled: enabled,
		policy:  policy,
	}
}

// Enabled reports whether prompt persistence is active.
func (p *PromptLogger) Enabled() bool {
	return p != nil && p.enabled
}

// Write persists a prompt log entry and returns the file path.
func (p *PromptLogger) Write(entry Entry) (string, error) {
	if !p.Enabled() {
		return "", nil
	}

	entry = p.sanitize(entry)
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	dir := filepath.Join(p.rootDir, ".axiom", "logs", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating prompt log directory: %w", err)
	}

	path := filepath.Join(dir, promptLogName(entry.TaskID, entry.AttemptID))
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("marshalling prompt log: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing prompt log: %w", err)
	}
	return path, nil
}

func (p *PromptLogger) sanitize(entry Entry) Entry {
	if p.policy == nil {
		return entry
	}

	sanitized := entry
	if len(entry.Messages) > 0 {
		sanitized.Messages = make([]Message, len(entry.Messages))
		for i, msg := range entry.Messages {
			analysis := p.policy.AnalyzeContent("", msg.Content)
			sanitized.Messages[i] = Message{
				Role:    msg.Role,
				Content: analysis.RedactedContent,
			}
		}
	}

	if entry.Response != "" {
		analysis := p.policy.AnalyzeContent("", entry.Response)
		sanitized.Response = analysis.RedactedContent
	}

	return sanitized
}

func promptLogName(taskID string, attemptID int64) string {
	if taskID == "" {
		taskID = "unknown-task"
	}
	if attemptID <= 0 {
		attemptID = 0
	}
	return fmt.Sprintf("%s-%d.json", taskID, attemptID)
}
