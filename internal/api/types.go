package api

import "time"

// --- REST request/response types ---

// ErrorResponse is the standard error payload for all API errors.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

// RunRequest is the body for POST /api/v1/projects/:id/run.
type RunRequest struct {
	Prompt    string  `json:"prompt"`
	BudgetUSD float64 `json:"budget_usd,omitempty"`
}

// SRSSubmitRequest is the body for POST /api/v1/projects/:id/srs/submit.
type SRSSubmitRequest struct {
	Content string `json:"content"`
}

// SRSRejectRequest is the body for POST /api/v1/projects/:id/srs/reject.
type SRSRejectRequest struct {
	Feedback string `json:"feedback"`
}

// ECORejectRequest is the body for POST /api/v1/projects/:id/eco/reject.
type ECORejectRequest struct {
	Feedback string `json:"feedback"`
}

// IndexQueryRequest is the body for POST /api/v1/index/query.
type IndexQueryRequest struct {
	QueryType string `json:"query_type"` // lookup_symbol, reverse_dependencies, list_exports, find_implementations, module_graph
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Package   string `json:"package,omitempty"`
}

// TokenInfo is the API-safe representation of a token (no hash exposed).
type TokenInfo struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// --- WebSocket control envelope (Section 8.6, 24.2) ---

// ControlRequest is the typed action envelope from external orchestrators.
type ControlRequest struct {
	RequestID      string         `json:"request_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	Type           string         `json:"type"`
	Payload        map[string]any `json:"payload"`
}

// ControlResponse is the engine's response to a control request.
type ControlResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"` // accepted, rejected, completed
	Result    any    `json:"result"`
	Error     string `json:"error,omitempty"`
}

// --- Auth context key ---

// tokenContextKey is the context key for the authenticated token.
type tokenContextKey struct{}

// TokenScope constants for authorization checks.
const (
	ScopeReadOnly    = "read-only"
	ScopeFullControl = "full-control"
)

// ValidControlRequestTypes enumerates accepted control WebSocket request types
// per Architecture Section 8.6.
var ValidControlRequestTypes = map[string]bool{
	"submit_srs":             true,
	"submit_eco":             true,
	"create_task":            true,
	"create_task_batch":      true,
	"spawn_meeseeks":         true,
	"spawn_reviewer":         true,
	"spawn_sub_orchestrator": true,
	"approve_output":         true,
	"reject_output":          true,
	"query_index":            true,
	"query_status":           true,
	"query_budget":           true,
	"request_inference":      true,
}
