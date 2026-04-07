package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openaxiom/axiom/internal/ipc"
	"github.com/openaxiom/axiom/internal/state"
)

const defaultTaskMaxFileLength = 500

func (e *Engine) buildTaskSpec(ctx context.Context, task *state.Task, attempt *state.TaskAttempt) (ipc.TaskSpec, error) {
	refs, err := e.db.GetTaskSRSRefs(task.ID)
	if err != nil {
		return ipc.TaskSpec{}, fmt.Errorf("loading task srs refs: %w", err)
	}

	targetFiles, err := e.db.GetTaskTargetFiles(task.ID)
	if err != nil {
		return ipc.TaskSpec{}, fmt.Errorf("loading task target files: %w", err)
	}

	contextBlocks, err := e.buildTaskContextBlocks(ctx, task, attempt, refs, targetFiles)
	if err != nil {
		return ipc.TaskSpec{}, err
	}

	spec := ipc.TaskSpec{
		TaskID:            task.ID,
		BaseSnapshot:      attempt.BaseSnapshot,
		Objective:         e.buildTaskObjective(task, refs),
		ContextBlocks:     contextBlocks,
		InterfaceContract: e.buildInterfaceContract(ctx, refs, targetFiles),
		Constraints: ipc.TaskConstraints{
			Language:      detectProjectLanguage(e.rootDir),
			Style:         "Follow the repository's existing conventions and architecture.",
			Dependencies:  "Prefer existing project dependencies; only add new ones when required by the task.",
			MaxFileLength: defaultTaskMaxFileLength,
		},
		AcceptanceCriteria: e.buildAcceptanceCriteria(task, refs, targetFiles),
	}

	return spec, nil
}

func (e *Engine) buildTaskObjective(task *state.Task, refs []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(task.Title))
	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(strings.TrimSpace(*task.Description))
	}
	if len(refs) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "(SRS refs: %s)", strings.Join(refs, ", "))
	}
	if b.Len() == 0 {
		return "Complete the assigned implementation task."
	}
	return b.String()
}

func (e *Engine) buildTaskContextBlocks(ctx context.Context, task *state.Task, attempt *state.TaskAttempt, refs []string, targetFiles []state.TaskTargetFile) ([]ipc.ContextBlock, error) {
	var blocks []ipc.ContextBlock

	if len(targetFiles) > 0 {
		sort.Slice(targetFiles, func(i, j int) bool {
			return targetFiles[i].FilePath < targetFiles[j].FilePath
		})
		for _, tf := range targetFiles {
			fullPath := filepath.Join(e.rootDir, filepath.FromSlash(tf.FilePath))
			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("reading target file %s: %w", tf.FilePath, err)
			}
			blocks = append(blocks, ipc.ContextBlock{
				Label:      fmt.Sprintf("File Context: %s", tf.FilePath),
				SourcePath: tf.FilePath,
				StartLine:  1,
				Content:    string(data),
			})
		}
	}

	if len(refs) > 0 {
		blocks = append(blocks, ipc.ContextBlock{
			Label:      "SRS Traceability",
			SourcePath: ".axiom/srs.md",
			StartLine:  1,
			Content:    "Relevant SRS refs: " + strings.Join(refs, ", "),
		})
	}

	if feedback := e.buildPriorFeedback(attempt); feedback != "" {
		blocks = append(blocks, ipc.ContextBlock{
			Label:      "Prior Attempt Feedback",
			SourcePath: "task_attempts",
			StartLine:  1,
			Content:    feedback,
		})
	}

	if len(blocks) > 0 {
		return blocks, nil
	}

	if repoMap := e.buildRepoMap(ctx); repoMap != "" {
		return []ipc.ContextBlock{{
			Label:      "Repo Map (tier: repo-map)",
			SourcePath: "semantic_index",
			StartLine:  1,
			Content:    repoMap,
		}}, nil
	}

	return []ipc.ContextBlock{{
		Label:      "Task Context",
		SourcePath: "task_metadata",
		StartLine:  1,
		Content:    e.buildTaskObjective(task, refs),
	}}, nil
}

