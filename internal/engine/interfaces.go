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

// InferenceRequest describes a model inference request from an agent.
type InferenceRequest struct {
	ModelID     string
	Prompt      string
	MaxTokens   int
	Temperature float64
	TaskID      string
}

// InferenceResponse holds the result of a model inference request.
type InferenceResponse struct {
	Content      string
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	ModelID      string
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
