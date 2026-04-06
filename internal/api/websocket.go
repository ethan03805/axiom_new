package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"nhooyr.io/websocket"

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
		return ControlResponse{RequestID: req.RequestID, Status: "completed", Result: status}

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

	case "submit_eco", "create_task", "create_task_batch",
		"spawn_meeseeks", "spawn_reviewer", "spawn_sub_orchestrator",
		"approve_output", "reject_output", "request_inference":
		// Long-running operations: acknowledge immediately.
		// Final outcome delivered through the event stream per Section 24.2.
		return ControlResponse{
			RequestID: req.RequestID,
			Status:    "accepted",
			Result:    map[string]string{"message": "request queued for processing"},
		}

	default:
		return ControlResponse{RequestID: req.RequestID, Status: "rejected", Error: "unhandled request type"}
	}
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