func (e *Engine) buildPriorFeedback(current *state.TaskAttempt) string {
	attempts, err := e.db.ListAttemptsByTask(current.TaskID)
	if err != nil {
		return ""
	}

	var entries []string
	for _, attempt := range attempts {
		if attempt.ID == current.ID {
			continue
		}
		var parts []string
		if attempt.FailureReason != nil && strings.TrimSpace(*attempt.FailureReason) != "" {
			parts = append(parts, "failure="+strings.TrimSpace(*attempt.FailureReason))
		}
		if attempt.Feedback != nil && strings.TrimSpace(*attempt.Feedback) != "" {
			parts = append(parts, "feedback="+strings.TrimSpace(*attempt.Feedback))
		}
		if len(parts) == 0 {
			continue
		}
		entries = append(entries, fmt.Sprintf("Attempt %d (%s): %s", attempt.AttemptNumber, attempt.Tier, strings.Join(parts, "; ")))
	}

	return strings.Join(entries, "\n")
}

func (e *Engine) buildInterfaceContract(ctx context.Context, refs []string, targetFiles []state.TaskTargetFile) string {
	var parts []string
	if len(refs) > 0 {
		parts = append(parts, "Maintain behavior required by SRS refs: "+strings.Join(refs, ", "))
	}

	if e.index != nil {
		dirs := uniqueTargetDirs(targetFiles)
		for _, dir := range dirs {
			exports, err := e.index.ListExports(ctx, dir)
			if err != nil || len(exports) == 0 {
				continue
			}
			var lines []string
			for _, sym := range exports {
				sig := strings.TrimSpace(sym.Signature)
				if sig == "" {
					sig = sym.Name
				}
				lines = append(lines, sig)
			}
			parts = append(parts, fmt.Sprintf("Exports in %s:\n- %s", dir, strings.Join(lines, "\n- ")))
		}
	}

	if len(parts) == 0 {
		return "Preserve existing public interfaces for the task's target files unless the task explicitly requires a change."
	}

	return strings.Join(parts, "\n\n")
}

func (e *Engine) buildAcceptanceCriteria(task *state.Task, refs []string, targetFiles []state.TaskTargetFile) []string {
	var criteria []string
	criteria = append(criteria, "Complete the task objective without modifying files outside the declared scope.")
	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		criteria = append(criteria, strings.TrimSpace(*task.Description))
	}
	if len(refs) > 0 {
		criteria = append(criteria, "Satisfy the mapped SRS refs: "+strings.Join(refs, ", "))
	}
	if len(targetFiles) > 0 {
		var files []string
		for _, tf := range targetFiles {
			files = append(files, tf.FilePath)
		}
		sort.Strings(files)
		criteria = append(criteria, "Limit output to the expected target files: "+strings.Join(files, ", "))
	}
	return criteria
}

func (e *Engine) buildRepoMap(ctx context.Context) string {
	if e.index != nil {
		graph, err := e.index.ModuleGraph(ctx, "")
		if err == nil && graph != nil && len(graph.Packages) > 0 {
			var lines []string
			for _, pkg := range graph.Packages {
				if pkg.Path == "" && pkg.Dir == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s (%s)", pkg.Path, pkg.Dir))
			}
			sort.Strings(lines)
			if len(lines) > 0 {
				return "Indexed packages:\n- " + strings.Join(lines, "\n- ")
			}
		}
	}

	entries, err := os.ReadDir(e.rootDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".axiom" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return "Top-level repository entries:\n- " + strings.Join(names, "\n- ")
}

func uniqueTargetDirs(targetFiles []state.TaskTargetFile) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, tf := range targetFiles {
		dir := filepath.ToSlash(filepath.Dir(tf.FilePath))
		if dir == "." || dir == "" {
			dir = "."
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func detectProjectLanguage(root string) string {
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		return "Go"
	case fileExists(filepath.Join(root, "package.json")):
		return "Node.js / TypeScript"
	case fileExists(filepath.Join(root, "pyproject.toml")), fileExists(filepath.Join(root, "requirements.txt")):
		return "Python"
	case fileExists(filepath.Join(root, "Cargo.toml")):
		return "Rust"
	default:
		return "Follow the primary language already used in the repository."
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
