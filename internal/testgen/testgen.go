// Package testgen implements test-generation separation and convergence logic.
// Per Architecture Section 11.5: Test Authorship Separation.
//
// This package enforces that implementation tasks and test tasks are authored
// by different model families, manages the convergence lifecycle between them,
// and handles post-test failure recovery through fix tasks.
package testgen

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// Service manages test-generation separation and convergence logic.
type Service struct {
	db  *state.DB
	bus *events.Bus
	log *slog.Logger
}

// New creates a new testgen service.
func New(db *state.DB, bus *events.Bus, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, bus: bus, log: log}
}

// CreateTestTask creates a test-generation task after an implementation task
// merges successfully. Per Architecture Section 11.5:
// - The impl task must be done and of type "implementation"
// - The successful attempt's model family is recorded for exclusion
// - A convergence pair is created linking impl → test
// - The test task depends on the impl task
func (s *Service) CreateTestTask(ctx context.Context, implTaskID string) (*state.Task, error) {
	implTask, err := s.db.GetTask(implTaskID)
	if err != nil {
		return nil, fmt.Errorf("getting implementation task: %w", err)
	}

	if implTask.TaskType != state.TaskTypeImplementation {
		return nil, fmt.Errorf("task %s is type %s, not implementation", implTaskID, implTask.TaskType)
	}
	if implTask.Status != state.TaskDone {
		return nil, fmt.Errorf("implementation task %s is not done (status: %s)", implTaskID, implTask.Status)
	}

	// Check for existing convergence pair (prevent duplicates)
	_, err = s.db.GetConvergencePairByImplTask(implTaskID)
	if err == nil {
		return nil, fmt.Errorf("convergence pair already exists for implementation task %s", implTaskID)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("checking existing convergence pair: %w", err)
	}

	// Get the model family from the successful attempt
	implFamily, err := s.getSuccessfulModelFamily(implTaskID)
	if err != nil {
		return nil, err
	}

	// Generate test task ID
	testTaskID := implTaskID + "-test"

	// Create the test task
	testTask := &state.Task{
		ID:       testTaskID,
		RunID:    implTask.RunID,
		Title:    fmt.Sprintf("Generate tests for: %s", implTask.Title),
		Status:   state.TaskQueued,
		Tier:     implTask.Tier,
		TaskType: state.TaskTypeTest,
	}

	// Create test task, dependency, and convergence pair atomically
	err = s.db.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tasks
			(id, run_id, title, description, status, tier, task_type, base_snapshot, eco_ref)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			testTask.ID, testTask.RunID, testTask.Title, testTask.Description,
			string(testTask.Status), string(testTask.Tier), string(testTask.TaskType),
			testTask.BaseSnapshot, testTask.ECORef)
		if err != nil {
			return fmt.Errorf("creating test task: %w", err)
		}

		_, err = tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`,
			testTaskID, implTaskID)
		if err != nil {
			return fmt.Errorf("adding dependency: %w", err)
		}

		_, err = tx.Exec(`INSERT INTO convergence_pairs
			(impl_task_id, test_task_id, status, impl_model_family, iteration)
			VALUES (?, ?, ?, ?, ?)`,
			implTaskID, testTaskID, string(state.ConvergenceTesting), implFamily, 1)
		if err != nil {
			return fmt.Errorf("creating convergence pair: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emitEvent(events.TestGenCreated, implTask.RunID, testTaskID, map[string]any{
		"impl_task":      implTaskID,
		"exclude_family": implFamily,
	})

	s.log.Info("test generation task created",
		"impl_task", implTaskID,
		"test_task", testTaskID,
		"exclude_family", implFamily,
	)

	return testTask, nil
}

// GetExcludeFamily returns the model family to exclude when dispatching
// a test task. Per Architecture Section 11.5: test tasks must use a different
// model family than the implementation. Returns empty string for non-test tasks.
func (s *Service) GetExcludeFamily(_ context.Context, taskID string) (string, error) {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return "", fmt.Errorf("getting task: %w", err)
	}

	if task.TaskType != state.TaskTypeTest {
		return "", nil
	}

	cp, err := s.db.GetConvergencePairByTestTask(taskID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("getting convergence pair: %w", err)
	}

	return cp.ImplModelFamily, nil
}

// HandleTestFailure creates an implementation-fix task when generated tests
// fail against the committed implementation. Per Architecture Section 11.5:
// 1. Create a follow-up implementation-fix task referencing the failing tests
// 2. The fix task receives committed code, failing test code, and failure output
// 3. The fix goes through the full approval pipeline
func (s *Service) HandleTestFailure(ctx context.Context, testTaskID string, failureOutput string) (*state.Task, error) {
	testTask, err := s.db.GetTask(testTaskID)
	if err != nil {
		return nil, fmt.Errorf("getting test task: %w", err)
	}

	if testTask.TaskType != state.TaskTypeTest {
		return nil, fmt.Errorf("task %s is type %s, not test", testTaskID, testTask.TaskType)
	}
	if testTask.Status != state.TaskFailed {
		return nil, fmt.Errorf("test task %s is not failed (status: %s)", testTaskID, testTask.Status)
	}

	// Find the convergence pair for this test task
	cp, err := s.db.GetConvergencePairByTestTask(testTaskID)
	if err != nil {
		return nil, fmt.Errorf("getting convergence pair: %w", err)
	}

	// Create fix task ID
	fixTaskID := fmt.Sprintf("%s-fix-%d", cp.ImplTaskID, cp.Iteration)

	// Build description with failure context
	desc := fmt.Sprintf(
		"Fix implementation to pass generated tests.\n\nOriginal implementation: %s\nFailing test task: %s\n\nFailure output:\n%s",
		cp.ImplTaskID, testTaskID, failureOutput,
	)

	fixTask := &state.Task{
		ID:          fixTaskID,
		RunID:       testTask.RunID,
		Title:       fmt.Sprintf("Fix implementation for failing tests: %s", cp.ImplTaskID),
		Description: &desc,
		Status:      state.TaskQueued,
		Tier:        testTask.Tier,
		TaskType:    state.TaskTypeImplementation,
	}

	// Create fix task, dependency, and update convergence pair atomically
	err = s.db.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tasks
			(id, run_id, title, description, status, tier, task_type, base_snapshot, eco_ref)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fixTask.ID, fixTask.RunID, fixTask.Title, fixTask.Description,
			string(fixTask.Status), string(fixTask.Tier), string(fixTask.TaskType),
			fixTask.BaseSnapshot, fixTask.ECORef)
		if err != nil {
			return fmt.Errorf("creating fix task: %w", err)
		}

		// Fix task depends on the test task (to receive test context)
		_, err = tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on) VALUES (?, ?)`,
			fixTaskID, testTaskID)
		if err != nil {
			return fmt.Errorf("adding fix dependency: %w", err)
		}

		// Update convergence pair atomically
		_, err = tx.Exec(`UPDATE convergence_pairs SET fix_task_id = ?, status = ?, iteration = iteration + 1 WHERE id = ?`,
			fixTaskID, string(state.ConvergenceFixing), cp.ID)
		if err != nil {
			return fmt.Errorf("updating convergence pair: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emitEvent(events.TestGenFixCreated, testTask.RunID, fixTaskID, map[string]any{
		"impl_task":  cp.ImplTaskID,
		"test_task":  testTaskID,
		"iteration":  cp.Iteration + 1,
	})

	s.log.Info("implementation fix task created",
		"impl_task", cp.ImplTaskID,
		"test_task", testTaskID,
		"fix_task", fixTaskID,
		"iteration", cp.Iteration+1,
	)

	return fixTask, nil
}

