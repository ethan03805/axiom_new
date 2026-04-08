package engine

import (
	"context"
	"time"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/state"
)

// GitService abstracts git operations for testability.
// The engine uses this interface instead of calling git directly.
type GitService interface {
	CurrentBranch(dir string) (string, error)
	CreateBranch(dir, name string) error
	CurrentHEAD(dir string) (string, error)
	IsDirty(dir string) (bool, error)
	ValidateClean(dir string) error
	SetupWorkBranch(dir, baseBranch, workBranch string) error
	// SetupWorkBranchAllowDirty is the recovery-mode variant that skips the
	// internal clean-tree check, carrying uncommitted changes over onto the
	// work branch. Only called by Engine.StartRun when AllowDirty is set.
	SetupWorkBranchAllowDirty(dir, baseBranch, workBranch string) error
	// CancelCleanup reverts uncommitted changes and switches back to the base
	// branch. Called by Engine.CancelRun to satisfy Architecture §23.4 —
	// committed work on the work branch is preserved; only uncommitted state
	// is discarded.
	CancelCleanup(dir, baseBranch string) error
	AddFiles(dir string, files []string) error
	Commit(dir string, message string) (string, error)
	ChangedFilesSince(dir, sinceRef string) ([]string, error)
}

// ContainerSpec describes a container to be started.
type ContainerSpec struct {
	Name      string
	Image     string
	CPULimit  float64
	MemLimit  string
	Network   string
	Mounts    []string
	Env       map[string]string
	TimeoutMs int64
}

// ExecResult holds the outcome of running a command inside a container via
// docker exec. Per Architecture Section 13.5, the DockerCheckRunner uses this
// to turn profile commands (go build, npm test, …) into CheckResults.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// ContainerService abstracts Docker container lifecycle for testability.
type ContainerService interface {
	Start(ctx context.Context, spec ContainerSpec) (string, error)
	Stop(ctx context.Context, id string) error
	ListRunning(ctx context.Context) ([]string, error)
	Cleanup(ctx context.Context) error
	// Exec runs a command inside a running container started via Start.
	// Returns the exit code plus captured stdout/stderr. A non-zero exit code
	// is NOT an error — it is a normal result. err is non-nil only on
	// infrastructure failures (container not found, docker daemon down).
	Exec(ctx context.Context, containerID string, cmd []string) (ExecResult, error)
}

// InferenceMessage represents a single message in an inference conversation.
type InferenceMessage struct {
	Role    string
	Content string
}

// InferenceRequest describes a model inference request from an agent.
// Per Architecture Section 19.5 and 20.4.
type InferenceRequest struct {
	RunID                     string
	TaskID                    string
	AttemptID                 int64
	AgentType                 string // meeseeks, reviewer, orchestrator, sub_orchestrator
	ModelID                   string
	Tier                      string // local, cheap, standard, premium
	Messages                  []InferenceMessage
	Prompt                    string   // legacy single-prompt shorthand; Messages takes precedence
	ContextFiles              []string // repo-derived files represented in the prompt payload
	AllowExternalForSensitive bool     // explicit user override for redacted sensitive context
	MaxTokens                 int
	Temperature               float64
	GrammarConstraints        *string // GBNF grammar for BitNet structured output
}

// InferenceResponse holds the result of a model inference request.
type InferenceResponse struct {
	Content      string
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	ModelID      string
	FinishReason string // stop, length, tool_calls, content_filter, error
	ProviderName string // openrouter, bitnet
}

// InferenceService abstracts model inference brokering for testability.
// The engine brokers all model API calls per Architecture Section 4.1.
type InferenceService interface {
	Available() bool
	Infer(ctx context.Context, req InferenceRequest) (*InferenceResponse, error)
}

// ValidationRename describes a rename that must be applied before checks run.
type ValidationRename struct {
	From string
	To   string
}

// ValidationCheckRequest describes the staged output to validate.
type ValidationCheckRequest struct {
	TaskID      string
	RunID       string
	Image       string
	StagingDir  string
	ProjectDir  string
	Config      *config.ValidationConfig
	Languages   []string
	DeleteFiles []string
	RenameFiles []ValidationRename
}

// ValidationCheckResult captures one automated check outcome.
type ValidationCheckResult struct {
	CheckType  state.ValidationCheckType
	Status     state.ValidationStatus
	Output     string
	DurationMs int64
}

