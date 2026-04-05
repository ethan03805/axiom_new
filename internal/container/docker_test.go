package container

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/openaxiom/axiom/internal/events"
	"github.com/openaxiom/axiom/internal/state"
)

// mockExecutor records docker commands instead of running them.
type mockExecutor struct {
	commands [][]string
	outputs  []string
	errors   []error
	callIdx  int
}

func (m *mockExecutor) Run(_ context.Context, args ...string) (string, error) {
	m.commands = append(m.commands, args)
	idx := m.callIdx
	m.callIdx++
	if idx < len(m.errors) && m.errors[idx] != nil {
		return "", m.errors[idx]
	}
	if idx < len(m.outputs) {
		return m.outputs[idx], nil
	}
	return "", nil
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{}
}

func testDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := state.Open(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedProject(t *testing.T, db *state.DB) string {
	t.Helper()
	id := "proj-test"
	db.Exec(`INSERT INTO projects (id, root_path, name, slug) VALUES (?, ?, ?, ?)`,
		id, "/tmp/test-project", "test-project", "test-project")
	return id
}

func seedRun(t *testing.T, db *state.DB, projectID string) string {
	t.Helper()
	id := "run-test"
	db.Exec(`INSERT INTO project_runs
		(id, project_id, status, base_branch, work_branch, orchestrator_mode,
		 orchestrator_runtime, srs_approval_delegate, budget_max_usd, config_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, "active", "main", "axiom/test-project",
		"embedded", "claw", "user", 10.0, "{}")
	return id
}

func seedTask(t *testing.T, db *state.DB, runID string) string {
	t.Helper()
	id := "task-test"
	db.Exec(`INSERT INTO tasks (id, run_id, title, status, tier, task_type) VALUES (?, ?, ?, ?, ?, ?)`,
		id, runID, "test task", "queued", "standard", "implementation")
	return id
}

func testService(t *testing.T, exec *mockExecutor) (*DockerService, *state.DB) {
	t.Helper()
	db := testDB(t)
	projID := seedProject(t, db)
	runID := seedRun(t, db, projID)
	_ = seedTask(t, db, runID)

	bus := events.New(db, nil)
	cfg := config.Default("test", "test")

	svc := New(Options{
		ProjectRoot: t.TempDir(),
		Config:      &cfg.Docker,
		DB:          db,
		Bus:         bus,
		Exec:        exec,
	})
	return svc, db
}

// --- Container Naming ---

func TestContainerName(t *testing.T) {
	name := ContainerName("task-042")

	if !strings.HasPrefix(name, "axiom-task-042-") {
		t.Errorf("name should start with 'axiom-task-042-', got %q", name)
	}

	// Should have a timestamp suffix
	parts := strings.SplitN(name, "-", 4) // axiom-task-042-<timestamp>
	if len(parts) < 4 {
		t.Errorf("expected at least 4 parts, got %d: %q", len(parts), name)
	}
}

func TestContainerNameUniqueness(t *testing.T) {
	name1 := ContainerName("task-001")
	name2 := ContainerName("task-001")

	// Names should be unique even for the same task ID (different timestamps)
	if name1 == name2 {
		t.Error("container names should be unique")
	}
}

// --- Docker Command Building ---

func TestBuildArgsHardeningFlags(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:      "axiom-task-042-1234",
		Image:     "axiom-meeseeks-multi:latest",
		CPULimit:  0.5,
		MemLimit:  "2g",
		Network:   "none",
		Mounts:    []string{"/host/spec:/workspace/spec:ro", "/host/staging:/workspace/staging:rw"},
		TimeoutMs: 1800000,
	}

	args := BuildArgs(spec)

	// Architecture Section 12.6.1: all hardening flags required
	requiredFlags := []string{
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=256",
		"--network=none",
		"--name=axiom-task-042-1234",
	}

	argsStr := strings.Join(args, " ")
	for _, flag := range requiredFlags {
		if !strings.Contains(argsStr, flag) {
			t.Errorf("missing hardening flag: %s\nargs: %s", flag, argsStr)
		}
	}
}

func TestBuildArgsCPUAndMemory(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:     "axiom-test-1234",
		Image:    "axiom-meeseeks-multi:latest",
		CPULimit: 0.5,
		MemLimit: "2g",
		Network:  "none",
	}

	args := BuildArgs(spec)
	argsStr := strings.Join(args, " ")

	if !strings.Contains(argsStr, "--cpus=0.5") {
		t.Errorf("missing --cpus flag\nargs: %s", argsStr)
	}
	if !strings.Contains(argsStr, "--memory=2g") {
		t.Errorf("missing --memory flag\nargs: %s", argsStr)
	}
}

func TestBuildArgsTmpfs(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:    "axiom-test-1234",
		Image:   "axiom-meeseeks-multi:latest",
		Network: "none",
	}

	args := BuildArgs(spec)
	argsStr := strings.Join(args, " ")

	// Architecture Section 12.6.1: tmpfs for /tmp
	if !strings.Contains(argsStr, "--tmpfs") {
		t.Errorf("missing --tmpfs flag\nargs: %s", argsStr)
	}
	if !strings.Contains(argsStr, "/tmp:rw,noexec,size=256m") {
		t.Errorf("missing tmpfs spec for /tmp\nargs: %s", argsStr)
	}
}

func TestBuildArgsVolumeMounts(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:  "axiom-test-1234",
		Image: "axiom-meeseeks-multi:latest",
		Mounts: []string{
			"/host/spec:/workspace/spec:ro",
			"/host/staging:/workspace/staging:rw",
			"/host/ipc:/workspace/ipc:rw",
		},
		Network: "none",
	}

	args := BuildArgs(spec)
	argsStr := strings.Join(args, " ")

	for _, mount := range spec.Mounts {
		if !strings.Contains(argsStr, mount) {
			t.Errorf("missing mount: %s\nargs: %s", mount, argsStr)
		}
	}
}

func TestBuildArgsRemoveOnExit(t *testing.T) {
	// Architecture Section 12.6: docker run --rm for automatic cleanup
	spec := engine.ContainerSpec{
		Name:    "axiom-test-1234",
		Image:   "axiom-meeseeks-multi:latest",
		Network: "none",
	}

	args := BuildArgs(spec)
	found := false
	for _, a := range args {
		if a == "--rm" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing --rm flag for automatic cleanup")
	}
}

func TestBuildArgsImageIsLast(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:    "axiom-test-1234",
		Image:   "axiom-meeseeks-multi:latest",
		Network: "none",
	}

	args := BuildArgs(spec)
	if len(args) == 0 {
		t.Fatal("args is empty")
	}
	if args[len(args)-1] != "axiom-meeseeks-multi:latest" {
		t.Errorf("image should be last arg, got %q", args[len(args)-1])
	}
}

func TestBuildArgsFirstIsRun(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:    "axiom-test-1234",
		Image:   "axiom-meeseeks-multi:latest",
		Network: "none",
	}

	args := BuildArgs(spec)
	if args[0] != "run" {
		t.Errorf("first arg should be 'run', got %q", args[0])
	}
}

// --- Start ---

func TestStartCreatesContainerSession(t *testing.T) {
	exec := newMockExecutor()
	svc, db := testService(t, exec)

	spec := engine.ContainerSpec{
		Name:      "axiom-task-test-1234",
		Image:     "axiom-meeseeks-multi:latest",
		CPULimit:  0.5,
		MemLimit:  "2g",
		Network:   "none",
		TimeoutMs: 1800000,
	}

	id, err := svc.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if id == "" {
		t.Fatal("returned ID is empty")
	}

	// Verify container session was persisted
	cs, err := db.GetContainerSession(id)
	if err != nil {
		t.Fatalf("GetContainerSession: %v", err)
	}
	if cs.Image != "axiom-meeseeks-multi:latest" {
		t.Errorf("image = %q", cs.Image)
	}
	if cs.StoppedAt != nil {
		t.Error("StoppedAt should be nil for running container")
	}
}

func TestStartCallsDockerRun(t *testing.T) {
	exec := newMockExecutor()
	svc, _ := testService(t, exec)

	spec := engine.ContainerSpec{
		Name:    "axiom-task-test-1234",
		Image:   "axiom-meeseeks-multi:latest",
		Network: "none",
	}

	if _, err := svc.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	if len(exec.commands) == 0 {
		t.Fatal("no docker commands executed")
	}

	// First command should be a docker run
	cmd := exec.commands[0]
	if cmd[0] != "run" {
		t.Errorf("expected 'run' command, got %q", cmd[0])
	}
}

// --- Stop ---

func TestStopMarksContainerStopped(t *testing.T) {
	exec := newMockExecutor()
	svc, db := testService(t, exec)

	spec := engine.ContainerSpec{
		Name:    "axiom-task-test-1234",
		Image:   "img",
		Network: "none",
	}

	id, err := svc.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	cs, _ := db.GetContainerSession(id)
	if cs.StoppedAt == nil {
		t.Error("StoppedAt should be set after stop")
	}
	if cs.ExitReason == nil || *cs.ExitReason != "stopped" {
		t.Error("ExitReason should be 'stopped'")
	}
}

func TestStopCallsDockerStop(t *testing.T) {
	exec := newMockExecutor()
	svc, _ := testService(t, exec)

	spec := engine.ContainerSpec{
		Name:    "axiom-task-test-1234",
		Image:   "img",
		Network: "none",
	}

	id, _ := svc.Start(context.Background(), spec)
	exec.commands = nil // reset to capture stop command

	svc.Stop(context.Background(), id)

	if len(exec.commands) == 0 {
		t.Fatal("no docker commands for stop")
	}

	// Should call docker stop or docker rm -f
	cmd := exec.commands[0]
	found := false
	for _, arg := range cmd {
		if arg == "stop" || arg == "rm" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stop or rm command, got %v", cmd)
	}
}

// --- ListRunning ---

func TestListRunning(t *testing.T) {
	exec := newMockExecutor()
	exec.outputs = []string{"axiom-task-001-111\naxiom-task-002-222\n"}
	svc, _ := testService(t, exec)

	ids, err := svc.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(ids))
	}
	if ids[0] != "axiom-task-001-111" {
		t.Errorf("ids[0] = %q", ids[0])
	}
}

func TestListRunningEmpty(t *testing.T) {
	exec := newMockExecutor()
	exec.outputs = []string{""} // no running containers
	svc, _ := testService(t, exec)

	ids, err := svc.ListRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 containers, got %d", len(ids))
	}
}

func TestListRunningFiltersAxiomPrefix(t *testing.T) {
	exec := newMockExecutor()
	svc, _ := testService(t, exec)

	// The ListRunning call should filter by axiom-* prefix
	svc.ListRunning(context.Background())

	if len(exec.commands) == 0 {
		t.Fatal("no docker commands")
	}

	cmd := strings.Join(exec.commands[0], " ")
	if !strings.Contains(cmd, "axiom-") {
		t.Errorf("list command should filter by axiom- prefix: %s", cmd)
	}
}

// --- Cleanup ---

func TestCleanup(t *testing.T) {
	exec := newMockExecutor()
	// First call lists running containers, subsequent calls stop them
	exec.outputs = []string{"axiom-task-old-111\naxiom-task-old-222\n", "", ""}
	svc, _ := testService(t, exec)

	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Should have called list, then stop for each orphan
	if len(exec.commands) < 2 {
		t.Errorf("expected at least 2 commands (list + stops), got %d", len(exec.commands))
	}
}

func TestCleanupNoOrphans(t *testing.T) {
	exec := newMockExecutor()
	exec.outputs = []string{""} // no orphans
	svc, _ := testService(t, exec)

	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Should only have the list command
	if len(exec.commands) != 1 {
		t.Errorf("expected 1 command (list only), got %d", len(exec.commands))
	}
}

// --- Interface compliance ---

func TestDockerServiceImplementsContainerService(t *testing.T) {
	var _ engine.ContainerService = (*DockerService)(nil)
}
