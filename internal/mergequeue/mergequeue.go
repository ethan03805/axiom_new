// Package mergequeue implements the serialized merge queue per Architecture
// Section 16.4. It processes one approved output at a time, validates
// against the current HEAD, runs integration checks, and commits or
// requeues on failure.
//
// The merge queue ensures every commit is validated against the actual
// current state of the project, not a stale snapshot.
package mergequeue

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// --- Interfaces ---

// GitOps abstracts the git operations the merge queue needs.
type GitOps interface {
	CurrentHEAD(dir string) (string, error)
	AddFiles(dir string, files []string) error
	Commit(dir string, message string) (string, error)
	ChangedFilesSince(dir, sinceRef string) ([]string, error)
}

// Validator runs project-wide integration checks (build, test, lint)
// in a validation sandbox against the merged state.
// Per Architecture Section 23.3.
type Validator interface {
	RunIntegrationChecks(ctx context.Context, projectDir string) (pass bool, output string, err error)
}

// Indexer incrementally re-indexes changed files after a commit.
// Per Architecture Section 17.4.
type Indexer interface {
	IndexFiles(ctx context.Context, dir string, paths []string) error
}

// LockReleaser releases write-set locks after merge or failure.
// Per Architecture Section 16.3.
type LockReleaser interface {
	ReleaseLocks(ctx context.Context, taskID string) error
}

// TaskCompleter handles task lifecycle after merge processing.
type TaskCompleter interface {
	CompleteTask(ctx context.Context, taskID string) error
	RequeueTask(ctx context.Context, taskID string, feedback string) error
}

// EventEmitter publishes engine events for the merge queue.
type EventEmitter interface {
	Emit(eventType string, taskID string, details map[string]any)
}

// --- Types ---

// CommitInfo holds the metadata for an architecture-compliant commit message.
// Per Architecture Section 23.2.
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

// RenameOp describes a file rename operation.
type RenameOp struct {
	From string
	To   string
}

// MergeItem represents a task output approved by the review pipeline
// and ready to be merged into the project.
type MergeItem struct {
	TaskID       string
	RunID        string
	AttemptID    int64
	BaseSnapshot string
	StagingDir   string
	CommitInfo   CommitInfo
	OutputFiles  []string   // files to copy from staging to project
	DeleteFiles  []string   // files to delete from project
	RenameFiles  []RenameOp // file renames (from is deleted, to is in OutputFiles)
}

// MergeResult describes the outcome of a merge attempt.
type MergeResult struct {
	Success   bool
	CommitSHA string
	Requeued  bool
	Feedback  string
}

// --- Queue ---

// Options configures a new Queue.
type Options struct {
	ProjectDir string
	Log        *slog.Logger
	Git        GitOps
	Validator  Validator
	Indexer    Indexer
	Locks      LockReleaser
	Tasks      TaskCompleter
	Events     EventEmitter
}

// Queue is the serialized merge queue per Architecture Section 16.4.
// It processes one approved output at a time.
type Queue struct {
	projectDir string
	log        *slog.Logger
	git        GitOps
	validator  Validator
	indexer    Indexer
	locks      LockReleaser
	tasks      TaskCompleter
	events     EventEmitter

	mu    sync.Mutex
	items []MergeItem
}

// New creates a new merge queue.
func New(opts Options) *Queue {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Queue{
		projectDir: opts.ProjectDir,
		log:        log,
		git:        opts.Git,
		validator:  opts.Validator,
		indexer:    opts.Indexer,
		locks:      opts.Locks,
		tasks:      opts.Tasks,
		events:     opts.Events,
	}
}

// Enqueue adds an approved output to the merge queue.
// Per Architecture Section 16.4 step 1: receive approved output.
func (q *Queue) Enqueue(item MergeItem) {
	q.mu.Lock()
	q.items = append(q.items, item)
	q.mu.Unlock()

	q.events.Emit("merge_queued", item.TaskID, map[string]any{
		"run_id":    item.RunID,
		"attempt":   item.AttemptID,
		"queue_pos": q.Len(),
	})

	q.log.Info("merge item enqueued",
		"task_id", item.TaskID,
		"queue_len", q.Len(),
	)
}

// Len returns the number of items in the queue.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Tick processes the next item in the queue, if any.
// Only one item is processed per tick to ensure serialization.
// Per Architecture Section 16.4: "processes one approved output at a time."
func (q *Queue) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	item, ok := q.dequeue()
	if !ok {
		return nil // empty queue
	}

	return q.processItem(ctx, item)
}

