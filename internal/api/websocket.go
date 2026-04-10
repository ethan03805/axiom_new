package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"nhooyr.io/websocket"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
)

// HandleEventWebSocket upgrades to a WebSocket and streams project events.
// ws://host/ws/projects/:id — real-time project events.
// Per Architecture Section 24.2.
func (h *Handlers) HandleEventWebSocket(w http.ResponseWriter, r *http.Request, projectID string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow cross-origin for local dev
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "server error")

	// Subscribe to events for this project's runs
	ch, subID := h.eng.Bus().Subscribe(func(ev events.EngineEvent) bool {
		return true // Stream all events; client can filter
	})
	defer h.eng.Bus().Unsubscribe(subID)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "connection closed")
			return
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

// HandleControlWebSocket upgrades to a WebSocket for external orchestrator control.
// ws://host/ws/projects/:id/control — authenticated control channel.
// Per Architecture Section 24.2: carries typed action requests from Section 8.6.
func (h *Handlers) HandleControlWebSocket(w http.ResponseWriter, r *http.Request, projectID string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "server error")

	log := slog.Default()
	idem := newIdempotencyTracker()

	ctx := r.Context()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var req ControlRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := ControlResponse{
				RequestID: "",
				Status:    "rejected",
				Error:     "invalid JSON: " + err.Error(),
			}
			writeWSResponse(ctx, conn, resp)
			continue
		}

		// Validate request type
		if !ValidControlRequestTypes[req.Type] {
			resp := ControlResponse{
				RequestID: req.RequestID,
				Status:    "rejected",
				Error:     "unknown request type: " + req.Type,
			}
			writeWSResponse(ctx, conn, resp)
			continue
		}

		// Check idempotency
		if req.IdempotencyKey != "" {
			if cached, ok := idem.get(req.IdempotencyKey); ok {
				cached.RequestID = req.RequestID
				writeWSResponse(ctx, conn, cached)
				continue
			}
		}

		resp := h.dispatchControlRequest(ctx, req, projectID)

		if req.IdempotencyKey != "" {
			idem.set(req.IdempotencyKey, resp)
		}

		writeWSResponse(ctx, conn, resp)
		log.Debug("control request processed",
			"type", req.Type,
			"request_id", req.RequestID,
			"status", resp.Status,
		)
	}
}

// dispatchControlRequest routes a control request to the appropriate engine method.
func (h *Handlers) dispatchControlRequest(ctx context.Context, req ControlRequest, projectID string) ControlResponse {
	switch req.Type {
	case "query_status":
		status, err := h.eng.GetRunStatus(projectID)
		if err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
		}
		return ControlResponse{RequestID: req.RequestID, Status: "completed", Result: redactSecretsInRunStatus(status)}

	case "query_budget":
		run, err := h.db.GetActiveRun(projectID)
		if err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
		}
		total, _ := h.db.TotalCostByRun(run.ID)
		return ControlResponse{
			RequestID: req.RequestID,
			Status:    "completed",
			Result: map[string]any{
				"max_usd":       run.BudgetMaxUSD,
				"spent_usd":     total,
				"remaining_usd": run.BudgetMaxUSD - total,
			},
		}

	case "query_index":
		name, _ := req.Payload["name"].(string)
		kind, _ := req.Payload["kind"].(string)
		results, err := h.db.LookupSymbol(name, kind)
		if err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
		}
		return ControlResponse{RequestID: req.RequestID, Status: "completed", Result: results}

	case "submit_srs":
		content, _ := req.Payload["content"].(string)
		if content == "" {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.content is required"}
		}
		run, err := h.db.GetActiveRun(projectID)
		if err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "no active run: " + err.Error()}
		}
		if err := h.eng.SubmitSRS(run.ID, content); err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
		}
		return ControlResponse{
			RequestID: req.RequestID,
			Status:    "completed",
			Result:    map[string]string{"status": "awaiting_srs_approval", "run_id": run.ID},
		}

	case "create_task":
		return h.dispatchCreateTask(req, projectID)

	case "create_task_batch":
		return h.dispatchCreateTaskBatch(req, projectID)

	case "approve_output":
		return h.dispatchApproveOutput(req)

	case "reject_output":
		return h.dispatchRejectOutput(req)

	case "submit_eco":
		return h.dispatchSubmitECO(req, projectID)

	case "spawn_meeseeks", "spawn_reviewer", "spawn_sub_orchestrator", "request_inference":
		// Not wired yet: fail loudly instead of silently accepting.
		// Architecture §8.6 lists these verbs, but the engine does not expose
		// direct public entrypoints for them — spawning is driven by the
		// scheduler/executor loop, and direct inference requests are served
		// via the REST /api/v1/inference/* surface. Once a public Engine.Spawn*
		// API exists these cases can be wired the same way as create_task.
		return ControlResponse{
			RequestID: req.RequestID,
			Status:    "rejected",
			Error:     fmt.Sprintf("control verb %q not yet implemented on the engine", req.Type),
		}

	default:
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "unhandled request type"}
	}
}

