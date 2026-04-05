package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

// sequence is a package-level counter for unique message filenames.
var sequence atomic.Int64

// Dirs holds the IPC directory paths for a task.
// Per Architecture Section 28.1 and Section 12.3.
type Dirs struct {
	Spec    string // .axiom/containers/specs/<task-id>/
	Staging string // .axiom/containers/staging/<task-id>/
	Input   string // .axiom/containers/ipc/<task-id>/input/
	Output  string // .axiom/containers/ipc/<task-id>/output/
}

// TaskDirs returns the directory paths for a given task without creating them.
func TaskDirs(projectRoot, taskID string) Dirs {
	base := filepath.Join(projectRoot, ".axiom", "containers")
	return Dirs{
		Spec:    filepath.Join(base, "specs", taskID),
		Staging: filepath.Join(base, "staging", taskID),
		Input:   filepath.Join(base, "ipc", taskID, "input"),
		Output:  filepath.Join(base, "ipc", taskID, "output"),
	}
}

// CreateTaskDirs creates all IPC directories for a task.
// Safe to call multiple times (idempotent).
func CreateTaskDirs(projectRoot, taskID string) (Dirs, error) {
	dirs := TaskDirs(projectRoot, taskID)

	for _, d := range []string{dirs.Spec, dirs.Staging, dirs.Input, dirs.Output} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Dirs{}, fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	return dirs, nil
}

// CleanupTaskDirs removes all directories for a task.
// Does not error if directories do not exist.
func CleanupTaskDirs(projectRoot, taskID string) error {
	dirs := TaskDirs(projectRoot, taskID)

	// Remove the IPC parent directory (contains input/ and output/)
	ipcParent := filepath.Dir(dirs.Input)

	for _, d := range []string{dirs.Spec, dirs.Staging, ipcParent} {
		if err := os.RemoveAll(d); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", d, err)
		}
	}
	return nil
}

// VolumeMounts returns Docker volume mount strings for this task's directories.
// Per Architecture Section 12.3:
//   - /workspace/spec/ is read-only (TaskSpec/ReviewSpec input)
//   - /workspace/staging/ is read-write (Meeseeks output)
//   - /workspace/ipc/ is read-write (IPC communication)
func (d Dirs) VolumeMounts() []string {
	ipcParent := filepath.Dir(d.Input) // .axiom/containers/ipc/<task-id>/
	return []string{
		d.Spec + ":/workspace/spec:ro",
		d.Staging + ":/workspace/staging:rw",
		ipcParent + ":/workspace/ipc:rw",
	}
}

// WriteMessage writes a JSON envelope to the given directory with a
// sequentially-named file. Returns the path of the written file.
func WriteMessage(dir string, env Envelope) (string, error) {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling envelope: %w", err)
	}

	seq := sequence.Add(1)
	name := fmt.Sprintf("msg-%06d.json", seq)
	path := filepath.Join(dir, name)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing message to %s: %w", path, err)
	}
	return path, nil
}

// ReadMessages reads all JSON envelopes from the given directory, ordered by filename.
// Returns an empty slice if the directory is empty.
func ReadMessages(dir string) ([]Envelope, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", dir, err)
	}

	// Filter and sort JSON files
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	sort.Strings(jsonFiles)

	var messages []Envelope
	for _, name := range jsonFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		messages = append(messages, env)
	}

	return messages, nil
}