// CheckConvergence returns the convergence status for an implementation task.
// Returns empty string if no convergence pair exists.
func (s *Service) CheckConvergence(_ context.Context, implTaskID string) (state.ConvergenceStatus, error) {
	cp, err := s.db.GetConvergencePairByImplTask(implTaskID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("getting convergence pair: %w", err)
	}
	return cp.Status, nil
}

// MarkConverged marks a convergence pair as converged after the test task
// completes successfully. Per Architecture Section 11.5: completion criteria
// require both the implementation and its generated tests to converge.
func (s *Service) MarkConverged(_ context.Context, implTaskID string) error {
	cp, err := s.db.GetConvergencePairByImplTask(implTaskID)
	if err != nil {
		return fmt.Errorf("getting convergence pair: %w", err)
	}

	// Verify the test task is done
	if cp.TestTaskID == nil {
		return fmt.Errorf("no test task for implementation %s", implTaskID)
	}
	testTask, err := s.db.GetTask(*cp.TestTaskID)
	if err != nil {
		return fmt.Errorf("getting test task: %w", err)
	}
	if testTask.Status != state.TaskDone {
		return fmt.Errorf("test task %s is not done (status: %s)", *cp.TestTaskID, testTask.Status)
	}

	if err := s.db.UpdateConvergencePairStatus(cp.ID, state.ConvergenceConverged); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}

	s.emitEvent(events.TestGenConverged, "", implTaskID, map[string]any{
		"test_task": *cp.TestTaskID,
	})

	s.log.Info("feature converged", "impl_task", implTaskID, "test_task", *cp.TestTaskID)
	return nil
}

// MarkBlocked marks a convergence pair as blocked after exhausting
// retries/escalations in the fix loop.
func (s *Service) MarkBlocked(_ context.Context, implTaskID string) error {
	cp, err := s.db.GetConvergencePairByImplTask(implTaskID)
	if err != nil {
		return fmt.Errorf("getting convergence pair: %w", err)
	}

	if err := s.db.UpdateConvergencePairStatus(cp.ID, state.ConvergenceBlocked); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}

	s.emitEvent(events.TestGenBlocked, "", implTaskID, map[string]any{
		"iteration": cp.Iteration,
	})

	s.log.Info("convergence blocked", "impl_task", implTaskID, "iteration", cp.Iteration)
	return nil
}

// IsFeatureDone returns true only when the convergence pair is converged.
// Per Architecture Section 11.5: a feature is not considered done until
// both the implementation and its generated tests converge.
func (s *Service) IsFeatureDone(_ context.Context, implTaskID string) (bool, error) {
	cp, err := s.db.GetConvergencePairByImplTask(implTaskID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("getting convergence pair: %w", err)
	}
	return cp.Status == state.ConvergenceConverged, nil
}

// emitEvent publishes an event to the bus. Errors are logged but do not block.
func (s *Service) emitEvent(eventType events.EventType, runID, taskID string, details map[string]any) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(events.EngineEvent{
		Type:    eventType,
		RunID:   runID,
		TaskID:  taskID,
		Details: details,
	}); err != nil {
		s.log.Error("failed to emit testgen event", "type", eventType, "error", err)
	}
}

// getSuccessfulModelFamily returns the model family from the successful (passed)
// attempt for an implementation task.
func (s *Service) getSuccessfulModelFamily(taskID string) (string, error) {
	attempts, err := s.db.ListAttemptsByTask(taskID)
	if err != nil {
		return "", fmt.Errorf("listing attempts: %w", err)
	}

	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Status == state.AttemptPassed {
			return attempts[i].ModelFamily, nil
		}
	}

	return "", fmt.Errorf("no successful attempt found for task %s", taskID)
}