// dispatchCreateTask wires create_task to Engine.CreateTask. The active run
// for the project is resolved server-side so orchestrators do not need to
// thread run IDs through every request.
func (h *Handlers) dispatchCreateTask(req ControlRequest, projectID string) ControlResponse {
	specRaw, ok := req.Payload["spec"]
	if !ok {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.spec is required"}
	}
	spec, err := decodeTaskCreateSpec(specRaw)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
	}

	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "no active run: " + err.Error()}
	}

	task, err := h.eng.CreateTask(run.ID, spec)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
	}

	return ControlResponse{
		RequestID: req.RequestID,
		Status:    "completed",
		Result: map[string]any{
			"task_id": task.ID,
			"status":  string(task.Status),
		},
	}
}

// dispatchCreateTaskBatch wires create_task_batch to Engine.CreateTaskBatch.
// The batch is atomic at the spec-decoding level but sequential at the DB
// level — see Engine.CreateTaskBatch for rationale.
func (h *Handlers) dispatchCreateTaskBatch(req ControlRequest, projectID string) ControlResponse {
	rawTasks, ok := req.Payload["tasks"]
	if !ok {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.tasks is required"}
	}
	tasksSlice, ok := rawTasks.([]any)
	if !ok {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.tasks must be an array"}
	}

	specs := make([]engine.TaskCreateSpec, 0, len(tasksSlice))
	for i, raw := range tasksSlice {
		// Each element may be either a bare spec object or an envelope of
		// the form {"spec": {...}} to match create_task. Accept both so
		// orchestrators can reuse the single-task payload shape.
		var specRaw any = raw
		if m, ok := raw.(map[string]any); ok {
			if inner, hasSpec := m["spec"]; hasSpec {
				specRaw = inner
			}
		}
		spec, err := decodeTaskCreateSpec(specRaw)
		if err != nil {
			return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: fmt.Sprintf("task[%d]: %s", i, err.Error())}
		}
		specs = append(specs, spec)
	}

	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "no active run: " + err.Error()}
	}

	tasks, err := h.eng.CreateTaskBatch(run.ID, specs)
	if err != nil {
		// Return the tasks that did make it so orchestrators can recover.
		ids := make([]string, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		return ControlResponse{
			RequestID: req.RequestID,
			Status:    "rejected",
			Error:     err.Error(),
			Result: map[string]any{
				"created_task_ids": ids,
				"count":            len(ids),
			},
		}
	}

	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ControlResponse{
		RequestID: req.RequestID,
		Status:    "completed",
		Result: map[string]any{
			"task_ids": ids,
			"count":    len(ids),
		},
	}
}

// dispatchApproveOutput wires approve_output to Engine.ApproveTaskOutput.
func (h *Handlers) dispatchApproveOutput(req ControlRequest) ControlResponse {
	taskID, _ := req.Payload["task_id"].(string)
	attemptID, _ := req.Payload["attempt_id"].(string)
	if taskID == "" || attemptID == "" {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.task_id and payload.attempt_id are required"}
	}
	if err := h.eng.ApproveTaskOutput(taskID, attemptID); err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
	}
	return ControlResponse{
		RequestID: req.RequestID,
		Status:    "completed",
		Result:    map[string]string{"status": "enqueued"},
	}
}

