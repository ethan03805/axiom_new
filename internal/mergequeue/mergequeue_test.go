package mergequeue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

)

// --- Mock implementations ---

type mockGit struct {
	mu            sync.Mutex
	head          string
	dirty         bool
	committed     []commitRecord
	addedFiles    [][]string
	failCommit    bool
	failHead      bool
	changedFiles  []string // files returned by ChangedFilesSince
}

type commitRecord struct {
	message string
	sha     string
}

func (m *mockGit) CurrentHEAD(_ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failHead {
		return "", fmt.Errorf("git HEAD error")
	}
	return m.head, nil
}

func (m *mockGit) AddFiles(_ string, files []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedFiles = append(m.addedFiles, files)
	return nil
}

func (m *mockGit) Commit(_ string, message string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCommit {
		return "", fmt.Errorf("commit failed")
	}
	sha := fmt.Sprintf("commit-%d", len(m.committed)+1)
	m.committed = append(m.committed, commitRecord{message: message, sha: sha})
	m.head = sha
	return sha, nil
}

func (m *mockGit) ChangedFilesSince(_ string, _ string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changedFiles, nil
}

type mockValidator struct {
	pass       bool
	output     string
	failStart  bool
}

func (m *mockValidator) RunIntegrationChecks(_ context.Context, projectDir string) (bool, string, error) {
	if m.failStart {
		return false, "", fmt.Errorf("validator unavailable")
	}
	return m.pass, m.output, nil
}

type mockIndexer struct {
	mu          sync.Mutex
	indexedDirs []string
	indexedFiles [][]string
	failIndex   bool
}

func (m *mockIndexer) IndexFiles(_ context.Context, dir string, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failIndex {
		return fmt.Errorf("indexing failed")
	}
	m.indexedDirs = append(m.indexedDirs, dir)
	m.indexedFiles = append(m.indexedFiles, paths)
	return nil
}

type mockLockReleaser struct {
	mu       sync.Mutex
	released []string
}

func (m *mockLockReleaser) ReleaseLocks(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = append(m.released, taskID)
	return nil
}

type mockTaskCompleter struct {
	mu        sync.Mutex
	completed []string
	failed    []failRecord
}

type failRecord struct {
	taskID   string
	feedback string
}

func (m *mockTaskCompleter) CompleteTask(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = append(m.completed, taskID)
	return nil
}

func (m *mockTaskCompleter) RequeueTask(_ context.Context, taskID string, feedback string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = append(m.failed, failRecord{taskID: taskID, feedback: feedback})
	return nil
}

type eventRecord struct {
	eventType string
	taskID    string
	details   map[string]any
}

type mockEventEmitter struct {
	mu     sync.Mutex
	events []eventRecord
}

func (m *mockEventEmitter) Emit(eventType string, taskID string, details map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, eventRecord{
		eventType: eventType,
		taskID:    taskID,
		details:   details,
	})
}

// --- Helper ---

func newTestQueue(opts ...func(*testSetup)) *testSetup {
	ts := &testSetup{
		git:       &mockGit{head: "abc123"},
		validator: &mockValidator{pass: true},
		indexer:   &mockIndexer{},
		locks:     &mockLockReleaser{},
		tasks:     &mockTaskCompleter{},
		emitter:   &mockEventEmitter{},
	}
	for _, o := range opts {
		o(ts)
	}

	ts.queue = New(Options{
		ProjectDir: ts.projectDir,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Git:        ts.git,
		Validator:  ts.validator,
		Indexer:    ts.indexer,
		Locks:      ts.locks,
		Tasks:      ts.tasks,
		Events:     ts.emitter,
	})
	return ts
}

type testSetup struct {
	queue      *Queue
	projectDir string
	git        *mockGit
	validator  *mockValidator
	indexer    *mockIndexer
	locks      *mockLockReleaser
	tasks      *mockTaskCompleter
	emitter    *mockEventEmitter
}

func makeStagingDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func makeProjectDir(t *testing.T, files map[string]string) string {
	t.Helper()
	return makeStagingDir(t, files) // same helper, just for naming clarity
}

