// Package gitops implements deterministic, architecture-compliant git operations
// for the Axiom engine. All git interactions are mediated through the Manager,
// which ensures branch naming, commit formatting, snapshot tracking, and
// workspace safety conform to the architecture (Sections 16, 23).
//
// Wiring: the engine's high-level Engine.StartRun calls ValidateClean and
// SetupWorkBranch to enforce the architecture's clean-tree contract and
// switch onto axiom/<slug> before the external orchestrator takes over.
// Engine.CancelRun calls CancelCleanup to revert uncommitted state and
// return to the base branch; committed work on the work branch is
// preserved per Architecture §23.4. The recovery-mode escape hatch
// exposed by `axiom run --allow-dirty` routes through the
// SetupWorkBranchAllowDirty variant which carries uncommitted state over
// onto the work branch.
//
// Design constraint (Section 23.4): This package SHALL NOT push, pull, fetch,
// or merge to/from remote repositories. Axiom never modifies remotes automatically.
package gitops

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// CommitInfo holds the metadata for an architecture-compliant commit message.
// See Architecture Section 23.2 for the required format.
type CommitInfo struct {
	TaskTitle     string
	TaskID        string
	SRSRefs       []string
	MeeseeksModel string
	ReviewerModel string
	AttemptNumber int
	MaxAttempts   int
	CostUSD       float64
	BaseSnapshot  string
}

// Manager provides deterministic git operations for the Axiom engine.
// It satisfies the engine.GitService interface and adds additional methods
// required by Phase 4 (branch management, commit formatting, diffs, cleanup).
type Manager struct {
	log *slog.Logger
}

// New creates a new git operations Manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log}
}

// CurrentBranch returns the name of the currently checked-out branch.
func (m *Manager) CurrentBranch(dir string) (string, error) {
	out, err := m.git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return out, nil
}

// CreateBranch creates a new branch at the current HEAD without checking it out.
func (m *Manager) CreateBranch(dir, name string) error {
	if _, err := m.git(dir, "branch", name); err != nil {
		return fmt.Errorf("creating branch %s: %w", name, err)
	}
	return nil
}

// CreateAndCheckoutBranch creates a new branch and switches to it.
func (m *Manager) CreateAndCheckoutBranch(dir, name string) error {
	if _, err := m.git(dir, "checkout", "-b", name); err != nil {
		return fmt.Errorf("creating and checking out branch %s: %w", name, err)
	}
	m.log.Info("created work branch", "branch", name)
	return nil
}

// CheckoutBranch switches to an existing branch.
func (m *Manager) CheckoutBranch(dir, name string) error {
	if _, err := m.git(dir, "checkout", name); err != nil {
		return fmt.Errorf("checking out branch %s: %w", name, err)
	}
	return nil
}

// BranchExists reports whether a local branch with the given name exists.
func (m *Manager) BranchExists(dir, name string) (bool, error) {
	_, err := m.git(dir, "rev-parse", "--verify", "refs/heads/"+name)
	if err != nil {
		// rev-parse exits non-zero when the ref doesn't exist
		return false, nil
	}
	return true, nil
}

// CurrentHEAD returns the full SHA of the current HEAD commit.
func (m *Manager) CurrentHEAD(dir string) (string, error) {
	out, err := m.git(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}
	return out, nil
}

// Snapshot returns the current HEAD SHA. This is the canonical way to
// capture a base_snapshot for task attempts (Architecture Section 16.2).
func (m *Manager) Snapshot(dir string) (string, error) {
	return m.CurrentHEAD(dir)
}

// IsDirty reports whether the working tree has uncommitted changes
// (staged, unstaged, or untracked files).
func (m *Manager) IsDirty(dir string) (bool, error) {
	out, err := m.git(dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("checking dirty state: %w", err)
	}
	return out != "", nil
}

