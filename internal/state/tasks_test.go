package state

import (
	"testing"
)

func TestCreateTask(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	task := &Task{
		ID:       "task-1",
		RunID:    runID,
		Title:    "Implement auth module",
		Status:   TaskQueued,
		Tier:     TierStandard,
		TaskType: TaskTypeImplementation,
	}
	desc := "Build the authentication module"
	task.Description = &desc

	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := db.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Implement auth module" {
		t.Errorf("Title = %q, want %q", got.Title, "Implement auth module")
	}
	if got.Status != TaskQueued {
		t.Errorf("Status = %q, want %q", got.Status, TaskQueued)
	}
	if got.Description == nil || *got.Description != "Build the authentication module" {
		t.Errorf("Description mismatch")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestGetTaskNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetTask("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListTasksByRun(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, id := range []string{"t-1", "t-2", "t-3"} {
		task := &Task{ID: id, RunID: runID, Title: id, Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation}
		if err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err := db.ListTasksByRun(runID)
	if err != nil {
		t.Fatalf("ListTasksByRun: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("len = %d, want 3", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	statuses := []TaskStatus{TaskQueued, TaskQueued, TaskInProgress}
	for i, s := range statuses {
		task := &Task{ID: "ts-" + string(rune('a'+i)), RunID: runID, Title: "task", Status: s, Tier: TierStandard, TaskType: TaskTypeImplementation}
		if err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	queued, err := db.ListTasksByStatus(runID, TaskQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 {
		t.Errorf("queued = %d, want 2", len(queued))
	}

	inProgress, err := db.ListTasksByStatus(runID, TaskInProgress)
	if err != nil {
		t.Fatal(err)
	}
	if len(inProgress) != 1 {
		t.Errorf("in_progress = %d, want 1", len(inProgress))
	}
}

func TestUpdateTaskStatus_ValidTransitions(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	transitions := []struct {
		from TaskStatus
		to   TaskStatus
	}{
		{TaskQueued, TaskInProgress},
		{TaskQueued, TaskWaitingOnLock},
		{TaskInProgress, TaskDone},
		{TaskInProgress, TaskFailed},
		{TaskWaitingOnLock, TaskInProgress},
	}

	for i, tr := range transitions {
		task := &Task{
			ID: "tt-" + string(rune('a'+i)), RunID: runID,
			Title: "task", Status: tr.from, Tier: TierStandard, TaskType: TaskTypeImplementation,
		}
		if err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateTaskStatus(task.ID, tr.to); err != nil {
			t.Errorf("transition %s→%s failed: %v", tr.from, tr.to, err)
		}
		got, _ := db.GetTask(task.ID)
		if got.Status != tr.to {
			t.Errorf("after transition, Status = %q, want %q", got.Status, tr.to)
		}
	}
}

func TestUpdateTaskStatus_InvalidTransition(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	task := &Task{
		ID: "tt-inv", RunID: runID, Title: "task",
		Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	err := db.UpdateTaskStatus("tt-inv", TaskDone)
	if err == nil {
		t.Error("expected error for invalid transition queued → done")
	}
}

func TestUpdateTaskStatus_SetsCompletedAt(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	task := &Task{
		ID: "tt-comp", RunID: runID, Title: "task",
		Status: TaskInProgress, Tier: TierStandard, TaskType: TaskTypeImplementation,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskStatus("tt-comp", TaskDone); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetTask("tt-comp")
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set after completion")
	}
}

func TestTaskDependencies(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	for _, id := range []string{"dep-a", "dep-b", "dep-c"} {
		task := &Task{ID: id, RunID: runID, Title: id, Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation}
		if err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	// dep-c depends on dep-a and dep-b
	if err := db.AddTaskDependency("dep-c", "dep-a"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTaskDependency("dep-c", "dep-b"); err != nil {
		t.Fatal(err)
	}

	deps, err := db.GetTaskDependencies("dep-c")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Errorf("deps = %d, want 2", len(deps))
	}

	// dep-a has no dependencies
	deps, err = db.GetTaskDependencies("dep-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Errorf("deps = %d, want 0", len(deps))
	}
}

func TestTaskTargetFiles(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	tf := &TaskTargetFile{
		TaskID: taskID, FilePath: "internal/auth/auth.go",
		LockScope: "file", LockResourceKey: "internal/auth/auth.go",
	}
	if err := db.AddTaskTargetFile(tf); err != nil {
		t.Fatal(err)
	}

	files, err := db.GetTaskTargetFiles(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if files[0].FilePath != "internal/auth/auth.go" {
		t.Errorf("FilePath = %q", files[0].FilePath)
	}
}

func TestTaskSRSRefs(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	if err := db.AddTaskSRSRef(taskID, "FR-001"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTaskSRSRef(taskID, "AC-003"); err != nil {
		t.Fatal(err)
	}

	refs, err := db.GetTaskSRSRefs(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("refs = %d, want 2", len(refs))
	}
}

func TestAcquireLock(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	err := db.AcquireLock("file", "internal/auth/auth.go", taskID)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	locks, err := db.GetTaskLocks(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 {
		t.Fatalf("locks = %d, want 1", len(locks))
	}
	if locks[0].ResourceKey != "internal/auth/auth.go" {
		t.Errorf("ResourceKey = %q", locks[0].ResourceKey)
	}
}

func TestAcquireLockConflict(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	// Create two tasks
	t1 := &Task{ID: "lock-t1", RunID: runID, Title: "t1", Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation}
	t2 := &Task{ID: "lock-t2", RunID: runID, Title: "t2", Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation}
	if err := db.CreateTask(t1); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(t2); err != nil {
		t.Fatal(err)
	}

	// First lock succeeds
	if err := db.AcquireLock("file", "shared.go", "lock-t1"); err != nil {
		t.Fatal(err)
	}

	// Second lock on same resource should fail
	err := db.AcquireLock("file", "shared.go", "lock-t2")
	if err != ErrLockConflict {
		t.Errorf("expected ErrLockConflict, got %v", err)
	}
}

func TestReleaseLock(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	if err := db.AcquireLock("file", "release.go", taskID); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseLock("file", "release.go"); err != nil {
		t.Fatal(err)
	}

	locks, err := db.GetTaskLocks(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Errorf("locks after release = %d, want 0", len(locks))
	}
}

func TestReleaseTaskLocks(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	if err := db.AcquireLock("file", "a.go", taskID); err != nil {
		t.Fatal(err)
	}
	if err := db.AcquireLock("file", "b.go", taskID); err != nil {
		t.Fatal(err)
	}

	if err := db.ReleaseTaskLocks(taskID); err != nil {
		t.Fatal(err)
	}

	locks, err := db.GetTaskLocks(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Errorf("locks after ReleaseTaskLocks = %d, want 0", len(locks))
	}
}

func TestLockWait(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)

	t1 := &Task{ID: "wait-t1", RunID: runID, Title: "t1", Status: TaskQueued, Tier: TierStandard, TaskType: TaskTypeImplementation}
	t2 := &Task{ID: "wait-t2", RunID: runID, Title: "t2", Status: TaskWaitingOnLock, Tier: TierStandard, TaskType: TaskTypeImplementation}
	if err := db.CreateTask(t1); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(t2); err != nil {
		t.Fatal(err)
	}

	blockedBy := "wait-t1"
	wait := &TaskLockWait{
		TaskID:             "wait-t2",
		WaitReason:         "initial_dispatch",
		RequestedResources: `[{"resource_type":"file","resource_key":"shared.go"}]`,
		BlockedByTaskID:    &blockedBy,
	}
	if err := db.AddLockWait(wait); err != nil {
		t.Fatal(err)
	}

	waits, err := db.ListLockWaits(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 {
		t.Fatalf("waits = %d, want 1", len(waits))
	}
	if waits[0].TaskID != "wait-t2" {
		t.Errorf("TaskID = %q", waits[0].TaskID)
	}

	if err := db.RemoveLockWait("wait-t2"); err != nil {
		t.Fatal(err)
	}

	waits, err = db.ListLockWaits(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 0 {
		t.Errorf("waits after remove = %d, want 0", len(waits))
	}
}
