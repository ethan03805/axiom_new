package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/spf13/cobra"
)

// TaskCmd creates the `axiom task` command group for orchestrator-driven
// task management (Issue B). It mirrors the WebSocket control verbs
// create_task / approve_output / reject_output so operators can drive runs
// from the shell without needing a connected orchestrator.
func TaskCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Create and manage run tasks",
	}

	cmd.AddCommand(taskCreateCmd(verbose))
	cmd.AddCommand(taskListCmd(verbose))
	cmd.AddCommand(taskApproveCmd(verbose))
	cmd.AddCommand(taskRejectCmd(verbose))

	return cmd
}

func taskCreateCmd(verbose *bool) *cobra.Command {
	var (
		objective    string
		contextTier  string
		files        []string
		constraints  []string
		acceptance   []string
		outputFormat string
		description  string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new task in the active run",
		Long: "Create a new task in the active run and enqueue it for scheduling.\n" +
			"The task starts in status=queued. Workers will pick it up on the\n" +
			"scheduler's next tick.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if objective == "" {
				return errors.New("--objective is required")
			}

			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			projectID, err := findProjectID(application)
			if err != nil {
				return err
			}

			run, err := findActiveRun(application, projectID)
			if err != nil {
				return err
			}

			spec := engine.TaskCreateSpec{
				Objective:          objective,
				Description:        description,
				ContextTier:        contextTier,
				Files:              files,
				Constraints:        constraints,
				AcceptanceCriteria: acceptance,
				OutputFormat:       outputFormat,
			}

			task, err := application.Engine.CreateTask(run.ID, spec)
			if err != nil {
				return fmt.Errorf("creating task: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Task created: %s\n", task.ID)
			fmt.Fprintf(out, "  Run:       %s\n", task.RunID)
			fmt.Fprintf(out, "  Status:    %s\n", task.Status)
			fmt.Fprintf(out, "  Objective: %s\n", objective)
			if len(files) > 0 {
				fmt.Fprintf(out, "  Files:     %v\n", files)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&objective, "objective", "", "short objective for the task (required)")
	cmd.Flags().StringVar(&description, "description", "", "long-form task description (optional)")
	cmd.Flags().StringVar(&contextTier, "context-tier", "", "context tier hint: symbol|file|package|repo_map|indexed")
	cmd.Flags().StringSliceVar(&files, "files", nil, "target file paths (repeatable)")
	cmd.Flags().StringSliceVar(&constraints, "constraint", nil, "textual constraint (repeatable)")
	cmd.Flags().StringSliceVar(&acceptance, "acceptance", nil, "acceptance criterion (repeatable)")
	cmd.Flags().StringVar(&outputFormat, "output-format", "files", "output format: patch|files")
	return cmd
}

func taskListCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tasks in the active run",
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

			run, err := findActiveRun(application, projectID)
			if err != nil {
				return err
			}

			return taskListAction(application, run.ID, cmd.OutOrStdout())
		},
	}
}

func taskListAction(application *app.App, runID string, w io.Writer) error {
	tasks, err := application.DB.ListTasksByRun(runID)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	fmt.Fprintf(w, "Tasks for run %s (%d total):\n", runID, len(tasks))
	if len(tasks) == 0 {
		fmt.Fprintln(w, "  (none)")
		return nil
	}
	for _, t := range tasks {
		fmt.Fprintf(w, "  %s  %-14s  %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}

func taskApproveCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <task-id> <attempt-id>",
		Short: "Approve a task attempt and enqueue it for merge",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			taskID, attemptID := args[0], args[1]
			if err := application.Engine.ApproveTaskOutput(taskID, attemptID); err != nil {
				return fmt.Errorf("approving task output: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Attempt %s for task %s enqueued for merge.\n",
				attemptID, taskID)
			return nil
		},
	}
}

func taskRejectCmd(verbose *bool) *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "reject <task-id> <attempt-id>",
		Short: "Reject a task attempt with a reason",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				return errors.New("--reason is required")
			}

			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			taskID, attemptID := args[0], args[1]
			if err := application.Engine.RejectTaskOutput(taskID, attemptID, reason); err != nil {
				return fmt.Errorf("rejecting task output: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Attempt %s for task %s marked rejected: %s\n",
				attemptID, taskID, reason)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "rejection reason (required)")
	return cmd
}
