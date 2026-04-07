package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/state"
)

type scriptedContainerService struct {
	t             *testing.T
	outputFiles   map[string]string
	deleteFiles   []string
	renameFiles   map[string]string
	started       []ContainerSpec
	stopped       []string
	cleanupCalled bool
	mu            sync.Mutex
}

func (s *scriptedContainerService) Start(_ context.Context, spec ContainerSpec) (string, error) {
	s.mu.Lock()
	s.started = append(s.started, spec)
	s.mu.Unlock()

	if spec.Env["AXIOM_CONTAINER_TYPE"] == string(state.ContainerMeeseeks) {
		taskID := spec.Env["AXIOM_TASK_ID"]
		stagingDir := mountHostPath(spec.Mounts, "/workspace/staging")
		ipcDir := mountHostPath(spec.Mounts, "/workspace/ipc")

		go func() {
			for relPath, content := range s.outputFiles {
				fullPath := filepath.Join(stagingDir, filepath.FromSlash(relPath))
				if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
					s.t.Errorf("mkdir output path: %v", err)
					return
				}
				if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
					s.t.Errorf("write staged output: %v", err)
					return
				}
			}

			manifestJSON := buildManifestJSON(taskID, spec.Env["AXIOM_ATTEMPT_ID"], s.outputFiles, s.deleteFiles, s.renameFiles)
			if err := os.WriteFile(filepath.Join(stagingDir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
				s.t.Errorf("write manifest: %v", err)
				return
			}

			env, err := ipc.NewEnvelope(ipc.MsgTaskOutput, taskID, map[string]string{"status": "completed"})
			if err != nil {
				s.t.Errorf("build task_output envelope: %v", err)
				return
			}
			if _, err := ipc.WriteMessage(filepath.Join(ipcDir, "output"), env); err != nil {
				s.t.Errorf("write task_output message: %v", err)
			}
		}()
	}

	return spec.Name, nil
}

func (s *scriptedContainerService) Stop(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, id)
	return nil
}

func (s *scriptedContainerService) ListRunning(_ context.Context) ([]string, error) { return nil, nil }

func (s *scriptedContainerService) Cleanup(_ context.Context) error {
	s.cleanupCalled = true
	return nil
}

type mockValidationService struct {
	results []ValidationCheckResult
	calls   int
}

func (m *mockValidationService) RunChecks(_ context.Context, _ ValidationCheckRequest) ([]ValidationCheckResult, error) {
	m.calls++
	return append([]ValidationCheckResult(nil), m.results...), nil
}

type mockReviewService struct {
	result *ReviewRunResult
	calls  int
}

func (m *mockReviewService) RunReview(_ context.Context, _ ReviewRunRequest) (*ReviewRunResult, error) {
	m.calls++
	return m.result, nil
}

type mockTaskService struct {
	db            *state.DB
	failureCalls  int
	scopeCalls    int
	failureAction TaskFailureAction
}

func (m *mockTaskService) HandleTaskFailure(_ context.Context, taskID string, _ string) (TaskFailureAction, error) {
	m.failureCalls++
	if err := m.db.UpdateTaskStatus(taskID, state.TaskQueued); err != nil {
		return "", err
	}
	return m.failureAction, nil
}

func (m *mockTaskService) RequestScopeExpansion(_ context.Context, _ string, _ []TargetFileSpec) error {
	m.scopeCalls++
	return nil
}

type trackingGitService struct {
	head    string
	commits []string
	added   [][]string
}

func (g *trackingGitService) CurrentBranch(string) (string, error) { return "main", nil }
func (g *trackingGitService) CreateBranch(string, string) error    { return nil }
func (g *trackingGitService) CurrentHEAD(string) (string, error) {
	if g.head == "" {
		g.head = "base-sha"
	}
	return g.head, nil
}
func (g *trackingGitService) IsDirty(string) (bool, error)                 { return false, nil }
func (g *trackingGitService) ValidateClean(string) error                   { return nil }
func (g *trackingGitService) SetupWorkBranch(string, string, string) error { return nil }
func (g *trackingGitService) AddFiles(_ string, files []string) error {
	g.added = append(g.added, append([]string(nil), files...))
	return nil
}
func (g *trackingGitService) Commit(_ string, message string) (string, error) {
	sha := fmt.Sprintf("commit-%d", len(g.commits)+1)
	g.head = sha
	g.commits = append(g.commits, message)
	return sha, nil
}
func (g *trackingGitService) ChangedFilesSince(string, string) ([]string, error) { return nil, nil }

