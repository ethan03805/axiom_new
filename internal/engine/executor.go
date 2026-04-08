package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/manifest"
	"github.com/openaxiom/axiom/internal/mergequeue"
	"github.com/openaxiom/axiom/internal/state"
)

const commitAttemptWindow = 5

func (e *Engine) executorLoop(ctx context.Context) error {
	runs, err := e.activeRunIDs()
	if err != nil {
		return fmt.Errorf("listing active runs for executor: %w", err)
	}

	for _, runID := range runs {
		tasks, err := e.db.ListTasksByStatus(runID, state.TaskInProgress)
		if err != nil {
			return fmt.Errorf("listing in-progress tasks for run %s: %w", runID, err)
		}
		for _, task := range tasks {
			attempt, err := e.latestRunningAttempt(task.ID)
			if err != nil {
				return err
			}
			if attempt == nil || attempt.Phase != state.PhaseExecuting {
				continue
			}
			if _, loaded := e.activeAttempts.LoadOrStore(attempt.ID, struct{}{}); loaded {
				continue
			}

			go func(task state.Task, attempt state.TaskAttempt) {
				defer e.activeAttempts.Delete(attempt.ID)
				e.executeAttempt(ctx, task, attempt)
			}(task, *attempt)
		}
	}

	return nil
}

func (e *Engine) executeAttempt(ctx context.Context, task state.Task, attempt state.TaskAttempt) {
	defer func() {
		if r := recover(); r != nil {
			e.log.Error("attempt executor panicked", "task_id", task.ID, "attempt_id", attempt.ID, "panic", r)
			if err := e.failAttempt(context.Background(), task, attempt, fmt.Sprintf("attempt executor panic: %v", r)); err != nil {
				e.log.Error("failed to record panic failure", "task_id", task.ID, "attempt_id", attempt.ID, "error", err)
			}
		}
	}()

	if err := e.runAttempt(ctx, task, attempt); err != nil {
		if errors.Is(err, errAttemptWaitingOnLock) {
			if releaseErr := e.sched.ReleaseLocks(ctx, task.ID); releaseErr != nil {
				e.log.Warn("failed to release locks after scope expansion wait", "task_id", task.ID, "error", releaseErr)
			}
			if cleanupErr := ipc.CleanupTaskDirs(e.rootDir, task.ID); cleanupErr != nil {
				e.log.Warn("failed to clean task dirs after scope wait", "task_id", task.ID, "error", cleanupErr)
			}
			return
		}
		if failErr := e.failAttempt(ctx, task, attempt, err.Error()); failErr != nil {
			e.log.Error("failed to record attempt failure", "task_id", task.ID, "attempt_id", attempt.ID, "error", failErr)
		}
	}
}

