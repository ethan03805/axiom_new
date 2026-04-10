package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// redactedPlaceholder is the value substituted for any field detected as a secret.
const redactedPlaceholder = "[REDACTED]"

// secretKeyPatterns is the list of case-insensitive substrings that mark a JSON
// field name as containing a secret. Matches are evaluated against the lower-cased
// field key. Per Architecture Section 19.5: secrets must never cross the
// HTTP/WS response boundary in plaintext.
var secretKeyPatterns = []string{
	"api_key",
	"apikey",
	"openrouter_api_key",
	"openrouterapikey",
	"_secret",
	"_token",
	"password",
	"passphrase",
}

// secretValuePattern matches string values that look like Axiom or OpenRouter
// secret tokens (e.g. `sk-or-v1-...`, `axm_sk_...`).
var secretValuePattern = regexp.MustCompile(`^(sk-[A-Za-z0-9_-]+|axm_sk_[A-Za-z0-9_-]+)`)

// isSecretKey reports whether the given JSON field name should be treated as
// a secret based on substring matching against secretKeyPatterns.
func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pat := range secretKeyPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// redactSecretsInJSONTree walks an arbitrary JSON-decoded value (map/slice/scalar)
// and replaces any secret-bearing string values with redactedPlaceholder.
// Maps are recursed by key (so the field name participates in the secret check);
// slices and scalar strings are checked against secretValuePattern only.
func redactSecretsInJSONTree(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if isSecretKey(k) {
				if _, ok := child.(string); ok {
					val[k] = redactedPlaceholder
					continue
				}
			}
			val[k] = redactSecretsInJSONTree(child)
		}
		return val
	case []any:
		for i, child := range val {
			val[i] = redactSecretsInJSONTree(child)
		}
		return val
	case string:
		if secretValuePattern.MatchString(val) {
			return redactedPlaceholder
		}
		return val
	default:
		return v
	}
}

// redactConfigSnapshot parses a JSON config snapshot, redacts any secret-bearing
// fields, and returns the re-serialized snapshot. If the input is empty or fails
// to parse, the original string is returned unchanged.
func redactConfigSnapshot(snapshot string) string {
	if snapshot == "" {
		return snapshot
	}
	var parsed any
	if err := json.Unmarshal([]byte(snapshot), &parsed); err != nil {
		return snapshot
	}
	parsed = redactSecretsInJSONTree(parsed)
	out, err := json.Marshal(parsed)
	if err != nil {
		return snapshot
	}
	return string(out)
}

// redactSecretsInRunStatus returns a copy of the projection with secrets in the
// embedded ProjectRun.ConfigSnapshot replaced by redactedPlaceholder. The
// original projection is left untouched so internal callers still see real
// credentials.
func redactSecretsInRunStatus(status *engine.RunStatusProjection) *engine.RunStatusProjection {
	if status == nil {
		return nil
	}
	clone := *status
	if clone.Run != nil {
		runCopy := *clone.Run
		runCopy.ConfigSnapshot = redactConfigSnapshot(runCopy.ConfigSnapshot)
		clone.Run = &runCopy
	}
	return &clone
}

// Handlers holds the REST endpoint handlers for the Axiom API server.
// Per Architecture Section 24.2.
type Handlers struct {
	eng *engine.Engine
	db  *state.DB
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(eng *engine.Engine, db *state.DB) *Handlers {
	return &Handlers{eng: eng, db: db}
}

// --- Project creation ---

// HandleCreateProject creates a new project.
// POST /api/v1/projects
func (h *Handlers) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RootPath string `json:"root_path"`
		Name     string `json:"name"`
		Slug     string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == "" || body.Slug == "" || body.RootPath == "" {
		writeError(w, http.StatusBadRequest, "root_path, name, and slug are required")
		return
	}

	proj := &state.Project{
		ID:       body.Slug,
		RootPath: body.RootPath,
		Name:     body.Name,
		Slug:     body.Slug,
	}
	if err := h.db.CreateProject(proj); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proj)
}

// --- Read endpoints ---

// HandleGetStatus returns the project status projection.
// GET /api/v1/projects/:id/status
func (h *Handlers) HandleGetStatus(w http.ResponseWriter, r *http.Request, projectID string) {
	status, err := h.eng.GetRunStatus(projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactSecretsInRunStatus(status))
}

