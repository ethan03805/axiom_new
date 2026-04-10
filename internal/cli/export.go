package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/spf13/cobra"
)

// ExportCmd creates the `axiom export` command.
func ExportCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Export project state as human-readable JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			projectID, err := findProjectID(application)
			if err != nil {
				return err
			}

			return exportAction(application, projectID, cmd.OutOrStdout())
		},
	}
}

// exportData is the JSON structure for project state export.
//
// The `run` field preserves the legacy single-active-run shape for
// backward compatibility. The `runs` array, added to fix GitHub #1's
// "no audit trail" gap, lists ALL runs for the project — including
// cancelled, completed, and orphaned runs whose draft files still
// live on disk but which were clobbered by a prior Force replace.
type exportData struct {
	ProjectName string       `json:"project_name"`
	ProjectSlug string       `json:"project_slug"`
	ProjectRoot string       `json:"project_root"`
	Run         *exportRun   `json:"run,omitempty"`
	Tasks       []exportTask `json:"tasks,omitempty"`
	Runs        []exportRun  `json:"runs"`
}

type exportRun struct {
	ID               string  `json:"id"`
	Status           string  `json:"status"`
	BaseBranch       string  `json:"base_branch"`
	WorkBranch       string  `json:"work_branch"`
	BudgetMax        float64 `json:"budget_max_usd"`
	InitialPrompt    string  `json:"initial_prompt,omitempty"`
	StartSource      string  `json:"start_source,omitempty"`
	OrchestratorMode string  `json:"orchestrator_mode,omitempty"`
	// Orphaned is true when the run's status is a terminal state
	// (cancelled, completed, error) or when it was superseded by a
	// Force replace. Emitted so operators can quickly spot historical
	// runs whose draft files may still exist on disk.
	Orphaned bool `json:"orphaned,omitempty"`
}

type exportTask struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Tier     string  `json:"tier"`
	Type     string  `json:"type"`
	ParentID *string `json:"parent_id,omitempty"`
}

// exportAction exports the project state as human-readable JSON.
func exportAction(application *app.App, projectID string, w io.Writer) error {
	proj, err := application.DB.GetProject(projectID)
	if err != nil {
		return fmt.Errorf("getting project: %w", err)
	}

	export := exportData{
		ProjectName: proj.Name,
		ProjectSlug: proj.Slug,
		ProjectRoot: application.ProjectRoot,
		Runs:        []exportRun{},
	}

	// Walk ALL runs for the project so cancelled / orphaned runs remain
	// discoverable via `axiom export`. This is the audit trail required
	// by GitHub #1 — when a run is clobbered by a Force replace, the
	// prior run's row is retained in project_runs and must surface here
	// so operators can recover or inspect its state.
	allRuns, err := application.DB.ListRunsByProject(projectID)
	if err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}
	for _, r := range allRuns {
		export.Runs = append(export.Runs, exportRun{
			ID:               r.ID,
			Status:           string(r.Status),
			BaseBranch:       r.BaseBranch,
			WorkBranch:       r.WorkBranch,
			BudgetMax:        r.BudgetMaxUSD,
			InitialPrompt:    r.InitialPrompt,
			StartSource:      r.StartSource,
			OrchestratorMode: r.OrchestratorMode,
			Orphaned:         isOrphanedRun(r.Status),
		})
	}

	// Preserve the legacy single-run field for backward compatibility:
	// callers that only read `run` should continue to see the currently
	// active run (if any) unchanged.
	run, err := application.DB.GetActiveRun(projectID)
	if err == nil {
		export.Run = &exportRun{
			ID:               run.ID,
			Status:           string(run.Status),
			BaseBranch:       run.BaseBranch,
			WorkBranch:       run.WorkBranch,
			BudgetMax:        run.BudgetMaxUSD,
			InitialPrompt:    run.InitialPrompt,
			StartSource:      run.StartSource,
			OrchestratorMode: run.OrchestratorMode,
		}

		// Include tasks for the active run
		tasks, err := application.DB.ListTasksByRun(run.ID)
		if err == nil {
			export.Tasks = make([]exportTask, len(tasks))
			for i, t := range tasks {
				export.Tasks[i] = exportTask{
					ID:       t.ID,
					Title:    t.Title,
					Status:   string(t.Status),
					Tier:     string(t.Tier),
					Type:     string(t.TaskType),
					ParentID: t.ParentID,
				}
			}
		}
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("getting run: %w", err)
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling export: %w", err)
	}

	fmt.Fprintln(w, string(data))
	return nil
}

// isOrphanedRun returns true for runs in a terminal state. Terminal
// runs that the operator hasn't explicitly inspected show up in the
// export as orphaned so their draft files (SRS drafts, task specs) can
// be recovered before manual cleanup.
func isOrphanedRun(status state.RunStatus) bool {
	switch status {
	case state.RunCancelled, state.RunCompleted, state.RunError:
		return true
	}
	return false
}