func (e *Engine) runAttempt(ctx context.Context, task state.Task, attempt state.TaskAttempt) error {
	dirs, err := ipc.CreateTaskDirs(e.rootDir, task.ID)
	if err != nil {
		return fmt.Errorf("creating task dirs: %w", err)
	}

	spec, err := e.buildTaskSpec(ctx, &task, &attempt)
	if err != nil {
		return err
	}
	if err := ipc.WriteTaskSpec(dirs.Spec, spec); err != nil {
		return fmt.Errorf("writing task spec: %w", err)
	}

	containerID, err := e.startMeeseeksContainer(ctx, &task, &attempt, dirs)
	if err != nil {
		return err
	}
	defer func() {
		if stopErr := e.container.Stop(context.Background(), containerID); stopErr != nil {
			e.log.Warn("failed to stop meeseeks container", "task_id", task.ID, "container", containerID, "error", stopErr)
		}
	}()

	monitorCtx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.Docker.TimeoutMinutes)*time.Minute)
	defer cancel()

	monitorResult, err := e.monitorTaskIPC(monitorCtx, ipcMonitorRequest{
		Task:    &task,
		Attempt: &attempt,
		Dirs:    dirs,
	})
	if err != nil {
		return err
	}
	if err := e.persistAttemptUsage(attempt.ID, monitorResult); err != nil {
		return err
	}
	attempt.CostUSD += monitorResult.CostUSD

	if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseValidating); err != nil {
		return fmt.Errorf("updating attempt to validating: %w", err)
	}

	manifestData, err := os.ReadFile(filepath.Join(dirs.Staging, "manifest.json"))
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	parsedManifest, err := manifest.ParseManifest(manifestData)
	if err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}
	if parsedManifest.TaskID != task.ID {
		return fmt.Errorf("manifest task_id %q does not match task %q", parsedManifest.TaskID, task.ID)
	}
	if parsedManifest.BaseSnapshot != attempt.BaseSnapshot {
		return fmt.Errorf("manifest base_snapshot %q does not match attempt snapshot %q", parsedManifest.BaseSnapshot, attempt.BaseSnapshot)
	}

	if errs := manifest.ValidateManifest(parsedManifest, dirs.Staging, allowedScope(task.ID, e.db), manifest.DefaultValidationConfig()); len(errs) > 0 {
		return joinErrors("manifest validation failed", errs)
	}

	artifacts, err := manifest.ComputeArtifacts(parsedManifest, dirs.Staging, attempt.ID)
	if err != nil {
		return fmt.Errorf("computing artifacts: %w", err)
	}
	if err := e.persistArtifacts(artifacts); err != nil {
		return err
	}

	if e.validation == nil {
		return fmt.Errorf("validation service unavailable")
	}
	validationResults, err := e.validation.RunChecks(ctx, ValidationCheckRequest{
		TaskID:      task.ID,
		RunID:       task.RunID,
		Image:       e.cfg.Docker.Image,
		StagingDir:  dirs.Staging,
		ProjectDir:  e.rootDir,
		Config:      &e.cfg.Validation,
		Languages:   detectValidationLanguages(e.rootDir),
		DeleteFiles: parsedManifest.Files.Deleted,
		RenameFiles: toValidationRenames(parsedManifest.Files.Renamed),
	})
	if err != nil {
		return fmt.Errorf("running validation checks: %w", err)
	}
	if err := e.persistValidationRuns(attempt.ID, validationResults); err != nil {
		return err
	}
	if !validationAllPassed(validationResults) {
		return fmt.Errorf("validation checks failed:\n%s", formatValidationResults(validationResults))
	}

	if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseReviewing); err != nil {
		return fmt.Errorf("updating attempt to reviewing: %w", err)
	}
	if e.review == nil {
		return fmt.Errorf("review service unavailable")
	}

	reviewTaskID := fmt.Sprintf("%s-review", task.ID)
	reviewDirs, err := ipc.CreateTaskDirs(e.rootDir, reviewTaskID)
	if err != nil {
		return fmt.Errorf("creating review dirs: %w", err)
	}
	defer func() {
		if cleanupErr := ipc.CleanupTaskDirs(e.rootDir, reviewTaskID); cleanupErr != nil {
			e.log.Warn("failed to clean review dirs", "task_id", task.ID, "error", cleanupErr)
		}
	}()

	taskSpecMarkdown, err := os.ReadFile(filepath.Join(dirs.Spec, "spec.md"))
	if err != nil {
		return fmt.Errorf("reading task spec markdown: %w", err)
	}
	if err := ipc.WriteReviewSpec(reviewDirs.Spec, ipc.ReviewSpec{
		TaskID:                task.ID,
		OriginalTaskSpec:      string(taskSpecMarkdown),
		MeeseeksOutput:        formatMeeseeksOutput(parsedManifest, dirs.Staging),
		MeeseeksOutputSource:  dirs.Staging,
		AutomatedCheckResults: formatValidationResults(validationResults),
	}); err != nil {
		return fmt.Errorf("writing review spec: %w", err)
	}

	reviewResult, err := e.review.RunReview(ctx, ReviewRunRequest{
		TaskID:         task.ID,
		RunID:          task.RunID,
		Image:          e.cfg.Docker.Image,
		SpecDir:        reviewDirs.Spec,
		TaskTier:       attempt.Tier,
		MeeseeksFamily: attempt.ModelFamily,
		CPULimit:       e.cfg.Docker.CPULimit,
		MemLimit:       e.cfg.Docker.MemLimit,
		AffectedFiles:  affectedFiles(parsedManifest),
	})
	if err != nil {
		return fmt.Errorf("running review: %w", err)
	}
	if err := e.persistReviewRun(attempt.ID, reviewResult); err != nil {
		return err
	}
	if reviewResult.Verdict != state.ReviewApprove {
		return fmt.Errorf("review rejected: %s", strings.TrimSpace(reviewResult.Feedback))
	}

	if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseAwaitingOrchestratorGate); err != nil {
		return fmt.Errorf("updating attempt to orchestrator gate: %w", err)
	}
	if !orchestratorGateApproved(reviewResult) {
		return fmt.Errorf("orchestrator gate rejected: %s", strings.TrimSpace(reviewResult.Feedback))
	}

	if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseQueuedForMerge); err != nil {
		return fmt.Errorf("updating attempt to queued_for_merge: %w", err)
	}
	e.EnqueueMerge(buildMergeItem(task, attempt, parsedManifest, dirs.Staging, reviewResult, e.db))
	return nil
}