func baseMergeItem(stagingDir string) MergeItem {
	return MergeItem{
		TaskID:       "task-001",
		RunID:        "run-001",
		AttemptID:    1,
		BaseSnapshot: "abc123",
		StagingDir:   stagingDir,
		CommitInfo: CommitInfo{
			TaskTitle:     "Implement auth handler",
			TaskID:        "task-001",
			SRSRefs:       []string{"FR-001"},
			MeeseeksModel: "anthropic/claude-4-sonnet",
			ReviewerModel: "openai/gpt-4o",
			AttemptNumber: 1,
			MaxAttempts:   3,
			CostUSD:       0.0234,
			BaseSnapshot:  "abc123",
		},
		OutputFiles: []string{"src/auth.go"},
		DeleteFiles: nil,
		RenameFiles: nil,
	}
}

// --- Tests ---

func TestQueue_EmptyQueueTickNoOp(t *testing.T) {
	ts := newTestQueue()

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick on empty queue: %v", err)
	}

	if len(ts.git.committed) != 0 {
		t.Error("expected no commits on empty queue")
	}
	if len(ts.tasks.completed) != 0 {
		t.Error("expected no completed tasks on empty queue")
	}
}

func TestQueue_Len(t *testing.T) {
	ts := newTestQueue()

	if ts.queue.Len() != 0 {
		t.Errorf("Len = %d, want 0", ts.queue.Len())
	}

	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})
	ts.queue.Enqueue(baseMergeItem(staging))

	if ts.queue.Len() != 1 {
		t.Errorf("Len = %d, want 1", ts.queue.Len())
	}
}

func TestQueue_CleanMerge_Success(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item := baseMergeItem(staging)
	ts.queue.Enqueue(item)

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Queue should be drained
	if ts.queue.Len() != 0 {
		t.Errorf("Len = %d, want 0 after processing", ts.queue.Len())
	}

	// Task should be marked completed
	if len(ts.tasks.completed) != 1 || ts.tasks.completed[0] != "task-001" {
		t.Errorf("completed = %v, want [task-001]", ts.tasks.completed)
	}

	// Locks should be released
	if len(ts.locks.released) != 1 || ts.locks.released[0] != "task-001" {
		t.Errorf("released = %v, want [task-001]", ts.locks.released)
	}

	// Files should be staged and committed
	if len(ts.git.committed) != 1 {
		t.Fatalf("committed count = %d, want 1", len(ts.git.committed))
	}

	// Written file should exist in project dir
	content, err := os.ReadFile(filepath.Join(projectDir, "src", "auth.go"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(content) != "package auth" {
		t.Errorf("file content = %q, want %q", string(content), "package auth")
	}

	// Indexer should have been called with affected files
	if len(ts.indexer.indexedFiles) != 1 {
		t.Fatalf("indexedFiles count = %d, want 1", len(ts.indexer.indexedFiles))
	}
	if len(ts.indexer.indexedFiles[0]) != 1 || ts.indexer.indexedFiles[0][0] != "src/auth.go" {
		t.Errorf("indexed files = %v, want [src/auth.go]", ts.indexer.indexedFiles[0])
	}
}

func TestQueue_CleanMerge_CommitMessage(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	ts.queue.Enqueue(baseMergeItem(staging))
	ts.queue.Tick(context.Background())

	if len(ts.git.committed) != 1 {
		t.Fatalf("committed count = %d, want 1", len(ts.git.committed))
	}

	msg := ts.git.committed[0].message
	// Per Architecture Section 23.2
	if !containsAll(msg, "[axiom]", "Implement auth handler", "Task: task-001",
		"SRS Refs: FR-001", "Meeseeks Model: anthropic/claude-4-sonnet",
		"Reviewer Model: openai/gpt-4o", "Attempt: 1/3", "Base Snapshot: abc123") {
		t.Errorf("commit message missing required fields:\n%s", msg)
	}
}

func TestQueue_StaleSnapshot_CleanApply(t *testing.T) {
	// base_snapshot differs from current HEAD, but no conflicts
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/new.go": "package new"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.git.head = "def456" // HEAD has advanced past base_snapshot
	})

	item := baseMergeItem(staging)
	item.BaseSnapshot = "abc123" // stale
	item.OutputFiles = []string{"src/new.go"}
	ts.queue.Enqueue(item)

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Should still succeed — new file, no conflict
	if len(ts.tasks.completed) != 1 {
		t.Errorf("completed = %v, want [task-001]", ts.tasks.completed)
	}
}

