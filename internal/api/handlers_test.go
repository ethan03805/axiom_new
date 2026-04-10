package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// testEngine creates a minimal engine with a test database and starts background workers.
func testEngine(t *testing.T) (*engine.Engine, *state.DB) {
	t.Helper()
	db := testDB(t)
	cfg := config.Default("test-project", "test-project")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	eng, err := engine.New(engine.Options{
		Config:  &cfg,
		DB:      db,
		RootDir: t.TempDir(),
		Log:     log,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Stop() })
	return eng, db
}

// testEngineNotStarted creates a minimal engine without starting background workers.
func testEngineNotStarted(t *testing.T) (*engine.Engine, *state.DB) {
	t.Helper()
	db := testDB(t)
	cfg := config.Default("test-project", "test-project")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	eng, err := engine.New(engine.Options{
		Config:  &cfg,
		DB:      db,
		RootDir: t.TempDir(),
		Log:     log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng, db
}

// seedProjectAndRun creates a project + active run for test endpoints.
func seedProjectAndRun(t *testing.T, db *state.DB) (string, string) {
	t.Helper()
	projID := "proj-test"
	_, err := db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		projID, "/tmp/test", "test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-test"
	_, err = db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode,
		 orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, projID, string(state.RunActive), "main", "axiom/test-project",
		"embedded", "claw", "user", 10.0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	return projID, runID
}

// authedRequest creates a request with a valid bearer token.
func authedRequest(t *testing.T, db *state.DB, method, url string, body []byte) *http.Request {
	t.Helper()
	rawToken := "axm_sk_handler_test_token_12345"
	seedToken(t, db, rawToken, ScopeFullControl, 24*time.Hour)

	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)
	return req
}

func TestHandleGetStatus(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	req := httptest.NewRequest("GET", "/api/v1/projects/"+projID+"/status", nil)
	rr := httptest.NewRecorder()
	h.HandleGetStatus(rr, req, projID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp engine.RunStatusProjection
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ProjectID != projID {
		t.Errorf("project_id: got %q, want %q", resp.ProjectID, projID)
	}
}

func TestHandleGetStatus_RedactsSecrets(t *testing.T) {
	eng, db := testEngine(t)
	projID, runID := seedProjectAndRun(t, db)

	// Replace the seeded run's empty config snapshot with one that contains
	// realistic secret material covering all redaction code paths:
	//   - field name match (OpenRouterAPIKey, api_key, _token, _secret, password)
	//   - value pattern match (raw `sk-or-v1-...` not under a "secret" key)
	secretSnapshot := `{
		"Inference": {"OpenRouterAPIKey": "sk-or-v1-TESTSECRET0123456789"},
		"Nested": {
			"api_key": "leaked-api-key-value",
			"refresh_token": "leaked-token-value",
			"shared_secret": "leaked-secret-value",
			"password": "leaked-password-value",
			"unrelated": "sk-or-v1-RAWTOKENVALUE",
			"safe": "ordinary-value"
		}
	}`
	if _, err := db.Exec(`UPDATE project_runs SET config_snapshot = ? WHERE id = ?`,
		secretSnapshot, runID); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("GET", "/api/v1/projects/"+projID+"/status", nil)
	rr := httptest.NewRecorder()
	h.HandleGetStatus(rr, req, projID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	body := rr.Body.String()

	// The plaintext secret material must be absent from the response.
	forbidden := []string{
		"sk-or-v1-TESTSECRET",
		"TESTSECRET0123456789",
		"leaked-api-key-value",
		"leaked-token-value",
		"leaked-secret-value",
		"leaked-password-value",
		"sk-or-v1-RAWTOKENVALUE",
	}
	for _, needle := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("response leaked secret %q; body: %s", needle, body)
		}
	}

	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("expected response to contain [REDACTED]; body: %s", body)
	}

	// Sanity: non-secret values must still be present.
	if !strings.Contains(body, "ordinary-value") {
		t.Errorf("non-secret value was unexpectedly stripped; body: %s", body)
	}

	// The original projection (held by the engine path) must remain intact:
	// redaction is a presentation concern only.
	stored, err := db.GetRun(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !strings.Contains(stored.ConfigSnapshot, "sk-or-v1-TESTSECRET0123456789") {
		t.Errorf("redaction must not modify stored config snapshot; got: %s", stored.ConfigSnapshot)
	}
}

func TestHandleGetTasks(t *testing.T) {
	eng, db := testEngine(t)
	_, runID := seedProjectAndRun(t, db)

	// Seed a task
	_, err := db.Exec(`INSERT INTO tasks (id, run_id, title, status, tier, task_type) VALUES (?, ?, ?, ?, ?, ?)`,
		"task-001", runID, "test task", string(state.TaskQueued), string(state.TierStandard), string(state.TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("GET", "/api/v1/projects/proj-test/tasks", nil)
	rr := httptest.NewRecorder()
	h.HandleGetTasks(rr, req, "proj-test")

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}

	var tasks []state.Task
	if err := json.NewDecoder(rr.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestHandleGetAttempts(t *testing.T) {
	eng, db := testEngine(t)
	_, runID := seedProjectAndRun(t, db)

	_, err := db.Exec(`INSERT INTO tasks (id, run_id, title, status, tier, task_type) VALUES (?, ?, ?, ?, ?, ?)`,
		"task-001", runID, "test task", string(state.TaskQueued), string(state.TierStandard), string(state.TaskTypeImplementation))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO task_attempts
		(task_id, attempt_number, model_id, model_family, base_snapshot, status, phase)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"task-001", 1, "test-model", "test-family", "abc123", string(state.AttemptRunning), string(state.PhaseExecuting))
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("GET", "/api/v1/projects/proj-test/tasks/task-001/attempts", nil)
	rr := httptest.NewRecorder()
	h.HandleGetAttempts(rr, req, "task-001")

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetCosts(t *testing.T) {
	eng, db := testEngine(t)
	_, runID := seedProjectAndRun(t, db)

	_, err := db.Exec(`INSERT INTO cost_log (run_id, agent_type, model_id, cost_usd) VALUES (?, ?, ?, ?)`,
		runID, "meeseeks", "test-model", 0.05)
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("GET", "/api/v1/projects/proj-test/costs", nil)
	rr := httptest.NewRecorder()
	h.HandleGetCosts(rr, req, "proj-test")

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetEvents(t *testing.T) {
	eng, db := testEngine(t)
	_, runID := seedProjectAndRun(t, db)

	db.CreateEvent(&state.Event{RunID: runID, EventType: "run_created"})

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("GET", "/api/v1/projects/proj-test/events", nil)
	rr := httptest.NewRecorder()
	h.HandleGetEvents(rr, req, "proj-test")

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetModels(t *testing.T) {
	eng, db := testEngine(t)
	h := NewHandlers(eng, db)

	req := httptest.NewRequest("GET", "/api/v1/models", nil)
	rr := httptest.NewRecorder()
	h.HandleGetModels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandlePause(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	req := httptest.NewRequest("POST", "/api/v1/projects/"+projID+"/pause", nil)
	rr := httptest.NewRecorder()
	h.HandlePause(rr, req, projID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleResume(t *testing.T) {
	eng, db := testEngine(t)
	projID, runID := seedProjectAndRun(t, db)

	// Transition to paused first
	if err := db.UpdateRunStatus(runID, state.RunPaused); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("POST", "/api/v1/projects/"+projID+"/resume", nil)
	rr := httptest.NewRecorder()
	h.HandleResume(rr, req, projID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleCancel(t *testing.T) {
	eng, db := testEngine(t)
	projID, _ := seedProjectAndRun(t, db)
	h := NewHandlers(eng, db)

	req := httptest.NewRequest("POST", "/api/v1/projects/"+projID+"/cancel", nil)
	rr := httptest.NewRecorder()
	h.HandleCancel(rr, req, projID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleSRSApprove_NoRun(t *testing.T) {
	eng, db := testEngine(t)
	// Project without a run
	_, err := db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		"proj-norun", "/tmp/norun", "no-run", "no-run")
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(eng, db)
	req := httptest.NewRequest("POST", "/api/v1/projects/proj-norun/srs/approve", nil)
	rr := httptest.NewRecorder()
	h.HandleSRSApprove(rr, req, "proj-norun")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestHandleTokenList(t *testing.T) {
	_, db := testEngine(t)
	h := NewHandlers(nil, db)

	// Seed some tokens
	for i := 0; i < 3; i++ {
		token := &state.APIToken{
			ID:          "tok-list-" + string(rune('a'+i)),
			TokenHash:   "hash-list-" + string(rune('a'+i)),
			TokenPrefix: "axm_sk_test",
			Scope:       ScopeFullControl,
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		}
		if err := db.CreateAPIToken(token); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/tokens", nil)
	rr := httptest.NewRecorder()
	h.HandleTokenList(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}

	var tokens []TokenInfo
	if err := json.NewDecoder(rr.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(tokens))
	}
}