func (e *Engine) failAttempt(ctx context.Context, task state.Task, attempt state.TaskAttempt, feedback string) error {
	attemptRecord, err := e.db.GetAttempt(attempt.ID)
	if err != nil {
		return fmt.Errorf("reloading attempt: %w", err)
	}
	if attemptRecord.Phase != state.PhaseFailed && attemptRecord.Phase != state.PhaseEscalated && attemptRecord.Phase != state.PhaseSucceeded {
		if err := e.db.UpdateAttemptPhase(attempt.ID, state.PhaseFailed); err != nil {
			return fmt.Errorf("updating attempt phase failed: %w", err)
		}
	}
	if attemptRecord.Status != state.AttemptFailed && attemptRecord.Status != state.AttemptPassed && attemptRecord.Status != state.AttemptEscalated {
		if err := e.db.UpdateAttemptStatus(attempt.ID, state.AttemptFailed); err != nil {
			return fmt.Errorf("updating attempt status failed: %w", err)
		}
	}
	if _, err := e.db.Exec(`UPDATE task_attempts SET failure_reason = ?, feedback = ? WHERE id = ?`, feedback, feedback, attempt.ID); err != nil {
		return fmt.Errorf("storing failure feedback: %w", err)
	}

	currentTask, err := e.db.GetTask(task.ID)
	if err != nil {
		return fmt.Errorf("reloading task: %w", err)
	}
	if currentTask.Status == state.TaskInProgress {
		if err := e.db.UpdateTaskStatus(task.ID, state.TaskFailed); err != nil {
			return fmt.Errorf("marking task failed: %w", err)
		}
	}

	if e.sched != nil {
		if err := e.sched.ReleaseLocks(ctx, task.ID); err != nil {
			return fmt.Errorf("releasing task locks: %w", err)
		}
	}

	if err := ipc.CleanupTaskDirs(e.rootDir, task.ID); err != nil {
		return fmt.Errorf("cleaning task dirs: %w", err)
	}

	if e.tasks == nil {
		return fmt.Errorf("task service unavailable")
	}
	action, err := e.tasks.HandleTaskFailure(ctx, task.ID, feedback)
	if err != nil {
		return err
	}

	// Architecture §11.5 + §30.1: when a test-type task exhausts all retries
	// and escalations the convergence pair must be marked blocked so the run
	// cannot silently pass the completion gate.
	if action == TaskFailureBlock && task.TaskType == state.TaskTypeTest && e.testGen != nil {
		cp, lookupErr := e.db.GetConvergencePairByTestTask(task.ID)
		if lookupErr == nil && cp != nil {
			if markErr := e.testGen.MarkBlocked(ctx, cp.ImplTaskID); markErr != nil {
				e.log.Warn("testgen MarkBlocked failed",
					"impl_task_id", cp.ImplTaskID,
					"test_task_id", task.ID,
					"error", markErr)
			}
		}
	}
	return nil
}

func (e *Engine) startMeeseeksContainer(ctx context.Context, task *state.Task, attempt *state.TaskAttempt, dirs ipc.Dirs) (string, error) {
	if e.container == nil {
		return "", fmt.Errorf("container service unavailable")
	}

	timeoutMs := int64(e.cfg.Docker.TimeoutMinutes) * int64(time.Minute/time.Millisecond)
	spec := ContainerSpec{
		Name:      fmt.Sprintf("axiom-%s-%d", task.ID, attempt.ID),
		Image:     e.cfg.Docker.Image,
		CPULimit:  e.cfg.Docker.CPULimit,
		MemLimit:  e.cfg.Docker.MemLimit,
		Network:   e.cfg.Docker.NetworkMode,
		Mounts:    dirs.VolumeMounts(),
		TimeoutMs: timeoutMs,
		Env: map[string]string{
			"AXIOM_CONTAINER_TYPE": string(state.ContainerMeeseeks),
			"AXIOM_TASK_ID":        task.ID,
			"AXIOM_RUN_ID":         task.RunID,
			"AXIOM_ATTEMPT_ID":     fmt.Sprintf("%d", attempt.ID),
			"AXIOM_MODEL_ID":       attempt.ModelID,
		},
	}
	containerID, err := e.container.Start(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("starting meeseeks container: %w", err)
	}
	return containerID, nil
}

