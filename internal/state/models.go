package state

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for repository operations.
var (
	ErrNotFound          = errors.New("not found")
	ErrInvalidTransition = errors.New("invalid status transition")
	ErrLockConflict      = errors.New("lock conflict")
)

// sqliteTimeFormat is the default format SQLite uses for CURRENT_TIMESTAMP.
const sqliteTimeFormat = "2006-01-02 15:04:05"

// --- Run status lifecycle ---

type RunStatus string

const (
	RunDraftSRS            RunStatus = "draft_srs"
	RunAwaitingSRSApproval RunStatus = "awaiting_srs_approval"
	RunActive              RunStatus = "active"
	RunPaused              RunStatus = "paused"
	RunCancelled           RunStatus = "cancelled"
	RunCompleted           RunStatus = "completed"
	RunError               RunStatus = "error"
)

var validRunTransitions = map[RunStatus][]RunStatus{
	RunDraftSRS:            {RunAwaitingSRSApproval},
	RunAwaitingSRSApproval: {RunActive, RunDraftSRS},
	RunActive:              {RunPaused, RunCancelled, RunCompleted, RunError},
	RunPaused:              {RunActive, RunCancelled},
}

func ValidRunTransition(from, to RunStatus) bool {
	for _, s := range validRunTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// --- Task status lifecycle ---

type TaskStatus string

const (
	TaskQueued        TaskStatus = "queued"
	TaskInProgress    TaskStatus = "in_progress"
	TaskWaitingOnLock TaskStatus = "waiting_on_lock"
	TaskDone          TaskStatus = "done"
	TaskFailed        TaskStatus = "failed"
	TaskBlocked       TaskStatus = "blocked"
	TaskCancelledECO  TaskStatus = "cancelled_eco"
)

var validTaskTransitions = map[TaskStatus][]TaskStatus{
	TaskQueued:        {TaskInProgress, TaskWaitingOnLock, TaskCancelledECO},
	TaskWaitingOnLock: {TaskInProgress, TaskQueued, TaskCancelledECO},
	TaskInProgress:    {TaskDone, TaskFailed, TaskBlocked, TaskCancelledECO},
	TaskFailed:        {TaskQueued}, // retry or escalation (Section 15.4)
}

func ValidTaskTransition(from, to TaskStatus) bool {
	for _, s := range validTaskTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// --- Attempt status lifecycle ---

type AttemptStatus string

const (
	AttemptRunning   AttemptStatus = "running"
	AttemptPassed    AttemptStatus = "passed"
	AttemptFailed    AttemptStatus = "failed"
	AttemptEscalated AttemptStatus = "escalated"
)

var validAttemptTransitions = map[AttemptStatus][]AttemptStatus{
	AttemptRunning: {AttemptPassed, AttemptFailed, AttemptEscalated},
}

func ValidAttemptTransition(from, to AttemptStatus) bool {
	for _, s := range validAttemptTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// --- Attempt phase lifecycle (Section 15.2) ---

type AttemptPhase string

const (
	PhaseExecuting               AttemptPhase = "executing"
	PhaseValidating              AttemptPhase = "validating"
	PhaseReviewing               AttemptPhase = "reviewing"
	PhaseAwaitingOrchestratorGate AttemptPhase = "awaiting_orchestrator_gate"
	PhaseQueuedForMerge          AttemptPhase = "queued_for_merge"
	PhaseMerging                 AttemptPhase = "merging"
	PhaseSucceeded               AttemptPhase = "succeeded"
	PhaseFailed                  AttemptPhase = "failed"
	PhaseEscalated               AttemptPhase = "escalated"
)

var validPhaseTransitions = map[AttemptPhase][]AttemptPhase{
	PhaseExecuting:                {PhaseValidating, PhaseFailed, PhaseEscalated},
	PhaseValidating:               {PhaseReviewing, PhaseFailed, PhaseEscalated},
	PhaseReviewing:                {PhaseAwaitingOrchestratorGate, PhaseFailed, PhaseEscalated},
	PhaseAwaitingOrchestratorGate: {PhaseQueuedForMerge, PhaseFailed, PhaseEscalated},
	PhaseQueuedForMerge:           {PhaseMerging, PhaseFailed, PhaseEscalated},
	PhaseMerging:                  {PhaseSucceeded, PhaseFailed, PhaseEscalated},
}

func ValidPhaseTransition(from, to AttemptPhase) bool {
	for _, p := range validPhaseTransitions[from] {
		if p == to {
			return true
		}
	}
	return false
}

// --- ECO status lifecycle ---

type ECOStatus string

const (
	ECOProposed ECOStatus = "proposed"
	ECOApproved ECOStatus = "approved"
	ECORejected ECOStatus = "rejected"
)

var validECOTransitions = map[ECOStatus][]ECOStatus{
	ECOProposed: {ECOApproved, ECORejected},
}

func ValidECOTransition(from, to ECOStatus) bool {
	for _, s := range validECOTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// --- Enumeration types ---

type TaskTier string

const (
	TierLocal    TaskTier = "local"
	TierCheap    TaskTier = "cheap"
	TierStandard TaskTier = "standard"
	TierPremium  TaskTier = "premium"
)

type TaskType string

const (
	TaskTypeImplementation TaskType = "implementation"
	TaskTypeTest           TaskType = "test"
	TaskTypeReview         TaskType = "review"
)

type ContainerType string

const (
	ContainerMeeseeks        ContainerType = "meeseeks"
	ContainerReviewer        ContainerType = "reviewer"
	ContainerValidator       ContainerType = "validator"
	ContainerSubOrchestrator ContainerType = "sub_orchestrator"
)

type SessionMode string

const (
	SessionBootstrap SessionMode = "bootstrap"
	SessionApproval  SessionMode = "approval"
	SessionExecution SessionMode = "execution"
	SessionPostrun   SessionMode = "postrun"
)

type ValidationCheckType string

const (
	CheckCompile  ValidationCheckType = "compile"
	CheckLint     ValidationCheckType = "lint"
	CheckTest     ValidationCheckType = "test"
	CheckSecurity ValidationCheckType = "security"
)

type ValidationStatus string

const (
	ValidationPass ValidationStatus = "pass"
	ValidationFail ValidationStatus = "fail"
	ValidationSkip ValidationStatus = "skip"
)

type ReviewVerdict string

const (
	ReviewApprove ReviewVerdict = "approve"
	ReviewReject  ReviewVerdict = "reject"
)

type ArtifactOp string

const (
	ArtifactAdd    ArtifactOp = "add"
	ArtifactModify ArtifactOp = "modify"
	ArtifactDelete ArtifactOp = "delete"
	ArtifactRename ArtifactOp = "rename"
)

// --- Domain model structs ---

type Project struct {
	ID        string
	RootPath  string
	Name      string
	Slug      string
	CreatedAt time.Time
}

type ProjectRun struct {
	ID                   string
	ProjectID            string
	Status               RunStatus
	BaseBranch           string
	WorkBranch           string
	OrchestratorMode     string
	OrchestratorRuntime  string
	OrchestratorIdentity *string
	SRSApprovalDelegate  string
	BudgetMaxUSD         float64
	ConfigSnapshot       string
	SRSHash              *string
	StartedAt            time.Time
	PausedAt             *time.Time
	CancelledAt          *time.Time
	CompletedAt          *time.Time
}

type Task struct {
	ID           string
	RunID        string
	ParentID     *string
	Title        string
	Description  *string
	Status       TaskStatus
	Tier         TaskTier
	TaskType     TaskType
	BaseSnapshot *string
	ECORef       *int64
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

type TaskSRSRef struct {
	TaskID string
	SRSRef string
}

type TaskDependency struct {
	TaskID    string
	DependsOn string
}

type TaskTargetFile struct {
	TaskID          string
	FilePath        string
	LockScope       string
	LockResourceKey string
}

type TaskLock struct {
	ResourceType string
	ResourceKey  string
	TaskID       string
	LockedAt     time.Time
}

type TaskLockWait struct {
	TaskID             string
	WaitReason         string
	RequestedResources string
	BlockedByTaskID    *string
	CreatedAt          time.Time
}

type TaskAttempt struct {
	ID            int64
	TaskID        string
	AttemptNumber int
	ModelID       string
	ModelFamily   string
	BaseSnapshot  string
	Status        AttemptStatus
	Phase         AttemptPhase
	InputTokens   *int64
	OutputTokens  *int64
	CostUSD       float64
	FailureReason *string
	Feedback      *string
	StartedAt     time.Time
	CompletedAt   *time.Time
}

type ValidationRun struct {
	ID         int64
	AttemptID  int64
	CheckType  ValidationCheckType
	Status     ValidationStatus
	Output     *string
	DurationMs *int64
	Timestamp  time.Time
}

type ReviewRun struct {
	ID             int64
	AttemptID      int64
	ReviewerModel  string
	ReviewerFamily string
	Verdict        ReviewVerdict
	Feedback       *string
	CostUSD        float64
	Timestamp      time.Time
}

type TaskArtifact struct {
	ID           int64
	AttemptID    int64
	Operation    ArtifactOp
	PathFrom     *string
	PathTo       *string
	SHA256Before *string
	SHA256After  *string
	SizeBefore   *int64
	SizeAfter    *int64
	Timestamp    time.Time
}

type ContainerSession struct {
	ID            string
	RunID         string
	TaskID        string
	ContainerType ContainerType
	Image         string
	ModelID       *string
	CPULimit      *float64
	MemLimit      *string
	StartedAt     time.Time
	StoppedAt     *time.Time
	ExitReason    *string
}

type Event struct {
	ID        int64
	RunID     string
	EventType string
	TaskID    *string
	AgentType *string
	AgentID   *string
	Details   *string
	Timestamp time.Time
}

type CostLogEntry struct {
	ID           int64
	RunID        string
	TaskID       *string
	AttemptID    *int64
	AgentType    string
	ModelID      string
	InputTokens  *int64
	OutputTokens *int64
	CostUSD      float64
	Timestamp    time.Time
}

type ECOLogEntry struct {
	ID             int64
	RunID          string
	ECOCode        string
	Category       string
	Description    string
	AffectedRefs   string
	ProposedChange string
	Status         ECOStatus
	ApprovedBy     *string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}

type UISession struct {
	ID           string
	ProjectID    string
	RunID        *string
	Name         *string
	Mode         SessionMode
	CreatedAt    time.Time
	LastActiveAt time.Time
}

type UIMessage struct {
	ID            int64
	SessionID     string
	Seq           int
	Role          string
	Kind          string
	Content       string
	RelatedTaskID *string
	RequestID     *string
	CreatedAt     time.Time
}

type UISessionSummary struct {
	ID          int64
	SessionID   string
	SummaryKind string
	Content     string
	CreatedAt   time.Time
}

type UIInputHistory struct {
	ID        int64
	ProjectID string
	SessionID *string
	InputMode string
	Content   string
	CreatedAt time.Time
}

// --- Transactional helper ---

// WithTx runs fn inside a database transaction, committing on success
// and rolling back on error or panic.
func (d *DB) WithTx(fn func(tx *sql.Tx) error) error {
	sqlTx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			sqlTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(sqlTx); err != nil {
		sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// --- Model registry (Section 18.3) ---

type ModelRegistryEntry struct {
	ID                    string
	Family                string
	Source                string // openrouter, bitnet, shipped
	Tier                  TaskTier
	ContextWindow         int
	MaxOutput             int
	PromptPerMillion      float64
	CompletionPerMillion  float64
	Strengths             []string
	Weaknesses            []string
	SupportsTools         bool
	SupportsVision        bool
	SupportsGrammar       bool
	RecommendedFor        []string
	NotRecommendedFor     []string
	HistoricalSuccessRate *float64
	AvgCostPerTask        *float64
	LastUpdated           time.Time
}

// --- Time scan helpers ---

// sqliteTimeFormats lists the timestamp formats SQLite/modernc may return.
var sqliteTimeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	sqliteTimeFormat,
}

// parseTime parses a SQLite timestamp string into time.Time.
func parseTime(s string) time.Time {
	for _, f := range sqliteTimeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseNullTime parses a nullable SQLite timestamp string.
func parseNullTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t := parseTime(*s)
	if t.IsZero() {
		return nil
	}
	return &t
}
