# Git Operations Reference

Axiom's git operations are handled by the `internal/gitops` package. All git interactions are mediated through the `Manager`, which ensures branch naming, commit formatting, snapshot tracking, and workspace safety conform to the architecture (Sections 16, 23).

## Design Constraints

- **Engine authority** — Only the trusted engine (via `gitops.Manager`) performs git operations. LLM agents never interact with git directly.
- **No remote operations** — The Manager has no push, pull, fetch, or remote-related methods. Axiom never modifies remotes automatically (Architecture Section 23.4).
- **Deterministic branching** — Work branches are created at the exact HEAD of the base branch with a predictable name.

## Manager

```go
import "github.com/openaxiom/axiom/internal/gitops"

mgr := gitops.New(logger)
```

The `Manager` satisfies the `engine.GitService` interface and provides additional methods for the full git workflow.

## Branch Management

### Branch Naming Convention

All work branches follow the pattern `axiom/<project-slug>` (Architecture Section 23.1). The project slug is derived from the project name during `axiom init`.

### Methods

| Method | Description |
|--------|-------------|
| `CurrentBranch(dir) (string, error)` | Returns the currently checked-out branch name |
| `CreateBranch(dir, name) error` | Creates a new branch at HEAD without checking it out |
| `CreateAndCheckoutBranch(dir, name) error` | Creates a new branch and switches to it |
| `CheckoutBranch(dir, name) error` | Switches to an existing branch |
| `BranchExists(dir, name) (bool, error)` | Reports whether a local branch exists |
| `SetupWorkBranch(dir, baseBranch, workBranch) error` | Creates or resumes a work branch (see below) |

### SetupWorkBranch

`SetupWorkBranch` is the high-level method for preparing the git workspace:

- **New run:** Validates the working tree is clean, creates the work branch from the base branch's HEAD, and checks it out.
- **Resume:** If the work branch already exists (e.g., after a crash or pause), checks it out directly.

```go
// New run: creates axiom/my-project from main
err := mgr.SetupWorkBranch(dir, "main", "axiom/my-project")

// Resume: detects existing branch and checks it out
err := mgr.SetupWorkBranch(dir, "main", "axiom/my-project")
```

Fails with an error if the working tree has uncommitted changes.

## Snapshots

| Method | Description |
|--------|-------------|
| `CurrentHEAD(dir) (string, error)` | Returns the full 40-character SHA of HEAD |
| `Snapshot(dir) (string, error)` | Alias for `CurrentHEAD` — canonical method for capturing `base_snapshot` |

Per Architecture Section 16.2, every TaskSpec includes a `base_snapshot` field containing the git SHA the TaskSpec was generated against. Use `Snapshot()` to capture this value before task dispatch.

```go
sha, err := mgr.Snapshot(dir)
// sha = "abc123def456789..." (40 chars)
```

## Working-Copy Validation

| Method | Description |
|--------|-------------|
| `IsDirty(dir) (bool, error)` | Reports whether the working tree has uncommitted changes (staged, unstaged, or untracked) |
| `ValidateClean(dir) error` | Returns an actionable error if the working tree is dirty |

The engine refuses to start a run on a dirty working tree (Architecture Section 28.2). `ValidateClean` is called by `SetupWorkBranch` automatically.

## Commit Operations

### CommitInfo

```go
type CommitInfo struct {
    TaskTitle     string   // e.g., "Implement user auth"
    TaskID        string   // e.g., "task-042"
    SRSRefs       []string // e.g., ["FR-001", "AC-003"]
    MeeseeksModel string   // e.g., "anthropic/claude-4-sonnet"
    ReviewerModel string   // e.g., "openai/gpt-4o"
    AttemptNumber int      // e.g., 2
    MaxAttempts   int      // e.g., 3
    CostUSD       float64  // e.g., 0.0234
    BaseSnapshot  string   // e.g., "abc123d"
}
```

### Commit Message Format

Per Architecture Section 23.2, every task commit follows this format:

```
[axiom] <task-title>

Task: <task-id>
SRS Refs: FR-001, AC-003
Meeseeks Model: anthropic/claude-4-sonnet
Reviewer Model: openai/gpt-4o
Attempt: 2/3
Cost: $0.0234
Base Snapshot: abc123d
```

### Methods

| Method | Description |
|--------|-------------|
| `AddFiles(dir, files) error` | Stages specified files for commit |
| `Commit(dir, message) (string, error)` | Creates a commit with the given message, returns SHA |
| `CommitTask(dir, info, files) (string, error)` | Stages files and commits with architecture-format message |
| `FormatCommitMessage(info) string` | Pure function that builds the commit message string |

```go
sha, err := mgr.CommitTask(dir, gitops.CommitInfo{
    TaskTitle:     "Implement user auth",
    TaskID:        "task-042",
    SRSRefs:       []string{"FR-001", "AC-003"},
    MeeseeksModel: "anthropic/claude-4-sonnet",
    ReviewerModel: "openai/gpt-4o",
    AttemptNumber: 2,
    MaxAttempts:   3,
    CostUSD:       0.0234,
    BaseSnapshot:  "abc123d",
}, []string{"internal/auth/auth.go", "internal/auth/auth_test.go"})
```

## Diff Helpers

| Method | Description |
|--------|-------------|
| `Diff(dir, from, to) (string, error)` | Diff between two refs (two-dot: `from..to`) |
| `DiffStaged(dir) (string, error)` | Diff of currently staged changes |
| `DiffWorkBranch(dir, baseBranch, workBranch) (string, error)` | Three-dot diff showing changes since branch divergence |
| `ChangedFilesSince(dir, sinceRef) ([]string, error)` | Returns file paths changed between `sinceRef` and HEAD (used by merge queue for conflict detection) |

`DiffWorkBranch` is used for the final branch review before the user merges (Architecture Section 23.4):

```go
diff, err := mgr.DiffWorkBranch(dir, "main", "axiom/my-project")
```

## Cancellation Cleanup

| Method | Description |
|--------|-------------|
| `CancelCleanup(dir, baseBranch) error` | Reverts uncommitted changes and returns to base branch |

When a run is cancelled, the engine calls `CancelCleanup` to:

1. Discard all staged and unstaged changes (`git reset --hard HEAD`)
2. Remove untracked files and directories (`git clean -fd`)
3. Switch back to the base branch (`git checkout <baseBranch>`)

Committed work on the work branch is **preserved** — the branch is not deleted. The user can review it, cherry-pick from it, or delete it at their discretion.

```go
err := mgr.CancelCleanup(dir, "main")
// Now on "main" with clean working tree
// "axiom/my-project" branch still exists with any committed work
```

## Integration with Engine

The `gitops.Manager` satisfies the `engine.GitService` interface:

```go
type GitService interface {
    CurrentBranch(dir string) (string, error)
    CreateBranch(dir, name string) error
    CurrentHEAD(dir string) (string, error)
    IsDirty(dir string) (bool, error)
    AddFiles(dir string, files []string) error
    Commit(dir string, message string) (string, error)
    ChangedFilesSince(dir, sinceRef string) ([]string, error)
}
```

Pass it as the `Git` field in `engine.Options`:

```go
eng, err := engine.New(engine.Options{
    Config:  cfg,
    DB:      db,
    RootDir: root,
    Log:     logger,
    Git:     gitops.New(logger),
})
```

## Test Coverage

The gitops package has 38 tests covering:

- Branch creation, checkout, existence checks, and error cases
- Snapshot capturing and HEAD resolution
- Dirty detection for untracked files, staged changes, and modified tracked files
- Commit message formatting with exact architecture compliance verification
- File staging and commit with SHA verification
- Diffs between refs, staged changes, work branches, and changed-files-since queries
- SetupWorkBranch for both new and resume scenarios
- CancelCleanup for uncommitted changes, committed work preservation, and clean branches
- Deterministic branch creation (exit criteria)
- Architecture-compliant commit format (exit criteria)