// HandleGetTasks returns the task tree for a project's active run.
// GET /api/v1/projects/:id/tasks
func (h *Handlers) HandleGetTasks(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeJSON(w, http.StatusOK, []state.Task{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tasks, err := h.db.ListTasksByRun(run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// HandleGetAttempts returns attempt history for a task.
// GET /api/v1/projects/:id/tasks/:tid/attempts
func (h *Handlers) HandleGetAttempts(w http.ResponseWriter, r *http.Request, taskID string) {
	attempts, err := h.db.ListAttemptsByTask(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, attempts)
}

// HandleGetCosts returns the cost breakdown for a project's active run.
// GET /api/v1/projects/:id/costs
func (h *Handlers) HandleGetCosts(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "total_usd": 0})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entries, err := h.db.ListCostLogByRun(run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, _ := h.db.TotalCostByRun(run.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":   entries,
		"total_usd": total,
	})
}

// HandleGetEvents returns the event log for a project's active run.
// GET /api/v1/projects/:id/events
func (h *Handlers) HandleGetEvents(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeJSON(w, http.StatusOK, []state.Event{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	events, err := h.db.ListEventsByRun(run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// HandleGetModels returns the model registry.
// GET /api/v1/models
func (h *Handlers) HandleGetModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.db.ListModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// --- Lifecycle endpoints ---

// HandlePause pauses the active run.
// POST /api/v1/projects/:id/pause
func (h *Handlers) HandlePause(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no active run: "+err.Error())
		return
	}

	if err := h.eng.PauseRun(run.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// HandleResume resumes a paused run.
// POST /api/v1/projects/:id/resume
func (h *Handlers) HandleResume(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		// Also check for paused runs
		run, err = h.db.GetLatestRunByProject(projectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "no run found: "+err.Error())
			return
		}
	}

	if err := h.eng.ResumeRun(run.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

// HandleCancel cancels the active run.
// POST /api/v1/projects/:id/cancel
func (h *Handlers) HandleCancel(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no active run: "+err.Error())
		return
	}

	if err := h.eng.CancelRun(run.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// HandleGetSRS returns the current SRS draft or approved SRS content.
// GET /api/v1/projects/:id/srs
func (h *Handlers) HandleGetSRS(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no active run")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := map[string]any{
		"run_id": run.ID,
		"status": string(run.Status),
	}

	if run.InitialPrompt != "" {
		result["initial_prompt"] = run.InitialPrompt
	}

	// Try reading the draft
	draftContent, draftErr := h.eng.ReadSRSDraft(run.ID)
	if draftErr == nil {
		result["draft"] = draftContent
	}

	// If SRS hash is set, the approved SRS exists
	if run.SRSHash != nil {
		result["srs_hash"] = *run.SRSHash
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleSRSSubmit accepts an SRS draft submission from an external orchestrator.
// POST /api/v1/projects/:id/srs/submit
func (h *Handlers) HandleSRSSubmit(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no active run")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var body SRSSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	if err := h.eng.SubmitSRS(run.ID, body.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "awaiting_srs_approval",
		"run_id": run.ID,
	})
}

// HandleGetRunHandoff returns the pending run handoff state for an external orchestrator.
// GET /api/v1/projects/:id/run/handoff
func (h *Handlers) HandleGetRunHandoff(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no active run")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handoff := map[string]any{
		"run_id":            run.ID,
		"project_id":        run.ProjectID,
		"status":            string(run.Status),
		"initial_prompt":    run.InitialPrompt,
		"start_source":      run.StartSource,
		"orchestrator_mode": run.OrchestratorMode,
		"base_branch":       run.BaseBranch,
		"work_branch":       run.WorkBranch,
		"budget_max_usd":    run.BudgetMaxUSD,
	}

	writeJSON(w, http.StatusOK, handoff)
}

// HandleSRSApprove approves the generated SRS.
// POST /api/v1/projects/:id/srs/approve
func (h *Handlers) HandleSRSApprove(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no active run: "+err.Error())
		return
	}

	if err := h.eng.ApproveSRS(run.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// HandleSRSReject rejects the SRS with feedback.
// POST /api/v1/projects/:id/srs/reject
func (h *Handlers) HandleSRSReject(w http.ResponseWriter, r *http.Request, projectID string) {
	run, err := h.db.GetActiveRun(projectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no active run: "+err.Error())
		return
	}

	var body SRSRejectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := h.eng.RejectSRS(run.ID, body.Feedback); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// HandleECOApprove approves a pending ECO.
// POST /api/v1/projects/:id/eco/approve
func (h *Handlers) HandleECOApprove(w http.ResponseWriter, r *http.Request, projectID string) {
	var body struct {
		ECOID int64 `json:"eco_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := h.eng.ApproveECO(body.ECOID, "api"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// HandleECOReject rejects a pending ECO.
// POST /api/v1/projects/:id/eco/reject
func (h *Handlers) HandleECOReject(w http.ResponseWriter, r *http.Request, projectID string) {
	var body struct {
		ECOID int64 `json:"eco_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := h.eng.RejectECO(body.ECOID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// HandleIndexQuery handles semantic index queries.
// POST /api/v1/index/query
func (h *Handlers) HandleIndexQuery(w http.ResponseWriter, r *http.Request) {
	var body IndexQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	switch body.QueryType {
	case "lookup_symbol":
		results, err := h.db.LookupSymbol(body.Name, body.Kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	case "reverse_dependencies":
		results, err := h.db.ListReferencesBySymbol(body.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	case "list_exports":
		results, err := h.db.ListExportedSymbolsByPackageDir(body.Package)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	case "find_implementations":
		results, err := h.db.FindImplementations(body.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	default:
		writeError(w, http.StatusBadRequest, "unknown query_type: "+body.QueryType)
	}
}

// --- Token management ---

// HandleTokenList lists all API tokens (without hashes).
// GET /api/v1/tokens
func (h *Handlers) HandleTokenList(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.db.ListAPITokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	infos := make([]TokenInfo, len(tokens))
	for i, t := range tokens {
		infos[i] = TokenInfo{
			ID:         t.ID,
			Prefix:     t.TokenPrefix,
			Scope:      t.Scope,
			CreatedAt:  t.CreatedAt,
			ExpiresAt:  t.ExpiresAt,
			RevokedAt:  t.RevokedAt,
			LastUsedAt: t.LastUsedAt,
		}
	}
	writeJSON(w, http.StatusOK, infos)
}

// HandleTokenRevoke revokes a token by ID.
// POST /api/v1/tokens/:id/revoke
func (h *Handlers) HandleTokenRevoke(w http.ResponseWriter, r *http.Request, tokenID string) {
	if err := h.db.RevokeAPIToken(tokenID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
