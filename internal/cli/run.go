package cli

import (
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/engine"
	"github.com/spf13/cobra"
)

// RunCmd creates the `axiom run "<prompt>"` command.
func RunCmd(verbose *bool) *cobra.Command {
	var budgetUSD float64
	var allowDirty bool

	cmd := &cobra.Command{
		Use:   "run <prompt>",
		Short: "Start a new project run",
		Long: "Start a new project: generate SRS, await approval, execute.\n\n" +
			"By default axiom refuses to start on a dirty working tree (Architecture §28.2).\n" +
			"Pass --allow-dirty to bypass this check for crash-recovery scenarios where\n" +
			"resuming work on a branch with uncommitted state is intentional.",
		Args: cobra.ExactArgs(1),
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

			return runAction(application, projectID, args[0], budgetUSD, allowDirty, cmd.OutOrStdout())
		},
	}

	cmd.Flags().Float64Var(&budgetUSD, "budget", 0, "budget in USD (defaults to config value)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "bypass the clean-working-tree check (recovery only)")
	return cmd
}

// runAction starts a new project run via the engine's high-level StartRun entrypoint.
func runAction(application *app.App, projectID, prompt string, budgetUSD float64, allowDirty bool, w io.Writer) error {
	run, err := application.Engine.StartRun(engine.StartRunOptions{
		ProjectID:  projectID,
		Prompt:     prompt,
		BaseBranch: "main",
		BudgetUSD:  budgetUSD,
		Source:     "cli",
		AllowDirty: allowDirty,
	})
	if err != nil {
		return fmt.Errorf("starting run: %w", err)
	}

	fmt.Fprintf(w, "Run created: %s\n", run.ID)
	fmt.Fprintf(w, "  Status: %s\n", run.Status)
	fmt.Fprintf(w, "  Branch: %s\n", run.WorkBranch)
	fmt.Fprintf(w, "  Mode:   external orchestrator\n")
	fmt.Fprintf(w, "  Budget: $%.2f\n", run.BudgetMaxUSD)
	fmt.Fprintf(w, "  Prompt: %s\n", run.InitialPrompt)
	fmt.Fprintf(w, "\nWaiting for external orchestrator to submit SRS draft.\n")
	fmt.Fprintf(w, "Use 'axiom srs show' to view draft status.\n")
	return nil
}

// PauseCmd creates the `axiom pause` command.
func PauseCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause execution",
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

			return pauseAction(application, run.ID, cmd.OutOrStdout())
		},
	}
}

// pauseAction pauses an active run and prints confirmation.
func pauseAction(application *app.App, runID string, w io.Writer) error {
	if err := application.Engine.PauseRun(runID); err != nil {
		return fmt.Errorf("pausing run: %w", err)
	}
	fmt.Fprintf(w, "Run %s paused.\n", runID)
	return nil
}

// ResumeCmd creates the `axiom resume` command.
func ResumeCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused project",
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

			return resumeAction(application, run.ID, cmd.OutOrStdout())
		},
	}
}

// resumeAction resumes a paused run and prints confirmation.
func resumeAction(application *app.App, runID string, w io.Writer) error {
	if err := application.Engine.ResumeRun(runID); err != nil {
		return fmt.Errorf("resuming run: %w", err)
	}
	fmt.Fprintf(w, "Run %s resumed.\n", runID)
	return nil
}

// CancelCmd creates the `axiom cancel` command.
func CancelCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel",
		Short: "Cancel execution, kill containers, revert uncommitted changes",
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

			return cancelAction(application, run.ID, cmd.OutOrStdout())
		},
	}
}

// cancelAction cancels a run and prints confirmation.
func cancelAction(application *app.App, runID string, w io.Writer) error {
	if err := application.Engine.CancelRun(runID); err != nil {
		return fmt.Errorf("cancelling run: %w", err)
	}
	fmt.Fprintf(w, "Run %s cancelled.\n", runID)
	return nil
}
