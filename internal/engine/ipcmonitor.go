package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/state"
)

var errAttemptWaitingOnLock = errors.New("attempt moved to waiting_on_lock")

type ipcMonitorRequest struct {
	Task    *state.Task
	Attempt *state.TaskAttempt
	Dirs    ipc.Dirs
}

type ipcMonitorResult struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

type inferenceResponsePayload struct {
	Content      string  `json:"content,omitempty"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	ModelID      string  `json:"model_id,omitempty"`
	FinishReason string  `json:"finish_reason,omitempty"`
	ProviderName string  `json:"provider_name,omitempty"`
	Error        string  `json:"error,omitempty"`
}

type actionResponsePayload struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (e *Engine) monitorTaskIPC(ctx context.Context, req ipcMonitorRequest) (*ipcMonitorResult, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	result := &ipcMonitorResult{}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		files, err := listIPCFiles(req.Dirs.Output)
		if err != nil {
			return nil, err
		}

		for _, file := range files {
			env, err := readIPCEnvelope(file)
			if err != nil {
				return nil, fmt.Errorf("reading ipc message %s: %w", file, err)
			}

			switch env.Type {
			case ipc.MsgInferenceRequest:
				if err := e.handleInferenceRequest(ctx, req, env, result); err != nil {
					return nil, err
				}
			case ipc.MsgRequestScopeExpansion:
				waiting, err := e.handleScopeExpansionRequest(ctx, req, env)
				if err != nil {
					return nil, err
				}
				if waiting {
					_ = os.Remove(file)
					return nil, errAttemptWaitingOnLock
				}
			case ipc.MsgActionRequest:
				if err := e.handleActionRequest(req, env); err != nil {
					return nil, err
				}
			case ipc.MsgTaskOutput:
				_ = os.Remove(file)
				return result, nil
			}

			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("removing processed ipc message %s: %w", file, err)
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *Engine) handleInferenceRequest(ctx context.Context, req ipcMonitorRequest, env ipc.Envelope, result *ipcMonitorResult) error {
	var payload ipc.InferenceRequestPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("decoding inference request: %w", err)
	}

	response := inferenceResponsePayload{}
	if e.inference == nil || !e.inference.Available() {
		response.Error = "inference broker unavailable"
		return writeIPCResponse(req.Dirs.Input, ipc.MsgInferenceResponse, req.Task.ID, response)
	}

	messages := make([]InferenceMessage, 0, len(payload.Messages))
	for _, msg := range payload.Messages {
		messages = append(messages, InferenceMessage{Role: msg.Role, Content: msg.Content})
	}

	inferenceReq := InferenceRequest{
		RunID:              req.Task.RunID,
		TaskID:             req.Task.ID,
		AttemptID:          req.Attempt.ID,
		AgentType:          "meeseeks",
		ModelID:            firstNonEmpty(payload.ModelID, req.Attempt.ModelID),
		Tier:               string(req.Attempt.Tier),
		Messages:           messages,
		MaxTokens:          payload.MaxTokens,
		Temperature:        payload.Temperature,
		GrammarConstraints: payload.GrammarConstraints,
	}

	resp, err := e.inference.Infer(ctx, inferenceReq)
	if err != nil {
		response.Error = err.Error()
		return writeIPCResponse(req.Dirs.Input, ipc.MsgInferenceResponse, req.Task.ID, response)
	}

	result.InputTokens += resp.InputTokens
	result.OutputTokens += resp.OutputTokens
	result.CostUSD += resp.CostUSD

	response = inferenceResponsePayload{
		Content:      resp.Content,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
		CostUSD:      resp.CostUSD,
		ModelID:      resp.ModelID,
		FinishReason: resp.FinishReason,
		ProviderName: resp.ProviderName,
	}
	return writeIPCResponse(req.Dirs.Input, ipc.MsgInferenceResponse, req.Task.ID, response)
}

func (e *Engine) handleScopeExpansionRequest(ctx context.Context, req ipcMonitorRequest, env ipc.Envelope) (bool, error) {
	var payload ipc.ScopeExpansionRequest
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return false, fmt.Errorf("decoding scope expansion request: %w", err)
	}

	additional := make([]TargetFileSpec, 0, len(payload.AdditionalFiles))
	for _, file := range payload.AdditionalFiles {
		canonical := filepath.ToSlash(filepath.Clean(file))
		additional = append(additional, TargetFileSpec{
			FilePath:        canonical,
			LockScope:       "file",
			LockResourceKey: canonical,
		})
	}

	if e.tasks == nil {
		resp := ipc.ScopeExpansionResponse{
			TaskID:  req.Task.ID,
			Status:  "denied",
			Message: "task service unavailable",
		}
		return false, writeIPCResponse(req.Dirs.Input, ipc.MsgScopeExpansionResponse, req.Task.ID, resp)
	}

	if err := e.tasks.RequestScopeExpansion(ctx, req.Task.ID, additional); err != nil {
		resp := ipc.ScopeExpansionResponse{
			TaskID:  req.Task.ID,
			Status:  "denied",
			Message: err.Error(),
		}
		if writeErr := writeIPCResponse(req.Dirs.Input, ipc.MsgScopeExpansionResponse, req.Task.ID, resp); writeErr != nil {
			return false, writeErr
		}
		return false, err
	}

	updated, err := e.db.GetTask(req.Task.ID)
	if err != nil {
		return false, fmt.Errorf("reloading task after scope expansion: %w", err)
	}
	if updated.Status == state.TaskWaitingOnLock {
		resp := ipc.ScopeExpansionResponse{
			TaskID:    req.Task.ID,
			Status:    "waiting_on_lock",
			BlockedBy: blockedByTaskID(e.db, req.Task.ID),
			Message:   "container will stop and the task will be re-queued when locks are available",
		}
		return true, writeIPCResponse(req.Dirs.Input, ipc.MsgScopeExpansionResponse, req.Task.ID, resp)
	}

	resp := ipc.ScopeExpansionResponse{
		TaskID:        req.Task.ID,
		Status:        "approved",
		ExpandedFiles: payload.AdditionalFiles,
		LocksAcquired: true,
	}
	return false, writeIPCResponse(req.Dirs.Input, ipc.MsgScopeExpansionResponse, req.Task.ID, resp)
}

func (e *Engine) handleActionRequest(req ipcMonitorRequest, _ ipc.Envelope) error {
	return writeIPCResponse(req.Dirs.Input, ipc.MsgActionResponse, req.Task.ID, actionResponsePayload{
		Status: "rejected",
		Error:  "action requests are not implemented for this runtime",
	})
}

func writeIPCResponse(dir string, msgType ipc.MessageType, taskID string, payload any) error {
	env, err := ipc.NewEnvelope(msgType, taskID, payload)
	if err != nil {
		return fmt.Errorf("building ipc response: %w", err)
	}
	if _, err := ipc.WriteMessage(dir, env); err != nil {
		return fmt.Errorf("writing ipc response: %w", err)
	}
	return nil
}

func listIPCFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading ipc directory %s: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func readIPCEnvelope(path string) (ipc.Envelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ipc.Envelope{}, err
	}
	var env ipc.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ipc.Envelope{}, err
	}
	return env, nil
}

func blockedByTaskID(db *state.DB, taskID string) string {
	var blockedBy sql.NullString
	if err := db.QueryRow(`SELECT blocked_by_task_id FROM task_lock_waits WHERE task_id = ?`, taskID).Scan(&blockedBy); err != nil {
		return ""
	}
	if !blockedBy.Valid {
		return ""
	}
	return strings.TrimSpace(blockedBy.String)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