func TestQueue_StaleSnapshot_ConflictRequeues(t *testing.T) {
	// base_snapshot differs from HEAD and output modifies a file that changed
	projectDir := makeProjectDir(t, map[string]string{
		"src/auth.go": "package auth // v2 from HEAD",
	})
	staging := makeStagingDir(t, map[string]string{
		"src/auth.go": "package auth // from meeseeks",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.git.head = "def456" // HEAD advanced
		s.git.changedFiles = []string{"src/auth.go"} // file actually changed since base
	})

	item := baseMergeItem(staging)
	item.BaseSnapshot = "abc123" // stale
	item.OutputFiles = []string{"src/auth.go"}
	ts.queue.Enqueue(item)

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Task should be requeued, not completed
	if len(ts.tasks.completed) != 0 {
		t.Errorf("expected no completed tasks, got %v", ts.tasks.completed)
	}
	if len(ts.tasks.failed) != 1 {
		t.Fatalf("expected 1 requeued task, got %d", len(ts.tasks.failed))
	}
	if ts.tasks.failed[0].taskID != "task-001" {
		t.Errorf("requeued task = %q, want task-001", ts.tasks.failed[0].taskID)
	}
	if ts.tasks.failed[0].feedback == "" {
		t.Error("expected feedback on requeue, got empty")
	}

	// Locks should still be released on requeue
	if len(ts.locks.released) != 1 {
		t.Errorf("released = %v, want [task-001]", ts.locks.released)
	}
}

func TestQueue_IntegrationCheckFailure_Requeues(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth // broken"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.validator.pass = false
		s.validator.output = "FAIL: TestAuth — compile error line 42"
	})

	item := baseMergeItem(staging)
	ts.queue.Enqueue(item)

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Task should be requeued with integration failure feedback
	if len(ts.tasks.completed) != 0 {
		t.Errorf("expected no completed tasks, got %v", ts.tasks.completed)
	}
	if len(ts.tasks.failed) != 1 {
		t.Fatalf("expected 1 requeued task, got %d", len(ts.tasks.failed))
	}
	if ts.tasks.failed[0].feedback == "" {
		t.Error("expected feedback containing integration failure details")
	}

	// No commit should have been made
	if len(ts.git.committed) != 0 {
		t.Errorf("expected no commits on integration failure, got %d", len(ts.git.committed))
	}

	// Locks released
	if len(ts.locks.released) != 1 {
		t.Errorf("released = %v, want [task-001]", ts.locks.released)
	}

	// File should NOT remain in project (reverted)
	if _, err := os.Stat(filepath.Join(projectDir, "src", "auth.go")); err == nil {
		t.Error("expected written file to be cleaned up after integration failure")
	}
}