func TestExecuteAttempt_SuccessEnqueuesAndMerges(t *testing.T) {
	engineUnderTest, containerSvc, validationSvc, reviewSvc, gitSvc := newExecutorEngine(t, executorEngineOptions{})
	taskRecord, attemptRecord := seedDispatchedAttempt(t, engineUnderTest, "run-success", "task-success", state.TaskInProgress)

	engineUnderTest.executeAttempt(context.Background(), *taskRecord, *attemptRecord)
	if got := engineUnderTest.MergeQueueLen(); got != 1 {
		t.Fatalf("merge queue len = %d, want 1", got)
	}
	if err := engineUnderTest.mergeQueueLoop(context.Background()); err != nil {
		t.Fatalf("mergeQueueLoop: %v", err)
	}

	taskAfter, err := engineUnderTest.db.GetTask(taskRecord.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if taskAfter.Status != state.TaskDone {
		t.Fatalf("task status = %q, want %q", taskAfter.Status, state.TaskDone)
	}

	attemptAfter, err := engineUnderTest.db.GetAttempt(attemptRecord.ID)
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if attemptAfter.Status != state.AttemptPassed {
		t.Fatalf("attempt status = %q, want %q", attemptAfter.Status, state.AttemptPassed)
	}
	if attemptAfter.Phase != state.PhaseSucceeded {
		t.Fatalf("attempt phase = %q, want %q", attemptAfter.Phase, state.PhaseSucceeded)
	}

	mergedPath := filepath.Join(engineUnderTest.rootDir, "hello.txt")
	data, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatalf("ReadFile(merged output): %v", err)
	}
	if string(data) != "hello from meeseeks\n" {
		t.Fatalf("merged file content = %q", string(data))
	}

	if validationSvc.calls != 2 {
		t.Fatalf("validation calls = %d, want 2 (stage2 + merge queue)", validationSvc.calls)
	}
	if reviewSvc.calls != 1 {
		t.Fatalf("review calls = %d, want 1", reviewSvc.calls)
	}
	if len(gitSvc.commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(gitSvc.commits))
	}
	if len(containerSvc.stopped) == 0 {
		t.Fatal("expected meeseeks container to be stopped")
	}

	if _, err := os.Stat(filepath.Join(engineUnderTest.rootDir, ".axiom", "containers", "staging", taskRecord.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected task staging dir to be cleaned, stat err = %v", err)
	}
}

