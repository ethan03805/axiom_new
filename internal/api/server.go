package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/state"
)

// ServerConfig holds configuration for the API server.
type ServerConfig struct {
	Port         int
	RateLimitRPM int
	AllowedIPs   []string
}

// Server is the Axiom API server (Section 24.2).
// It exposes REST endpoints, WebSocket channels, and audit logging.
type Server struct {
	eng      *engine.Engine
	db       *state.DB
	cfg      ServerConfig
	handlers *Handlers
	httpSrv  *http.Server
	listener net.Listener
	log      *slog.Logger
	mu       sync.Mutex
	ready    chan struct{}
}

// NewServer creates a new API server.
func NewServer(eng *engine.Engine, db *state.DB, cfg ServerConfig) *Server {
	return &Server{
		eng:      eng,
		db:       db,
		cfg:      cfg,
		handlers: NewHandlers(eng, db),
		log:      slog.Default(),
		ready:    make(chan struct{}),
	}
}

// Start starts the API server. Blocks until the context is cancelled or the server errors.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// Build middleware chain
	var handler http.Handler = mux

	// Rate limiting
	if s.cfg.RateLimitRPM > 0 {
		rl := NewRateLimiter(s.cfg.RateLimitRPM)
		handler = rl.Middleware()(handler)
	}

	// IP allowlist
	if len(s.cfg.AllowedIPs) > 0 {
		handler = IPAllowlist(s.cfg.AllowedIPs)(handler)
	}

	addr := fmt.Sprintf(":%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.httpSrv = &http.Server{Handler: handler}
	s.mu.Unlock()

	close(s.ready)
	s.log.Info("api server started", "addr", ln.Addr().String())

	err = s.httpSrv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()

	if srv == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	s.log.Info("api server stopped")
}

// Addr returns the server's listen address (host:port).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// WaitReady blocks until the server is ready or the timeout expires.
func (s *Server) WaitReady(timeout time.Duration) bool {
	select {
	case <-s.ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

// registerRoutes sets up all REST and WebSocket routes per Section 24.2.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Health check — no auth required
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Auth middleware wrapping for API routes
	authMW := AuthMiddleware(s.db)

	// Read endpoints (require at least read-only scope)
	readMW := func(h http.HandlerFunc) http.Handler {
		return authMW(s.auditLog(RequireScope(ScopeReadOnly)(h)))
	}

	// Write endpoints (require full-control scope)
	writeMW := func(h http.HandlerFunc) http.Handler {
		return authMW(s.auditLog(RequireScope(ScopeFullControl)(h)))
	}

	// --- Project creation ---
	mux.Handle("POST /api/v1/projects", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleCreateProject(w, r)
	}))

	// --- Project lifecycle + read ---
	mux.Handle("GET /api/v1/projects/{id}/status", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetStatus(w, r, r.PathValue("id"))
	}))
	mux.Handle("GET /api/v1/projects/{id}/tasks", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetTasks(w, r, r.PathValue("id"))
	}))
	mux.Handle("GET /api/v1/projects/{id}/tasks/{tid}/attempts", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetAttempts(w, r, r.PathValue("tid"))
	}))
	mux.Handle("GET /api/v1/projects/{id}/costs", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetCosts(w, r, r.PathValue("id"))
	}))
	mux.Handle("GET /api/v1/projects/{id}/events", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetEvents(w, r, r.PathValue("id"))
	}))
	mux.Handle("GET /api/v1/models", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetModels(w, r)
	}))

	// Lifecycle commands (require full-control)
	mux.Handle("POST /api/v1/projects/{id}/run", writeMW(func(w http.ResponseWriter, r *http.Request) {
		var body RunRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if body.Prompt == "" {
			writeError(w, http.StatusBadRequest, "prompt is required")
			return
		}
		run, err := s.eng.StartRun(engine.StartRunOptions{
			ProjectID: r.PathValue("id"),
			Prompt:    body.Prompt,
			BudgetUSD: body.BudgetUSD,
			Source:    "api",
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, run)
	}))

	mux.Handle("GET /api/v1/projects/{id}/srs", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetSRS(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/srs/submit", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleSRSSubmit(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/srs/approve", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleSRSApprove(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/srs/reject", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleSRSReject(w, r, r.PathValue("id"))
	}))
	mux.Handle("GET /api/v1/projects/{id}/run/handoff", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleGetRunHandoff(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/eco/approve", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleECOApprove(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/eco/reject", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleECOReject(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/pause", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandlePause(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/resume", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleResume(w, r, r.PathValue("id"))
	}))
	mux.Handle("POST /api/v1/projects/{id}/cancel", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleCancel(w, r, r.PathValue("id"))
	}))

	// Index query (read scope, POST for structured body)
	mux.Handle("POST /api/v1/index/query", readMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleIndexQuery(w, r)
	}))

	// Token management (full-control)
	mux.Handle("GET /api/v1/tokens", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleTokenList(w, r)
	}))
	mux.Handle("POST /api/v1/tokens/{id}/revoke", writeMW(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleTokenRevoke(w, r, r.PathValue("id"))
	}))

	// WebSocket endpoints — auth handled at connection level
	mux.Handle("/ws/projects/{id}", authMW(s.auditLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleEventWebSocket(w, r, r.PathValue("id"))
	}))))
	mux.Handle("/ws/projects/{id}/control", authMW(s.auditLog(RequireScope(ScopeFullControl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handlers.HandleControlWebSocket(w, r, r.PathValue("id"))
	})))))
}

// auditLog wraps a handler to log API requests.
// Per Section 24.3: all API requests shall be logged in the events table.
// If a project run can be resolved from the URL, logs to the events table.
// All requests are also logged to the api_audit_log table.
func (s *Server) auditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := TokenFromContext(r.Context())
		tokenID := ""
		if token != nil {
			tokenID = token.ID
		}

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		host, _, _ := net.SplitHostPort(r.RemoteAddr)

		// Log to api_audit_log table (always, best effort)
		s.db.Exec(`INSERT INTO api_audit_log (token_id, method, path, status_code, source_ip) VALUES (?, ?, ?, ?, ?)`,
			tokenID, r.Method, r.URL.Path, rw.statusCode, host)

		// Log to events table when a project run context is available
		projectID := extractProjectID(r.URL.Path)
		if projectID != "" {
			if run, err := s.db.GetActiveRun(projectID); err == nil {
				details := map[string]any{
					"method":   r.Method,
					"path":     r.URL.Path,
					"token_id": tokenID,
					"status":   rw.statusCode,
				}
				if rw.statusCode >= 400 {
					details["source_ip"] = host
				}
				detailsJSON, _ := json.Marshal(details)
				detailsStr := string(detailsJSON)
				s.db.CreateEvent(&state.Event{
					RunID:     run.ID,
					EventType: "api_request",
					Details:   &detailsStr,
				})
			}
		}
	})
}

// responseWriter captures the status code for audit logging.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// extractProjectID extracts the project ID from the URL path.
func extractProjectID(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