func TestQueue_DeleteFiles(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{
		"go.mod":          "module example",
		"src/old_auth.go": "package auth // old",
		"src/new_auth.go": "package auth // new",
	})
	staging := makeStagingDir(t, map[string]string{
		"src/new_auth.go": "package auth // updated",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item := baseMergeItem(staging)
	item.OutputFiles = []string{"src/new_auth.go"}
	item.DeleteFiles = []string{"src/old_auth.go"}
	ts.queue.Enqueue(item)

	ts.queue.Tick(context.Background())

	if len(ts.tasks.completed) != 1 {
		t.Fatalf("expected 1 completed task, got %d", len(ts.tasks.completed))
	}

	// Deleted file should not exist
	if _, err := os.Stat(filepath.Join(projectDir, "src", "old_auth.go")); err == nil {
		t.Error("expected deleted file to be removed")
	}

	// New file should exist with updated content
	content, err := os.ReadFile(filepath.Join(projectDir, "src", "new_auth.go"))
	if err != nil {
		t.Fatalf("reading updated file: %v", err)
	}
	if string(content) != "package auth // updated" {
		t.Errorf("content = %q, want %q", string(content), "package auth // updated")
	}
}

func TestQueue_RenameFiles(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{
		"go.mod":          "module example",
		"src/utils/hash.go": "package utils",
	})
	staging := makeStagingDir(t, map[string]string{
		"src/crypto/hash.go": "package crypto",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item := baseMergeItem(staging)
	item.OutputFiles = []string{"src/crypto/hash.go"}
	item.DeleteFiles = nil
	item.RenameFiles = []RenameOp{
		{From: "src/utils/hash.go", To: "src/crypto/hash.go"},
	}
	ts.queue.Enqueue(item)

	ts.queue.Tick(context.Background())

	if len(ts.tasks.completed) != 1 {
		t.Fatalf("expected 1 completed task, got %d", len(ts.tasks.completed))
	}

	// Old path should be deleted
	if _, err := os.Stat(filepath.Join(projectDir, "src", "utils", "hash.go")); err == nil {
		t.Error("expected old rename path to be removed")
	}

	// New path should exist with new content
	content, err := os.ReadFile(filepath.Join(projectDir, "src", "crypto", "hash.go"))
	if err != nil {
		t.Fatalf("reading renamed file: %v", err)
	}
	if string(content) != "package crypto" {
		t.Errorf("content = %q, want %q", string(content), "package crypto")
	}
}

func TestQueue_Serialization_OneAtATime(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging1 := makeStagingDir(t, map[string]string{"file1.go": "package one"})
	staging2 := makeStagingDir(t, map[string]string{"file2.go": "package two"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item1 := baseMergeItem(staging1)
	item1.TaskID = "task-001"
	item1.OutputFiles = []string{"file1.go"}
	item1.CommitInfo.TaskID = "task-001"

	item2 := baseMergeItem(staging2)
	item2.TaskID = "task-002"
	item2.OutputFiles = []string{"file2.go"}
	item2.CommitInfo.TaskID = "task-002"

	ts.queue.Enqueue(item1)
	ts.queue.Enqueue(item2)

	// First tick processes only one item
	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	if ts.queue.Len() != 1 {
		t.Errorf("after first tick: Len = %d, want 1", ts.queue.Len())
	}
	if len(ts.git.committed) != 1 {
		t.Errorf("after first tick: commits = %d, want 1", len(ts.git.committed))
	}

	// Second tick processes the other
	err = ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	if ts.queue.Len() != 0 {
		t.Errorf("after second tick: Len = %d, want 0", ts.queue.Len())
	}
	if len(ts.git.committed) != 2 {
		t.Errorf("after second tick: commits = %d, want 2", len(ts.git.committed))
	}
}

func TestQueue_Events_MergeQueued(t *testing.T) {
	ts := newTestQueue()

	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})
	ts.queue.Enqueue(baseMergeItem(staging))

	found := false
	for _, e := range ts.emitter.events {
		if e.eventType == "merge_queued" && e.taskID == "task-001" {
			found = true
		}
	}
	if !found {
		t.Error("expected merge_queued event on enqueue")
	}
}

func TestQueue_Events_MergeSucceeded(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})
	ts.queue.Enqueue(baseMergeItem(staging))
	ts.queue.Tick(context.Background())

	found := false
	for _, e := range ts.emitter.events {
		if e.eventType == "merge_succeeded" && e.taskID == "task-001" {
			found = true
		}
	}
	if !found {
		t.Error("expected merge_succeeded event after successful merge")
	}
}

func TestQueue_Events_MergeFailed(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.validator.pass = false
		s.validator.output = "compile error"
	})
	ts.queue.Enqueue(baseMergeItem(staging))
	ts.queue.Tick(context.Background())

	found := false
	for _, e := range ts.emitter.events {
		if e.eventType == "merge_failed" && e.taskID == "task-001" {
			found = true
		}
	}
	if !found {
		t.Error("expected merge_failed event after integration failure")
	}
}

func TestQueue_CommitFailure_Requeues(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.git.failCommit = true
	})
	ts.queue.Enqueue(baseMergeItem(staging))

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Should requeue
	if len(ts.tasks.failed) != 1 {
		t.Errorf("expected 1 requeued task, got %d", len(ts.tasks.failed))
	}
	if len(ts.tasks.completed) != 0 {
		t.Error("expected no completed tasks")
	}
}

func TestQueue_IndexerFailure_NonFatal(t *testing.T) {
	// Indexer failure should NOT block the commit or task completion.
	// The commit is the critical path; indexing can be retried.
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.indexer.failIndex = true
	})
	ts.queue.Enqueue(baseMergeItem(staging))

	err := ts.queue.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Task should still be completed despite indexer failure
	if len(ts.tasks.completed) != 1 {
		t.Errorf("completed = %v, want [task-001]", ts.tasks.completed)
	}
}