// ValidationService runs automated checks for staged task output.
type ValidationService interface {
	RunChecks(ctx context.Context, req ValidationCheckRequest) ([]ValidationCheckResult, error)
}

// ReviewRunRequest describes a reviewer evaluation request.
type ReviewRunRequest struct {
	TaskID         string
	RunID          string
	Image          string
	SpecDir        string
	TaskTier       state.TaskTier
	MeeseeksFamily string
	CPULimit       float64
	MemLimit       string
	AffectedFiles  []string
}

// ReviewRunResult holds the parsed reviewer verdict and metadata.
type ReviewRunResult struct {
	Verdict        state.ReviewVerdict
	Feedback       string
	ReviewerModel  string
	ReviewerFamily string
	ReviewerTier   state.TaskTier
}

// ReviewService orchestrates the reviewer stage.
type ReviewService interface {
	RunReview(ctx context.Context, req ReviewRunRequest) (*ReviewRunResult, error)
}

// TaskFailureAction records how the task service routed a failed task.
type TaskFailureAction string

const (
	TaskFailureRetry    TaskFailureAction = "retry"
	TaskFailureEscalate TaskFailureAction = "escalate"
	TaskFailureBlock    TaskFailureAction = "block"
)

// TargetFileSpec describes an engine-level target file and lock scope.
type TargetFileSpec struct {
	FilePath        string
	LockScope       string
	LockResourceKey string
}

// TaskService handles retry/escalation decisions and scope expansion.
type TaskService interface {
	HandleTaskFailure(ctx context.Context, taskID string, feedback string) (TaskFailureAction, error)
	RequestScopeExpansion(ctx context.Context, taskID string, additionalFiles []TargetFileSpec) error
}

// IndexService abstracts semantic indexing for testability.
// Per Architecture Section 17, the indexer maintains a structured index
// of project code symbols, exports, and dependency relationships.
type IndexService interface {
	// Index performs a full project index.
	Index(ctx context.Context, dir string) error
	// IndexFiles performs incremental indexing of specific files.
	IndexFiles(ctx context.Context, dir string, paths []string) error
	// LookupSymbol finds symbols by name, optionally filtered by kind.
	LookupSymbol(ctx context.Context, name, kind string) ([]SymbolResult, error)
	// ReverseDependencies returns files/symbols that reference the given symbol.
	ReverseDependencies(ctx context.Context, symbolName string) ([]ReferenceResult, error)
	// ListExports returns all exported symbols for a package directory.
	ListExports(ctx context.Context, packagePath string) ([]SymbolResult, error)
	// FindImplementations returns types implementing the given interface.
	FindImplementations(ctx context.Context, interfaceName string) ([]ReferenceResult, error)
	// ModuleGraph returns the package dependency graph.
	ModuleGraph(ctx context.Context, rootPackage string) (*ModuleGraphResult, error)
}

// SymbolResult represents a symbol found in the index.
// Per Architecture Section 17.5.
type SymbolResult struct {
	Name      string
	Kind      string // function, type, interface, constant, variable, field, method
	FilePath  string
	Line      int
	Signature string
	Exported  bool
}

// ReferenceResult represents a reference to a symbol.
// Per Architecture Section 17.5.
type ReferenceResult struct {
	FilePath   string
	Line       int
	SymbolName string
	UsageType  string // call, reference, implementation
}

// ModuleGraphResult holds the package dependency graph.
// Per Architecture Section 17.5.
type ModuleGraphResult struct {
	Packages []PackageNode
	Edges    []PackageEdge
}

// PackageNode represents a package in the dependency graph.
type PackageNode struct {
	Path string
	Dir  string
}

// PackageEdge represents a dependency edge between packages.
type PackageEdge struct {
	From string
	To   string
}

// ModelService abstracts model registry operations for testability.
// Per Architecture Section 18, the registry feeds the broker's model allowlist.
type ModelService interface {
	RefreshShipped() error
	RefreshOpenRouter(ctx context.Context, baseURL string) error
	RefreshBitNet(ctx context.Context, baseURL string) error
	List(tier, family string) ([]ModelInfo, error)
	Get(id string) (*ModelInfo, error)
}

// ModelInfo is the engine-level view of a registered model.
type ModelInfo struct {
	ID                    string
	Family                string
	Source                string
	Tier                  string
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
	LastUpdated           string
}