func TestExecuteAttempt_ValidationFailureRequeuesTask(t *testing.T) {
	engineUnderTest, _, validationSvc, _, _ := newExecutorEngine(t, executorEngineOptions{
		validationResults: []ValidationCheckResult{{
			CheckType:  state.CheckCompile,
			Status:     state.ValidationFail,
			Output:     "compile failed",
			DurationMs: 10,
		}},
	})
	taskRecord, attemptRecord := seedDispatchedAttempt(t, engineUnderTest, "run-fail", "task-fail", state.TaskInProgress)

	engineUnderTest.executeAttempt(context.Background(), *taskRecord, *attemptRecord)

	taskAfter, err := engineUnderTest.db.GetTask(taskRecord.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if taskAfter.Status != state.TaskQueued {
		t.Fatalf("task status = %q, want %q", taskAfter.Status, state.TaskQueued)
	}

	attemptAfter, err := engineUnderTest.db.GetAttempt(attemptRecord.ID)
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if attemptAfter.Status != state.AttemptFailed {
		t.Fatalf("attempt status = %q, want %q", attemptAfter.Status, state.AttemptFailed)
	}
	if attemptAfter.Phase != state.PhaseFailed {
		t.Fatalf("attempt phase = %q, want %q", attemptAfter.Phase, state.PhaseFailed)
	}
	if attemptAfter.Feedback == nil || !strings.Contains(*attemptAfter.Feedback, "compile failed") {
		t.Fatalf("attempt feedback = %v, want compile failure details", attemptAfter.Feedback)
	}

	if validationSvc.calls != 1 {
		t.Fatalf("validation calls = %d, want 1", validationSvc.calls)
	}
	if _, err := os.Stat(filepath.Join(engineUnderTest.rootDir, ".axiom", "containers", "staging", taskRecord.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected failed attempt dirs to be cleaned, stat err = %v", err)
	}
}

func TestEngineWorkers_SchedulerExecutorMergeQueueFlow(t *testing.T) {
	engineUnderTest, _, _, _, _ := newExecutorEngine(t, executorEngineOptions{})
	runID := seedActiveRun(t, engineUnderTest, "worker-run")
	if err := engineUnderTest.db.CreateTask(&state.Task{
		ID:       "queued-task",
		RunID:    runID,
		Title:    "Create hello file",
		Status:   state.TaskQueued,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := engineUnderTest.db.AddTaskTargetFile(&state.TaskTargetFile{
		TaskID:          "queued-task",
		FilePath:        "hello.txt",
		LockScope:       "file",
		LockResourceKey: "hello.txt",
	}); err != nil {
		t.Fatalf("AddTaskTargetFile: %v", err)
	}

	if err := engineUnderTest.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engineUnderTest.Stop()

	waitForCondition(t, 5*time.Second, func() bool {
		taskAfter, err := engineUnderTest.db.GetTask("queued-task")
		return err == nil && taskAfter.Status == state.TaskDone
	})

	mergedPath := filepath.Join(engineUnderTest.rootDir, "hello.txt")
	if _, err := os.Stat(mergedPath); err != nil {
		t.Fatalf("expected merged output file: %v", err)
	}
}

type executorEngineOptions struct {
	validationResults []ValidationCheckResult
	reviewResult      *ReviewRunResult
}

func newExecutorEngine(t *testing.T, opts executorEngineOptions) (*Engine, *scriptedContainerService, *mockValidationService, *mockReviewService, *trackingGitService) {
	t.Helper()

	db := testDB(t)
	cfg := testConfig()
	root := t.TempDir()
	gitSvc := &trackingGitService{head: "base-sha"}
	containerSvc := &scriptedContainerService{
		t:           t,
		outputFiles: map[string]string{"hello.txt": "hello from meeseeks\n"},
	}
	validationResults := opts.validationResults
	if len(validationResults) == 0 {
		validationResults = []ValidationCheckResult{{
			CheckType:  state.CheckCompile,
			Status:     state.ValidationPass,
			Output:     "",
			DurationMs: 5,
		}}
	}
	validationSvc := &mockValidationService{results: validationResults}
	reviewResult := opts.reviewResult
	if reviewResult == nil {
		reviewResult = &ReviewRunResult{
			Verdict:        state.ReviewApprove,
			ReviewerModel:  "review/model",
			ReviewerFamily: "review",
			ReviewerTier:   state.TierStandard,
		}
	}
	reviewSvc := &mockReviewService{result: reviewResult}
	taskSvc := &mockTaskService{db: db, failureAction: TaskFailureRetry}

	engineUnderTest, err := New(Options{
		Config:     cfg,
		DB:         db,
		RootDir:    root,
		Log:        testLogger(),
		Git:        gitSvc,
		Container:  containerSvc,
		Validation: validationSvc,
		Review:     reviewSvc,
		Tasks:      taskSvc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { engineUnderTest.Stop() })
	return engineUnderTest, containerSvc, validationSvc, reviewSvc, gitSvc
}

func seedDispatchedAttempt(t *testing.T, e *Engine, runID, taskID string, taskStatus state.TaskStatus) (*state.Task, *state.TaskAttempt) {
	t.Helper()

	run := seedActiveRun(t, e, runID)
	taskRecord := &state.Task{
		ID:       taskID,
		RunID:    run,
		Title:    "Create hello file",
		Status:   taskStatus,
		Tier:     state.TierStandard,
		TaskType: state.TaskTypeImplementation,
	}
	if err := e.db.CreateTask(taskRecord); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := e.db.AddTaskTargetFile(&state.TaskTargetFile{
		TaskID:          taskID,
		FilePath:        "hello.txt",
		LockScope:       "file",
		LockResourceKey: "hello.txt",
	}); err != nil {
		t.Fatalf("AddTaskTargetFile: %v", err)
	}

	attemptID, err := e.db.CreateAttempt(&state.TaskAttempt{
		TaskID:        taskID,
		AttemptNumber: 1,
		ModelID:       "meeseeks/model",
		ModelFamily:   "meeseeks",
		Tier:          state.TierStandard,
		BaseSnapshot:  "base-sha",
		Status:        state.AttemptRunning,
		Phase:         state.PhaseExecuting,
	})
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}

	attemptRecord, err := e.db.GetAttempt(attemptID)
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	return taskRecord, attemptRecord
}

func seedActiveRun(t *testing.T, e *Engine, runID string) string {
	t.Helper()

	projectID := "project-" + runID
	if err := e.db.CreateProject(&state.Project{
		ID:       projectID,
		RootPath: e.rootDir,
		Name:     projectID,
		Slug:     projectID,
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := e.db.CreateRun(&state.ProjectRun{
		ID:                  runID,
		ProjectID:           projectID,
		Status:              state.RunActive,
		BaseBranch:          "main",
		WorkBranch:          "axiom/test",
		OrchestratorMode:    "external",
		OrchestratorRuntime: "codex",
		SRSApprovalDelegate: "user",
		BudgetMaxUSD:        10,
		ConfigSnapshot:      "{}",
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return runID
}

func buildManifestJSON(taskID, attemptID string, outputFiles map[string]string, deleteFiles []string, renameFiles map[string]string) string {
	var added []string
	for relPath := range outputFiles {
		added = append(added, fmt.Sprintf(`{"path":"%s","binary":false}`, relPath))
	}
	var renames []string
	for from, to := range renameFiles {
		renames = append(renames, fmt.Sprintf(`{"from":"%s","to":"%s"}`, from, to))
	}
	return fmt.Sprintf(`{
  "task_id": "%s",
  "base_snapshot": "base-sha",
  "files": {
    "added": [%s],
    "modified": [],
    "deleted": [%s],
    "renamed": [%s]
  }
}`, taskID, strings.Join(added, ","), joinQuoted(deleteFiles), strings.Join(renames, ","))
}

func joinQuoted(items []string) string {
	if len(items) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf(`"%s"`, item))
	}
	return strings.Join(quoted, ",")
}

func mountHostPath(mounts []string, containerPath string) string {
	for _, mount := range mounts {
		parts := strings.Split(mount, ":")
		if len(parts) >= 2 && parts[1] == containerPath {
			return parts[0]
		}
	}
	return ""
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