func TestQueue_AllAffectedFilesIndexed(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{
		"go.mod":          "module example",
		"src/old.go":      "package old",
		"src/utils/hash.go": "package utils",
	})
	staging := makeStagingDir(t, map[string]string{
		"src/new.go":         "package new",
		"src/crypto/hash.go": "package crypto",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item := baseMergeItem(staging)
	item.OutputFiles = []string{"src/new.go", "src/crypto/hash.go"}
	item.DeleteFiles = []string{"src/old.go"}
	item.RenameFiles = []RenameOp{
		{From: "src/utils/hash.go", To: "src/crypto/hash.go"},
	}
	ts.queue.Enqueue(item)
	ts.queue.Tick(context.Background())

	if len(ts.indexer.indexedFiles) != 1 {
		t.Fatalf("expected 1 index call, got %d", len(ts.indexer.indexedFiles))
	}

	// All affected paths should be indexed
	indexed := make(map[string]bool)
	for _, p := range ts.indexer.indexedFiles[0] {
		indexed[p] = true
	}

	expected := []string{"src/new.go", "src/crypto/hash.go", "src/old.go", "src/utils/hash.go"}
	for _, e := range expected {
		if !indexed[e] {
			t.Errorf("expected %q in indexed files, got %v", e, ts.indexer.indexedFiles[0])
		}
	}
}

func TestQueue_GitFilesForCommit(t *testing.T) {
	// Verify that the correct files are staged with git add
	projectDir := makeProjectDir(t, map[string]string{
		"go.mod":          "module example",
		"src/old.go":      "package old",
	})
	staging := makeStagingDir(t, map[string]string{
		"src/new.go": "package new",
		"src/mod.go": "package mod",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	item := baseMergeItem(staging)
	item.OutputFiles = []string{"src/new.go", "src/mod.go"}
	item.DeleteFiles = []string{"src/old.go"}
	ts.queue.Enqueue(item)
	ts.queue.Tick(context.Background())

	if len(ts.git.addedFiles) != 1 {
		t.Fatalf("expected 1 AddFiles call, got %d", len(ts.git.addedFiles))
	}

	// All output files AND deleted files should be staged
	staged := make(map[string]bool)
	for _, f := range ts.git.addedFiles[0] {
		staged[f] = true
	}
	for _, f := range []string{"src/new.go", "src/mod.go", "src/old.go"} {
		if !staged[f] {
			t.Errorf("expected %q in staged files, got %v", f, ts.git.addedFiles[0])
		}
	}
}

func TestQueue_IntegrationCheckRevertsFiles(t *testing.T) {
	// When integration checks fail, files written to project should be reverted
	originalContent := "package auth // original"
	projectDir := makeProjectDir(t, map[string]string{
		"go.mod":     "module example",
		"src/auth.go": originalContent,
	})
	staging := makeStagingDir(t, map[string]string{
		"src/auth.go": "package auth // broken from meeseeks",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.validator.pass = false
		s.validator.output = "test failure"
	})

	item := baseMergeItem(staging)
	ts.queue.Enqueue(item)
	ts.queue.Tick(context.Background())

	// Original file should be restored
	content, err := os.ReadFile(filepath.Join(projectDir, "src", "auth.go"))
	if err != nil {
		t.Fatalf("reading file after revert: %v", err)
	}
	if string(content) != originalContent {
		t.Errorf("file content after revert = %q, want %q", string(content), originalContent)
	}
}

func TestQueue_NewFileDeletedOnRevert(t *testing.T) {
	// A newly added file should be deleted on revert
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{
		"src/new.go": "package new",
	})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
		s.validator.pass = false
		s.validator.output = "lint failure"
	})

	item := baseMergeItem(staging)
	item.OutputFiles = []string{"src/new.go"}
	ts.queue.Enqueue(item)
	ts.queue.Tick(context.Background())

	// Newly added file should not exist after revert
	if _, err := os.Stat(filepath.Join(projectDir, "src", "new.go")); err == nil {
		t.Error("expected newly added file to be removed after revert")
	}
}

func TestQueue_ContextCancellation(t *testing.T) {
	projectDir := makeProjectDir(t, map[string]string{"go.mod": "module example"})
	staging := makeStagingDir(t, map[string]string{"src/auth.go": "package auth"})

	ts := newTestQueue(func(s *testSetup) {
		s.projectDir = projectDir
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ts.queue.Enqueue(baseMergeItem(staging))
	err := ts.queue.Tick(ctx)

	// Should return early without processing
	if err != nil && err != context.Canceled {
		t.Fatalf("expected nil or context.Canceled, got %v", err)
	}
}

// --- helpers ---

func containsAll(s string, substrings ...string) bool {
	for _, sub := range substrings {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
