package state

import (
	"testing"
)

func TestCreateContainerSession(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	cs := &ContainerSession{
		ID:            "axiom-meeseeks-abc123",
		RunID:         runID,
		TaskID:        taskID,
		ContainerType: ContainerMeeseeks,
		Image:         "axiom-meeseeks-multi:latest",
	}
	if err := db.CreateContainerSession(cs); err != nil {
		t.Fatalf("CreateContainerSession: %v", err)
	}

	got, err := db.GetContainerSession("axiom-meeseeks-abc123")
	if err != nil {
		t.Fatalf("GetContainerSession: %v", err)
	}
	if got.ContainerType != ContainerMeeseeks {
		t.Errorf("ContainerType = %q", got.ContainerType)
	}
	if got.StoppedAt != nil {
		t.Error("StoppedAt should be nil")
	}
}

func TestGetContainerSessionNotFound(t *testing.T) {
	db := testDB(t)

	_, err := db.GetContainerSession("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListActiveContainers(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	for _, id := range []string{"c-1", "c-2"} {
		cs := &ContainerSession{
			ID: id, RunID: runID, TaskID: taskID,
			ContainerType: ContainerMeeseeks, Image: "img",
		}
		if err := db.CreateContainerSession(cs); err != nil {
			t.Fatal(err)
		}
	}

	active, err := db.ListActiveContainers(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("active = %d, want 2", len(active))
	}

	// Stop one
	if err := db.MarkContainerStopped("c-1", "completed"); err != nil {
		t.Fatal(err)
	}

	active, err = db.ListActiveContainers(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Errorf("active after stop = %d, want 1", len(active))
	}
}

func TestMarkContainerStopped(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	cs := &ContainerSession{
		ID: "c-stop", RunID: runID, TaskID: taskID,
		ContainerType: ContainerMeeseeks, Image: "img",
	}
	if err := db.CreateContainerSession(cs); err != nil {
		t.Fatal(err)
	}

	if err := db.MarkContainerStopped("c-stop", "timeout"); err != nil {
		t.Fatalf("MarkContainerStopped: %v", err)
	}

	got, _ := db.GetContainerSession("c-stop")
	if got.StoppedAt == nil {
		t.Error("StoppedAt should be set")
	}
	if got.ExitReason == nil || *got.ExitReason != "timeout" {
		t.Error("ExitReason should be 'timeout'")
	}
}

func TestListAllContainers(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	taskID := seedTask(t, db, runID)

	for _, id := range []string{"ca-1", "ca-2", "ca-3"} {
		cs := &ContainerSession{
			ID: id, RunID: runID, TaskID: taskID,
			ContainerType: ContainerMeeseeks, Image: "img",
		}
		if err := db.CreateContainerSession(cs); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MarkContainerStopped("ca-2", "completed"); err != nil {
		t.Fatal(err)
	}

	all, err := db.ListContainersByRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("all = %d, want 3", len(all))
	}
}
