package events

import "time"

// EventType identifies the kind of engine event.
type EventType string

// --- Authoritative events (persisted to SQLite events table) ---

const (
	RunCreated   EventType = "run_created"
	RunStarted   EventType = "run_started"
	RunPaused    EventType = "run_paused"
	RunResumed   EventType = "run_resumed"
	RunCancelled EventType = "run_cancelled"
	RunCompleted EventType = "run_completed"
	RunError     EventType = "run_error"

	TaskCreated   EventType = "task_created"
	TaskStarted   EventType = "task_started"
	TaskCompleted EventType = "task_completed"
	TaskFailed    EventType = "task_failed"
	TaskBlocked   EventType = "task_blocked"

	AttemptStarted   EventType = "attempt_started"
	AttemptPassed    EventType = "attempt_passed"
	AttemptFailed    EventType = "attempt_failed"
	AttemptEscalated EventType = "attempt_escalated"

	ContainerStarted EventType = "container_started"
	ContainerStopped EventType = "container_stopped"

	ECOProposed EventType = "eco_proposed"
	ECOResolved EventType = "eco_resolved"

	MergeQueued    EventType = "merge_queued"
	MergeSucceeded EventType = "merge_succeeded"
	MergeFailed    EventType = "merge_failed"

	BudgetWarning  EventType = "budget_warning"
	BudgetExceeded EventType = "budget_exceeded"

	InferenceRequested   EventType = "inference_requested"
	InferenceCompleted   EventType = "inference_completed"
	InferenceFailed      EventType = "inference_failed"
	ProviderAvailable    EventType = "provider_available"
	ProviderUnavailable  EventType = "provider_unavailable"
)

// --- View-model events (fanned out to subscribers, NOT persisted) ---
// Per Architecture Section 26.2.10: interface-layer events for TUI/GUI.

const (
	StartupSummary        EventType = "startup_summary"
	SessionModeChanged    EventType = "session_mode_changed"
	PromptSuggestion      EventType = "prompt_suggestion"
	TaskProjectionUpdated EventType = "task_projection_updated"
	ApprovalRequested     EventType = "approval_requested"
	ApprovalResolved      EventType = "approval_resolved"
	DiffPreviewReady      EventType = "diff_preview_ready"
	TranscriptCompacted   EventType = "transcript_compacted"
)

// viewModelEvents is the set of events that are only fanned out, not persisted.
var viewModelEvents = map[EventType]bool{
	StartupSummary:        true,
	SessionModeChanged:    true,
	PromptSuggestion:      true,
	TaskProjectionUpdated: true,
	ApprovalRequested:     true,
	ApprovalResolved:      true,
	DiffPreviewReady:      true,
	TranscriptCompacted:   true,
}

// IsViewModelEvent returns true if the event type is a view-model event
// that should be fanned out to subscribers but NOT persisted to SQLite.
func IsViewModelEvent(et EventType) bool {
	return viewModelEvents[et]
}

// EngineEvent is the event payload emitted by the engine.
type EngineEvent struct {
	Type      EventType
	RunID     string
	TaskID    string
	AgentType string
	AgentID   string
	Details   map[string]any
	Timestamp time.Time
}