// dequeue removes and returns the next item from the queue.
func (q *Queue) dequeue() (MergeItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return MergeItem{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// processItem handles the full merge workflow for a single item.
// Architecture Section 16.4 steps 2–10.
func (q *Queue) processItem(ctx context.Context, item MergeItem) error {
	q.log.Info("processing merge item",
		"task_id", item.TaskID,
		"base_snapshot", item.BaseSnapshot,
	)

	// Step 2: Validate base_snapshot against current HEAD
	head, err := q.git.CurrentHEAD(q.projectDir)
	if err != nil {
		return q.failItem(ctx, item, fmt.Sprintf("failed to get current HEAD: %v", err))
	}

	isStale := head != item.BaseSnapshot

	// Step 3: If stale, check for conflicts
	if isStale {
		hasConflict, err := q.checkConflicts(item)
		if err != nil {
			return q.failItem(ctx, item, fmt.Sprintf("conflict check error: %v", err))
		}
		if hasConflict {
			q.log.Info("stale snapshot with conflicts, requeuing",
				"task_id", item.TaskID,
				"base_snapshot", item.BaseSnapshot,
				"current_head", head,
			)
			return q.failItem(ctx, item, fmt.Sprintf(
				"merge conflict: base_snapshot %s is stale (HEAD is %s) and output modifies files that have changed",
				item.BaseSnapshot, head,
			))
		}
		q.log.Info("stale snapshot but no conflicts, proceeding",
			"task_id", item.TaskID,
		)
	}

	// Step 4: Apply Meeseeks output to project
	backup, err := q.applyOutput(item)
	if err != nil {
		return q.failItem(ctx, item, fmt.Sprintf("failed to apply output: %v", err))
	}

	// Step 5: Run integration checks
	pass, output, err := q.validator.RunIntegrationChecks(ctx, q.projectDir)
	if err != nil {
		q.revertOutput(item, backup)
		return q.failItem(ctx, item, fmt.Sprintf("integration check error: %v", err))
	}

	// Step 6: If integration fails, revert and requeue
	if !pass {
		q.revertOutput(item, backup)
		q.log.Info("integration checks failed, requeuing",
			"task_id", item.TaskID,
			"output", output,
		)
		return q.failItem(ctx, item, fmt.Sprintf("integration checks failed:\n%s", output))
	}

	// Step 7: Commit
	msg := formatCommitMessage(item.CommitInfo)
	allFiles := q.allGitFiles(item)

	if err := q.git.AddFiles(q.projectDir, allFiles); err != nil {
		q.revertOutput(item, backup)
		return q.failItem(ctx, item, fmt.Sprintf("failed to stage files: %v", err))
	}

	commitSHA, err := q.git.Commit(q.projectDir, msg)
	if err != nil {
		q.revertOutput(item, backup)
		return q.failItem(ctx, item, fmt.Sprintf("commit failed: %v", err))
	}

	q.log.Info("merge committed",
		"task_id", item.TaskID,
		"sha", commitSHA,
	)

	// Step 8: Re-index changed files (non-fatal)
	affectedPaths := q.allAffectedPaths(item)
	if err := q.indexer.IndexFiles(ctx, q.projectDir, affectedPaths); err != nil {
		q.log.Warn("post-merge indexing failed (non-fatal)",
			"task_id", item.TaskID,
			"error", err,
		)
	}

	// Step 9: Release write-set locks
	if err := q.locks.ReleaseLocks(ctx, item.TaskID); err != nil {
		q.log.Warn("failed to release locks (non-fatal)",
			"task_id", item.TaskID,
			"error", err,
		)
	}

	// Step 10: Mark task done and unblock dependents
	if err := q.tasks.CompleteTask(ctx, item.TaskID); err != nil {
		q.log.Error("failed to complete task",
			"task_id", item.TaskID,
			"error", err,
		)
	}

	q.events.Emit("merge_succeeded", item.TaskID, map[string]any{
		"commit_sha": commitSHA,
		"run_id":     item.RunID,
	})

	return nil
}

// failItem requeues a task with feedback and releases locks.
func (q *Queue) failItem(ctx context.Context, item MergeItem, feedback string) error {
	q.events.Emit("merge_failed", item.TaskID, map[string]any{
		"run_id":   item.RunID,
		"feedback": feedback,
	})

	// Release locks even on failure
	if err := q.locks.ReleaseLocks(ctx, item.TaskID); err != nil {
		q.log.Warn("failed to release locks after merge failure",
			"task_id", item.TaskID,
			"error", err,
		)
	}

	if err := q.tasks.RequeueTask(ctx, item.TaskID, feedback); err != nil {
		q.log.Error("failed to requeue task after merge failure",
			"task_id", item.TaskID,
			"error", err,
		)
		return fmt.Errorf("requeuing task %s: %w", item.TaskID, err)
	}

	return nil
}

// checkConflicts detects whether the output modifies files that have
// actually changed since the base_snapshot. Uses git to determine which
// files changed between the base_snapshot and current HEAD, then checks
// for overlap with the merge item's output files.
// Per Architecture Section 16.2: only actual conflicts trigger a requeue.
func (q *Queue) checkConflicts(item MergeItem) (bool, error) {
	// Ask git which files changed since the base_snapshot
	changedFiles, err := q.git.ChangedFilesSince(q.projectDir, item.BaseSnapshot)
	if err != nil {
		// If we can't determine changes (e.g., bad ref), fall back to
		// conservative conflict detection based on file existence.
		q.log.Warn("cannot diff since base_snapshot, using conservative check",
			"task_id", item.TaskID,
			"error", err,
		)
		return q.checkConflictsConservative(item), nil
	}

	changedSet := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changedSet[filepath.ToSlash(f)] = true
	}

	// Check if any output file overlaps with files changed since base_snapshot
	for _, f := range item.OutputFiles {
		if changedSet[filepath.ToSlash(f)] {
			return true, nil
		}
	}

	return false, nil
}

// checkConflictsConservative is a fallback when git diff is unavailable.
// It conservatively treats any existing file modification as a conflict.
func (q *Queue) checkConflictsConservative(item MergeItem) bool {
	for _, f := range item.OutputFiles {
		projectPath := filepath.Join(q.projectDir, filepath.FromSlash(f))
		if _, err := os.Stat(projectPath); err == nil {
			return true
		}
	}
	return false
}

// fileBackup tracks the original state of files for revert.
type fileBackup struct {
	// existing maps file paths to their original content (nil = did not exist).
	existing map[string][]byte
}

// applyOutput writes Meeseeks output files to the project directory,
// handles deletes and renames, and returns a backup for revert.
func (q *Queue) applyOutput(item MergeItem) (*fileBackup, error) {
	backup := &fileBackup{
		existing: make(map[string][]byte),
	}

	// Handle renames: delete the "from" path
	for _, r := range item.RenameFiles {
		fromPath := filepath.Join(q.projectDir, filepath.FromSlash(r.From))
		if content, err := os.ReadFile(fromPath); err == nil {
			backup.existing[r.From] = content
		}
		os.Remove(fromPath)
	}

	// Handle deletes
	for _, d := range item.DeleteFiles {
		delPath := filepath.Join(q.projectDir, filepath.FromSlash(d))
		if content, err := os.ReadFile(delPath); err == nil {
			backup.existing[d] = content
		}
		os.Remove(delPath)
	}

	// Copy output files from staging to project
	for _, f := range item.OutputFiles {
		srcPath := filepath.Join(item.StagingDir, filepath.FromSlash(f))
		dstPath := filepath.Join(q.projectDir, filepath.FromSlash(f))

		// Backup existing file content
		if content, err := os.ReadFile(dstPath); err == nil {
			backup.existing[f] = content
		} else {
			// Mark as new file (nil content = did not exist)
			backup.existing[f] = nil
		}

		// Ensure parent directory exists
		dir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return backup, fmt.Errorf("creating directory %s: %w", dir, err)
		}

		// Copy file
		if err := copyFile(srcPath, dstPath); err != nil {
			return backup, fmt.Errorf("copying %s: %w", f, err)
		}
	}

	return backup, nil
}

