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
type exportData struct {
	ProjectName string      `json:"project_name"`
	ProjectSlug string      `json:"project_slug"`
	ProjectRoot string      `json:"project_root"`
	Run         *exportRun  `json:"run,omitempty"`
	Tasks       []exportTask `json:"tasks,omitempty"`
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
	}

	// Include active run if one exists
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
