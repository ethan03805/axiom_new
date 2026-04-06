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