func (e *Engine) activeRunIDs() ([]string, error) {
	rows, err := e.db.Query(`SELECT id FROM project_runs WHERE status = ?`, string(state.RunActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *Engine) latestRunningAttempt(taskID string) (*state.TaskAttempt, error) {
	attempts, err := e.db.ListAttemptsByTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("listing attempts for %s: %w", taskID, err)
	}
	if len(attempts) == 0 {
		return nil, nil
	}
	latest := attempts[len(attempts)-1]
	if latest.Status != state.AttemptRunning {
		return nil, nil
	}
	return &latest, nil
}

func (e *Engine) persistAttemptUsage(attemptID int64, usage *ipcMonitorResult) error {
	if usage == nil {
		return nil
	}
	if _, err := e.db.Exec(`UPDATE task_attempts SET input_tokens = ?, output_tokens = ?, cost_usd = COALESCE(cost_usd, 0) + ? WHERE id = ?`,
		usage.InputTokens, usage.OutputTokens, usage.CostUSD, attemptID); err != nil {
		return fmt.Errorf("updating attempt usage: %w", err)
	}
	return nil
}

func (e *Engine) persistArtifacts(records []manifest.ArtifactRecord) error {
	for _, record := range records {
		if _, err := e.db.CreateArtifact(&state.TaskArtifact{
			AttemptID:    record.AttemptID,
			Operation:    state.ArtifactOp(record.Operation),
			PathFrom:     record.PathFrom,
			PathTo:       record.PathTo,
			SHA256Before: record.SHA256Before,
			SHA256After:  record.SHA256After,
			SizeBefore:   record.SizeBefore,
			SizeAfter:    record.SizeAfter,
		}); err != nil {
			return fmt.Errorf("creating artifact record: %w", err)
		}
	}
	return nil
}

func (e *Engine) persistValidationRuns(attemptID int64, results []ValidationCheckResult) error {
	for _, result := range results {
		output := result.Output
		duration := result.DurationMs
		if _, err := e.db.CreateValidationRun(&state.ValidationRun{
			AttemptID:  attemptID,
			CheckType:  result.CheckType,
			Status:     result.Status,
			Output:     &output,
			DurationMs: &duration,
		}); err != nil {
			return fmt.Errorf("creating validation run: %w", err)
		}
	}
	return nil
}

func (e *Engine) persistReviewRun(attemptID int64, result *ReviewRunResult) error {
	if result == nil {
		return nil
	}
	feedback := result.Feedback
	if _, err := e.db.CreateReviewRun(&state.ReviewRun{
		AttemptID:      attemptID,
		ReviewerModel:  result.ReviewerModel,
		ReviewerFamily: result.ReviewerFamily,
		Verdict:        result.Verdict,
		Feedback:       &feedback,
	}); err != nil {
		return fmt.Errorf("creating review run: %w", err)
	}
	return nil
}

func validationAllPassed(results []ValidationCheckResult) bool {
	for _, result := range results {
		if result.Status == state.ValidationFail {
			return false
		}
	}
	return true
}

func formatValidationResults(results []ValidationCheckResult) string {
	var lines []string
	for _, result := range results {
		status := strings.ToUpper(string(result.Status))
		line := fmt.Sprintf("%s: %s (%dms)", result.CheckType, status, result.DurationMs)
		if strings.TrimSpace(result.Output) != "" {
			line += "\n" + strings.TrimSpace(result.Output)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func detectValidationLanguages(root string) []string {
	var langs []string
	if fileExists(filepath.Join(root, "go.mod")) {
		langs = append(langs, "go")
	}
	if fileExists(filepath.Join(root, "package.json")) {
		langs = append(langs, "node")
	}
	if fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "requirements.txt")) {
		langs = append(langs, "python")
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		langs = append(langs, "rust")
	}
	sort.Strings(langs)
	return langs
}

func formatMeeseeksOutput(m *manifest.Manifest, stagingDir string) string {
	var sections []string
	for _, file := range m.Files.Added {
		sections = append(sections, formatStagedFile("ADDED", file.Path, file.Binary, stagingDir))
	}
	for _, file := range m.Files.Modified {
		sections = append(sections, formatStagedFile("MODIFIED", file.Path, file.Binary, stagingDir))
	}
	for _, rename := range m.Files.Renamed {
		sections = append(sections, fmt.Sprintf("RENAMED: %s -> %s\n%s", rename.From, rename.To, formatStagedFile("RENAMED TARGET", rename.To, false, stagingDir)))
	}
	if len(m.Files.Deleted) > 0 {
		sections = append(sections, "DELETED:\n- "+strings.Join(m.Files.Deleted, "\n- "))
	}
	return strings.Join(sections, "\n\n")
}

func formatStagedFile(kind, relPath string, binary bool, stagingDir string) string {
	if binary {
		return fmt.Sprintf("%s: %s\n[binary file omitted]", kind, relPath)
	}
	data, err := os.ReadFile(filepath.Join(stagingDir, filepath.FromSlash(relPath)))
	if err != nil {
		return fmt.Sprintf("%s: %s\n[unable to read staged file: %v]", kind, relPath, err)
	}
	return fmt.Sprintf("%s: %s\n```text\n%s\n```", kind, relPath, string(data))
}

func allowedScope(taskID string, db *state.DB) []string {
	targetFiles, err := db.GetTaskTargetFiles(taskID)
	if err != nil || len(targetFiles) == 0 {
		return nil
	}
	scopes := make([]string, 0, len(targetFiles))
	for _, tf := range targetFiles {
		scopes = append(scopes, tf.FilePath)
	}
	sort.Strings(scopes)
	return scopes
}

func affectedFiles(m *manifest.Manifest) []string {
	files := make([]string, 0, len(m.Files.Added)+len(m.Files.Modified)+len(m.Files.Deleted)+len(m.Files.Renamed)*2)
	for _, file := range m.Files.Added {
		files = append(files, file.Path)
	}
	for _, file := range m.Files.Modified {
		files = append(files, file.Path)
	}
	files = append(files, m.Files.Deleted...)
	for _, rename := range m.Files.Renamed {
		files = append(files, rename.From, rename.To)
	}
	sort.Strings(files)
	return files
}

func toValidationRenames(renames []manifest.RenameEntry) []ValidationRename {
	out := make([]ValidationRename, 0, len(renames))
	for _, rename := range renames {
		out = append(out, ValidationRename{From: rename.From, To: rename.To})
	}
	return out
}

func orchestratorGateApproved(result *ReviewRunResult) bool {
	return result != nil && result.Verdict == state.ReviewApprove
}

func buildMergeItem(task state.Task, attempt state.TaskAttempt, m *manifest.Manifest, stagingDir string, reviewResult *ReviewRunResult, db *state.DB) mergequeue.MergeItem {
	refs, _ := db.GetTaskSRSRefs(task.ID)

	item := mergequeue.MergeItem{
		TaskID:       task.ID,
		RunID:        task.RunID,
		AttemptID:    attempt.ID,
		BaseSnapshot: attempt.BaseSnapshot,
		StagingDir:   stagingDir,
		CommitInfo: mergequeue.CommitInfo{
			TaskTitle:     task.Title,
			TaskID:        task.ID,
			SRSRefs:       refs,
			MeeseeksModel: attempt.ModelID,
			AttemptNumber: attempt.AttemptNumber,
			MaxAttempts:   commitAttemptWindow,
			CostUSD:       attempt.CostUSD,
			BaseSnapshot:  attempt.BaseSnapshot,
		},
		DeleteFiles: m.Files.Deleted,
	}

	if reviewResult != nil {
		item.CommitInfo.ReviewerModel = reviewResult.ReviewerModel
	}

	for _, file := range m.Files.Added {
		item.OutputFiles = append(item.OutputFiles, file.Path)
	}
	for _, file := range m.Files.Modified {
		item.OutputFiles = append(item.OutputFiles, file.Path)
	}
	for _, rename := range m.Files.Renamed {
		item.OutputFiles = append(item.OutputFiles, rename.To)
		item.RenameFiles = append(item.RenameFiles, mergequeue.RenameOp{From: rename.From, To: rename.To})
	}

	return item
}

func joinErrors(prefix string, errs []error) error {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return fmt.Errorf("%s: %s", prefix, strings.Join(parts, "; "))
}
