package engine

import "context"

// GitService abstracts git operations for testability.
// The engine uses this interface instead of calling git directly.
type GitService interface {
	CurrentBranch(dir string) (string, error)
	CreateBranch(dir, name string) error
	CurrentHEAD(dir string) (string, error)
	IsDirty(dir string) (bool, error)
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

// ContainerService abstracts Docker container lifecycle for testability.
type ContainerService interface {
	Start(ctx context.Context, spec ContainerSpec) (string, error)
	Stop(ctx context.Context, id string) error
	ListRunning(ctx context.Context) ([]string, error)
	Cleanup(ctx context.Context) error
}

// InferenceMessage represents a single message in an inference conversation.
type InferenceMessage struct {
	Role    string
	Content string
}

// InferenceRequest describes a model inference request from an agent.
// Per Architecture Section 19.5 and 20.4.
type InferenceRequest struct {
	RunID              string
	TaskID             string
	AttemptID          int64
	AgentType          string // meeseeks, reviewer, orchestrator, sub_orchestrator
	ModelID            string
	Tier               string // local, cheap, standard, premium
	Messages           []InferenceMessage
	Prompt             string  // legacy single-prompt shorthand; Messages takes precedence
	MaxTokens          int
	Temperature        float64
	GrammarConstraints *string // GBNF grammar for BitNet structured output
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
