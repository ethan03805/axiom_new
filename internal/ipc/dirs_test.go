package ipc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskDirsPaths(t *testing.T) {
	dirs := TaskDirs("/project", "task-042")

	if dirs.Spec != filepath.Join("/project", ".axiom", "containers", "specs", "task-042") {
		t.Errorf("Spec = %q", dirs.Spec)
	}
	if dirs.Staging != filepath.Join("/project", ".axiom", "containers", "staging", "task-042") {
		t.Errorf("Staging = %q", dirs.Staging)
	}
	if dirs.Input != filepath.Join("/project", ".axiom", "containers", "ipc", "task-042", "input") {
		t.Errorf("Input = %q", dirs.Input)
	}
	if dirs.Output != filepath.Join("/project", ".axiom", "containers", "ipc", "task-042", "output") {
		t.Errorf("Output = %q", dirs.Output)
	}
}

func TestCreateTaskDirs(t *testing.T) {
	root := t.TempDir()

	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatalf("CreateTaskDirs: %v", err)
	}

	// Verify all directories were created
	for _, d := range []string{dirs.Spec, dirs.Staging, dirs.Input, dirs.Output} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("directory %q does not exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

func TestCreateTaskDirsIdempotent(t *testing.T) {
	root := t.TempDir()

	// Create twice should not error
	if _, err := CreateTaskDirs(root, "task-001"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := CreateTaskDirs(root, "task-001"); err != nil {
		t.Fatalf("second create: %v", err)
	}
}

func TestCleanupTaskDirs(t *testing.T) {
	root := t.TempDir()

	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}

	// Write a file into staging to verify cleanup removes contents
	testFile := filepath.Join(dirs.Staging, "output.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CleanupTaskDirs(root, "task-001"); err != nil {
		t.Fatalf("CleanupTaskDirs: %v", err)
	}

	// Verify directories are gone
	for _, d := range []string{dirs.Spec, dirs.Staging, dirs.Input, dirs.Output} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("directory %q should not exist after cleanup", d)
		}
	}
}

func TestCleanupTaskDirsNonexistent(t *testing.T) {
	root := t.TempDir()

	// Cleaning up nonexistent dirs should not error
	if err := CleanupTaskDirs(root, "nonexistent"); err != nil {
		t.Fatalf("CleanupTaskDirs on nonexistent: %v", err)
	}
}

func TestCreateMultipleTaskDirs(t *testing.T) {
	root := t.TempDir()

	dirs1, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}
	dirs2, err := CreateTaskDirs(root, "task-002")
	if err != nil {
		t.Fatal(err)
	}

	// Verify they are independent
	if dirs1.Spec == dirs2.Spec {
		t.Error("different tasks should have different spec dirs")
	}
	if dirs1.Staging == dirs2.Staging {
		t.Error("different tasks should have different staging dirs")
	}
}

func TestWriteMessage(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}

	env, err := NewEnvelope(MsgShutdown, "task-001", nil)
	if err != nil {
		t.Fatal(err)
	}

	path, err := WriteMessage(dirs.Input, env)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("message file does not exist: %v", err)
	}
}

func TestReadMessages(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}

	// Write two messages
	env1, _ := NewEnvelope(MsgInferenceRequest, "task-001", map[string]string{"seq": "1"})
	env2, _ := NewEnvelope(MsgInferenceResponse, "task-001", map[string]string{"seq": "2"})

	if _, err := WriteMessage(dirs.Output, env1); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMessage(dirs.Output, env2); err != nil {
		t.Fatal(err)
	}

	messages, err := ReadMessages(dirs.Output)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Messages should be ordered by filename (which includes sequence number)
	if messages[0].Type != MsgInferenceRequest {
		t.Errorf("messages[0].Type = %q, want %q", messages[0].Type, MsgInferenceRequest)
	}
	if messages[1].Type != MsgInferenceResponse {
		t.Errorf("messages[1].Type = %q, want %q", messages[1].Type, MsgInferenceResponse)
	}
}

func TestReadMessagesEmptyDir(t *testing.T) {
	root := t.TempDir()
	dirs, err := CreateTaskDirs(root, "task-001")
	if err != nil {
		t.Fatal(err)
	}

	messages, err := ReadMessages(dirs.Output)
	if err != nil {
		t.Fatalf("ReadMessages on empty dir: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(messages))
	}
}

func TestWriteMessageSequencing(t *testing.T) {
	dir := t.TempDir()

	// Write three messages and verify they get sequential filenames
	for i := 0; i < 3; i++ {
		env, _ := NewEnvelope(MsgShutdown, "task-001", nil)
		if _, err := WriteMessage(dir, env); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 files, got %d", len(entries))
	}

	// Verify lexicographic ordering of filenames
	for i := 1; i < len(entries); i++ {
		if entries[i].Name() <= entries[i-1].Name() {
			t.Errorf("filenames not ordered: %q <= %q", entries[i].Name(), entries[i-1].Name())
		}
	}
}

func TestVolumeMounts(t *testing.T) {
	// Architecture Section 12.3: verify mount path generation
	dirs := TaskDirs("/project", "task-042")
	mounts := dirs.VolumeMounts()

	if len(mounts) != 3 {
		t.Fatalf("expected 3 mounts, got %d", len(mounts))
	}

	// Spec mount should be read-only
	specMount := mounts[0]
	expected := dirs.Spec + ":/workspace/spec:ro"
	if specMount != expected {
		t.Errorf("spec mount = %q, want %q", specMount, expected)
	}

	// Staging mount should be read-write
	stagingMount := mounts[1]
	expected = dirs.Staging + ":/workspace/staging:rw"
	if stagingMount != expected {
		t.Errorf("staging mount = %q, want %q", stagingMount, expected)
	}

	// IPC mount should be read-write (parent of input/output)
	ipcMount := mounts[2]
	ipcParent := filepath.Dir(dirs.Input) // .axiom/containers/ipc/<task-id>/
	expected = ipcParent + ":/workspace/ipc:rw"
	if ipcMount != expected {
		t.Errorf("ipc mount = %q, want %q", ipcMount, expected)
	}
}