// revertOutput restores files to their pre-apply state using the backup.
func (q *Queue) revertOutput(_ MergeItem, backup *fileBackup) {
	if backup == nil {
		return
	}

	for path, content := range backup.existing {
		fullPath := filepath.Join(q.projectDir, filepath.FromSlash(path))
		if content == nil {
			// File did not exist before — remove it
			os.Remove(fullPath)
		} else {
			// Restore original content
			os.MkdirAll(filepath.Dir(fullPath), 0o755)
			os.WriteFile(fullPath, content, 0o644)
		}
	}
}

// allGitFiles returns all paths that need to be staged with git add.
// This includes output files (added/modified) and deleted files.
func (q *Queue) allGitFiles(item MergeItem) []string {
	seen := make(map[string]bool)
	var files []string

	for _, f := range item.OutputFiles {
		if !seen[f] {
			files = append(files, f)
			seen[f] = true
		}
	}
	for _, f := range item.DeleteFiles {
		if !seen[f] {
			files = append(files, f)
			seen[f] = true
		}
	}
	// Rename "from" paths need to be staged as deletions
	for _, r := range item.RenameFiles {
		if !seen[r.From] {
			files = append(files, r.From)
			seen[r.From] = true
		}
	}

	return files
}

// allAffectedPaths returns every path touched by the merge for re-indexing.
// Per Architecture Section 17.4: incremental index of changed files.
func (q *Queue) allAffectedPaths(item MergeItem) []string {
	seen := make(map[string]bool)
	var paths []string

	add := func(p string) {
		if !seen[p] {
			paths = append(paths, p)
			seen[p] = true
		}
	}

	for _, f := range item.OutputFiles {
		add(f)
	}
	for _, f := range item.DeleteFiles {
		add(f)
	}
	for _, r := range item.RenameFiles {
		add(r.From)
		add(r.To)
	}

	return paths
}

// formatCommitMessage builds the commit message per Architecture Section 23.2.
func formatCommitMessage(info CommitInfo) string {
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

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