// ValidateClean returns an error if the working tree has uncommitted changes.
// Per Architecture Section 28.2: the engine SHALL refuse to start on a dirty tree.
func (m *Manager) ValidateClean(dir string) error {
	dirty, err := m.IsDirty(dir)
	if err != nil {
		return err
	}
	if dirty {
		return errors.New("working tree has uncommitted changes; commit or stash before running axiom")
	}
	return nil
}

// AddFiles stages the specified files for commit.
func (m *Manager) AddFiles(dir string, files []string) error {
	if len(files) == 0 {
		return errors.New("no files to add")
	}
	args := append([]string{"add", "--"}, files...)
	if _, err := m.git(dir, args...); err != nil {
		return fmt.Errorf("staging files: %w", err)
	}
	return nil
}

// Commit creates a commit with the given message. Returns the new commit SHA.
// Fails if there is nothing staged.
func (m *Manager) Commit(dir string, message string) (string, error) {
	if _, err := m.git(dir, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}
	sha, err := m.CurrentHEAD(dir)
	if err != nil {
		return "", fmt.Errorf("reading commit SHA after commit: %w", err)
	}
	return sha, nil
}

// CommitTask stages the given files and commits them with an
// architecture-compliant message (Section 23.2). Returns the commit SHA.
func (m *Manager) CommitTask(dir string, info CommitInfo, files []string) (string, error) {
	if len(files) == 0 {
		return "", errors.New("no files to commit")
	}

	if err := m.AddFiles(dir, files); err != nil {
		return "", err
	}

	msg := FormatCommitMessage(info)
	sha, err := m.Commit(dir, msg)
	if err != nil {
		return "", err
	}

	m.log.Info("committed task output",
		"task_id", info.TaskID,
		"sha", sha[:8],
		"files", len(files),
	)
	return sha, nil
}

// FormatCommitMessage builds the commit message per Architecture Section 23.2:
//
//	[axiom] <task-title>
//
//	Task: <task-id>
//	SRS Refs: FR-001, AC-003
//	Meeseeks Model: anthropic/claude-4-sonnet
//	Reviewer Model: openai/gpt-4o
//	Attempt: 2/3
//	Cost: $0.0234
//	Base Snapshot: abc123d
func FormatCommitMessage(info CommitInfo) string {
	var b strings.Builder

	fmt.Fprintf(&b, "[axiom] %s\n\n", info.TaskTitle)
	fmt.Fprintf(&b, "Task: %s\n", info.TaskID)
	fmt.Fprintf(&b, "SRS Refs: %s\n", strings.Join(info.SRSRefs, ", "))
	fmt.Fprintf(&b, "Meeseeks Model: %s\n", info.MeeseeksModel)
	fmt.Fprintf(&b, "Reviewer Model: %s\n", info.ReviewerModel)
	fmt.Fprintf(&b, "Attempt: %d/%d\n", info.AttemptNumber, info.MaxAttempts)
	fmt.Fprintf(&b, "Cost: $%.4f\n", info.CostUSD)
	fmt.Fprintf(&b, "Base Snapshot: %s", info.BaseSnapshot)

	return b.String()
}

// Diff returns the diff between two refs (commits, branches, or tags).
func (m *Manager) Diff(dir, from, to string) (string, error) {
	out, err := m.git(dir, "diff", from+".."+to)
	if err != nil {
		return "", fmt.Errorf("diffing %s..%s: %w", from, to, err)
	}
	return out, nil
}

// DiffStaged returns the diff of currently staged changes.
func (m *Manager) DiffStaged(dir string) (string, error) {
	out, err := m.git(dir, "diff", "--staged")
	if err != nil {
		return "", fmt.Errorf("getting staged diff: %w", err)
	}
	return out, nil
}

// DiffWorkBranch returns the diff of changes on the work branch relative
// to the base branch, using three-dot notation (changes since divergence).
func (m *Manager) DiffWorkBranch(dir, baseBranch, workBranch string) (string, error) {
	out, err := m.git(dir, "diff", baseBranch+"..."+workBranch)
	if err != nil {
		return "", fmt.Errorf("diffing %s...%s: %w", baseBranch, workBranch, err)
	}
	return out, nil
}