// dispatchRejectOutput wires reject_output to Engine.RejectTaskOutput.
func (h *Handlers) dispatchRejectOutput(req ControlRequest) ControlResponse {
	taskID, _ := req.Payload["task_id"].(string)
	attemptID, _ := req.Payload["attempt_id"].(string)
	reason, _ := req.Payload["reason"].(string)
	if taskID == "" || attemptID == "" {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "payload.task_id and payload.attempt_id are required"}
	}
	if err := h.eng.RejectTaskOutput(taskID, attemptID, reason); err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
	}
	return ControlResponse{
		RequestID: req.RequestID,
		Status:    "completed",
		Result:    map[string]string{"status": "rejected"},
	}
}

// dispatchSubmitECO wires submit_eco to Engine.ProposeECO. Payload fields map
// to the ECOProposal struct; the active run is resolved server-side.
func (h *Handlers) dispatchSubmitECO(req ControlRequest, projectID string) ControlResponse {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "no active run: " + err.Error()}
	}

	// Support both rich orchestrator payloads ({reason,rationale,proposed_changes})
	// and the explicit ECOProposal shape ({category,affected_refs,description,proposed_change}).
	category, _ := req.Payload["category"].(string)
	if category == "" {
		category, _ = req.Payload["reason"].(string)
	}
	description, _ := req.Payload["description"].(string)
	if description == "" {
		description, _ = req.Payload["rationale"].(string)
	}
	affectedRefs, _ := req.Payload["affected_refs"].(string)
	proposedChange, _ := req.Payload["proposed_change"].(string)
	if proposedChange == "" {
		if raw, ok := req.Payload["proposed_changes"]; ok {
			// proposed_changes may be an array; flatten to a string summary.
			if data, err := json.Marshal(raw); err == nil {
				proposedChange = string(data)
			}
		}
	}

	ecoID, err := h.eng.ProposeECO(engine.ECOProposal{
		RunID:          run.ID,
		Category:       category,
		AffectedRefs:   affectedRefs,
		Description:    description,
		ProposedChange: proposedChange,
	})
	if err != nil {
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: err.Error()}
	}

	return ControlResponse{
		RequestID: req.RequestID,
		Status:    "completed",
		Result: map[string]any{
			"eco_id": ecoID,
			"status": "proposed",
		},
	}
}

// decodeTaskCreateSpec marshals a raw payload map/struct into a TaskCreateSpec
// via JSON round-trip. This is more tolerant than manual type assertions and
// matches the schema in the control dispatch docstrings.
func decodeTaskCreateSpec(raw any) (engine.TaskCreateSpec, error) {
	var spec engine.TaskCreateSpec
	data, err := json.Marshal(raw)
	if err != nil {
		return spec, fmt.Errorf("encoding spec: %w", err)
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return spec, fmt.Errorf("decoding spec: %w", err)
	}
	if spec.Objective == "" {
		return spec, fmt.Errorf("spec.objective is required")
	}
	return spec, nil
}

func writeWSResponse(ctx context.Context, conn *websocket.Conn, resp ControlResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(ctx, websocket.MessageText, data)
}

// idempotencyTracker caches responses by idempotency key.
// Per Section 24.2: idempotency keys ensure reconnect-safe retries.
type idempotencyTracker struct {
	mu    sync.Mutex
	cache map[string]ControlResponse
}

func newIdempotencyTracker() *idempotencyTracker {
	return &idempotencyTracker{cache: make(map[string]ControlResponse)}
}

func (it *idempotencyTracker) get(key string) (ControlResponse, bool) {
	it.mu.Lock()
	defer it.mu.Unlock()
	resp, ok := it.cache[key]
	return resp, ok
}

func (it *idempotencyTracker) set(key string, resp ControlResponse) {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.cache[key] = resp
}
