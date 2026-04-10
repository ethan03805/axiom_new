package cli

import (
	"errors"
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
	var force bool
	var baseBranch string

	cmd := &cobra.Command{
		Use:   "run <prompt>",
		Short: "Start a new project run",
		Long: "Start a new project: generate SRS, await approval, execute.\n\n" +
			"By default axiom refuses to start on a dirty working tree (Architecture §28.2).\n" +
			"Pass --allow-dirty to bypass this check for crash-recovery scenarios where\n" +
			"resuming work on a branch with uncommitted state is intentional.\n\n" +
			"By default axiom refuses to start a new run when the project already has\n" +
			"an in-flight run (draft_srs, awaiting_srs_approval, active, or paused).\n" +
			"Pass --force to replace the existing run. The previous run's draft files\n" +
			"remain on disk; use 'axiom export' to recover orphaned state.\n\n" +
			"By default axiom detects the repository's base branch from local state\n" +
			"(init.defaultBranch, current branch, then main/master). Pass --base-branch\n" +
			"to override detection for repositories with unusual trunk names.",
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

			return runAction(application, projectID, args[0], budgetUSD, allowDirty, force, baseBranch, cmd.OutOrStdout())
		},
	}

	cmd.Flags().Float64Var(&budgetUSD, "budget", 0, "budget in USD (defaults to config value)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "bypass the clean-working-tree check (recovery only)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing in-flight run (clobbers prior state)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "base branch to branch from (default: auto-detect from repo)")
	return cmd
}

// runAction starts a new project run via the engine's high-level StartRun entrypoint.
func runAction(application *app.App, projectID, prompt string, budgetUSD float64, allowDirty bool, force bool, baseBranch string, w io.Writer) error {
	run, err := application.Engine.StartRun(engine.StartRunOptions{
		ProjectID:  projectID,
		Prompt:     prompt,
		BaseBranch: baseBranch,
		BudgetUSD:  budgetUSD,
		Source:     "cli",
		AllowDirty: allowDirty,
		Force:      force,
	})
	if err != nil {
		var activeErr *engine.ActiveRunExistsError
		if errors.As(err, &activeErr) {
			return fmt.Errorf(
				"a run already exists for this project: %s (%s).\n"+
					"Cancel it with 'axiom cancel' or re-run with --force to replace it.\n"+
					"Use 'axiom export' to inspect the existing run's state before deciding",
				activeErr.RunID, activeErr.Status)
		}
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