// DiffRange satisfies engine.GitService.DiffRange. It returns the diff of
// `head` against the merge base with `base` (three-dot notation), and is
// the entrypoint used by the TUI `/diff` slash command. If either ref is
// missing locally, the underlying git command returns a descriptive error
// that the caller surfaces in the transcript rather than swallowing.
func (m *Manager) DiffRange(dir, base, head string) (string, error) {
	return m.DiffWorkBranch(dir, base, head)
}

// SetupWorkBranch prepares the git workspace for a run. If the work branch
// already exists (resume case), it checks it out. If it doesn't exist (new run),
// it creates the branch from the current HEAD.
// The working tree must be clean before calling this.
func (m *Manager) SetupWorkBranch(dir, baseBranch, workBranch string) error {
	if err := m.ValidateClean(dir); err != nil {
		return fmt.Errorf("cannot setup work branch: %w", err)
	}
	return m.setupWorkBranchBody(dir, baseBranch, workBranch)
}

// SetupWorkBranchAllowDirty is the recovery-mode variant of SetupWorkBranch.
// It skips the clean-tree precondition, carrying any uncommitted changes
// over onto the work branch. Used exclusively by the engine's StartRun when
// StartRunOptions.AllowDirty is set (Architecture §28.2 escape hatch).
//
// The caller is responsible for logging the bypass — this method performs
// no safety warnings of its own.
func (m *Manager) SetupWorkBranchAllowDirty(dir, baseBranch, workBranch string) error {
	return m.setupWorkBranchBody(dir, baseBranch, workBranch)
}

// setupWorkBranchBody holds the branch-resolution logic shared by both
// SetupWorkBranch and SetupWorkBranchAllowDirty. It assumes any cleanliness
// check has already been performed (or intentionally bypassed) by the caller.
func (m *Manager) setupWorkBranchBody(dir, baseBranch, workBranch string) error {
	exists, err := m.BranchExists(dir, workBranch)
	if err != nil {
		return err
	}

	if exists {
		m.log.Info("resuming existing work branch", "branch", workBranch)
		return m.CheckoutBranch(dir, workBranch)
	}

	// Ensure we're on the base branch before creating
	current, err := m.CurrentBranch(dir)
	if err != nil {
		return err
	}
	if current != baseBranch {
		if err := m.CheckoutBranch(dir, baseBranch); err != nil {
			return fmt.Errorf("switching to base branch %s: %w", baseBranch, err)
		}
	}

	return m.CreateAndCheckoutBranch(dir, workBranch)
}

// CancelCleanup reverts all uncommitted changes on the current branch and
// switches back to the base branch. Committed work on the work branch is
// preserved (the branch is not deleted). Per Architecture Section 23.4,
// the user reviews and merges at their discretion.
func (m *Manager) CancelCleanup(dir, baseBranch string) error {
	// Discard all staged and unstaged changes
	if _, err := m.git(dir, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("resetting working tree: %w", err)
	}

	// Remove untracked files and directories
	if _, err := m.git(dir, "clean", "-fd"); err != nil {
		return fmt.Errorf("cleaning untracked files: %w", err)
	}

	// Switch back to base branch
	if err := m.CheckoutBranch(dir, baseBranch); err != nil {
		return fmt.Errorf("returning to base branch %s: %w", baseBranch, err)
	}

	m.log.Info("cancel cleanup complete", "base_branch", baseBranch)
	return nil
}

// ChangedFilesSince returns the list of file paths that have changed
// between the given ref (commit SHA) and the current HEAD.
// Used by the merge queue to detect actual conflicts for stale snapshots
// (Architecture Section 16.2).
func (m *Manager) ChangedFilesSince(dir, sinceRef string) ([]string, error) {
	out, err := m.git(dir, "diff", "--name-only", sinceRef, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("diffing %s..HEAD: %w", sinceRef, err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// git runs a git command in the given directory and returns trimmed stdout.
func (m *Manager) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
