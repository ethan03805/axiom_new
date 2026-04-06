package engine

import "context"

// GitService abstracts git operations for testability.
// The engine uses this interface instead of calling git directly.
type GitService interface {
	CurrentBranch(dir string) (string, error)
	CreateBranch(dir, name string) error
	CurrentHEAD(dir string) (string, error)
	IsDirty(dir string) (bool, error)
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
type IndexService interface {
	Index(ctx context.Context, dir string) error
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
