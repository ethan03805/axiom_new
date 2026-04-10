package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/openaxiom/axiom/internal/events"
)

func TestEventWebSocket_ReceivesEvents(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleEventWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Emit an event through the bus
	eng.Bus().Publish(events.EngineEvent{
		Type:  events.TaskCreated,
		RunID: "run-test",
		Details: map[string]any{
			"task_id": "task-001",
		},
	})

	// Read the event from the WebSocket
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var ev events.EngineEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != events.TaskCreated {
		t.Errorf("type: got %q, want %q", ev.Type, events.TaskCreated)
	}

	conn.Close(websocket.StatusNormalClosure, "done")
}

func TestControlWebSocket_ProcessesRequest(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleControlWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send a control request
	req := ControlRequest{
		RequestID:      "req-001",
		IdempotencyKey: "test:query_status:1",
		Type:           "query_status",
		Payload:        map[string]any{"project_id": projID},
	}
	data, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the response
	_, respData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.RequestID != "req-001" {
		t.Errorf("request_id: got %q, want %q", resp.RequestID, "req-001")
	}
	if resp.Status != "completed" && resp.Status != "accepted" {
		t.Errorf("status: got %q, want completed or accepted", resp.Status)
	}
}

func TestControlWebSocket_QueryStatus_RedactsSecrets(t *testing.T) {
	eng, db := testEngine(t)
	projID, runID := seedProjectAndRun(t, db)

	// Plant a config snapshot containing real-shaped secret material so we can
	// verify the WebSocket query_status path redacts before serializing.
	secretSnapshot := `{
		"Inference": {"OpenRouterAPIKey": "sk-or-v1-WSTESTSECRET0123456789"},
		"Nested": {
			"api_key": "ws-leaked-api-key",
			"refresh_token": "ws-leaked-token",
			"safe": "ordinary-value"
		}
	}`
	if _, err := db.Exec(`UPDATE project_runs SET config_snapshot = ? WHERE id = ?`,
		secretSnapshot, runID); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleControlWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	req := ControlRequest{
		RequestID: "req-redact",
		Type:      "query_status",
		Payload:   map[string]any{"project_id": projID},
	}
	data, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, respData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	body := string(respData)
	forbidden := []string{
		"sk-or-v1-WSTESTSECRET",
		"WSTESTSECRET0123456789",
		"ws-leaked-api-key",
		"ws-leaked-token",
	}
	for _, needle := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("WebSocket response leaked secret %q; body: %s", needle, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("expected WebSocket response to contain [REDACTED]; body: %s", body)
	}
	if !strings.Contains(body, "ordinary-value") {
		t.Errorf("non-secret value was unexpectedly stripped; body: %s", body)
	}
}

func TestControlWebSocket_InvalidRequestType(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleControlWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	req := ControlRequest{
		RequestID: "req-bad",
		Type:      "invalid_type",
		Payload:   map[string]any{},
	}
	data, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, respData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "rejected" {
		t.Errorf("status: got %q, want rejected", resp.Status)
	}
	if resp.Error == "" {
		t.Error("expected error message for invalid request type")
	}
}

func TestControlWebSocket_IdempotencyKey(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleControlWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send the same idempotency key twice
	for i := 0; i < 2; i++ {
		req := ControlRequest{
			RequestID:      "req-idem-" + string(rune('0'+i)),
			IdempotencyKey: "test:query_budget:same",
			Type:           "query_budget",
			Payload:        map[string]any{"project_id": projID},
		}
		data, _ := json.Marshal(req)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}

		_, _, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	// Both should succeed without error (idempotent)
}

// TestDispatchControlRequest_CreateTask verifies that a create_task control
// request routes into Engine.CreateTask, persists the task in the DB, and
// returns a completed response with the new task ID. This is the regression
// guard for the WebSocket dispatch wiring (Issue A) — previously this verb
// silently returned "accepted" without touching the engine.
func TestDispatchControlRequest_CreateTask(t *testing.T) {
	eng, db := testEngine(t)
	projID, runID := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.HandleControlWebSocket(w, r, projID)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	req := ControlRequest{
		RequestID: "req-create-task",
		Type:      "create_task",
		Payload: map[string]any{
			"spec": map[string]any{
				"objective":    "Add a new widget",
				"context_tier": "file",
				"files":        []any{"internal/widget/widget.go"},
				"constraints":  []any{"no new deps"},
				"acceptance_criteria": []any{
					"compile",
					"tests pass",
				},
				"output_format": "files",
			},
		},
	}
	data, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, respData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("status: got %q want completed; error=%q", resp.Status, resp.Error)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type: got %T want map", resp.Result)
	}
	taskID, _ := resultMap["task_id"].(string)
	if taskID == "" {
		t.Fatalf("result missing task_id: %+v", resultMap)
	}
	if status, _ := resultMap["status"].(string); status != "queued" {
		t.Errorf("result.status: got %q want queued", status)
	}

	// Verify the task actually lives in the DB for the seeded run.
	tasks, err := db.ListTasksByRun(runID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var found bool
	for _, tk := range tasks {
		if tk.ID == taskID {
			found = true
			if tk.Title != "Add a new widget" {
				t.Errorf("task title: got %q want %q", tk.Title, "Add a new widget")
			}
			if tk.Status != "queued" {
				t.Errorf("task status: got %q want queued", tk.Status)
			}
			break
		}
	}
	if !found {
		t.Fatalf("task %s not found in run %s tasks %+v", taskID, runID, tasks)
	}
}
