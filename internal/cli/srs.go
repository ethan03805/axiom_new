package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/srs"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/spf13/cobra"
)

// SRSCmd creates the `axiom srs` command group for SRS operator surfaces.
func SRSCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "srs",
		Short: "View and manage SRS drafts",
	}

	cmd.AddCommand(srsShowCmd(verbose))
	cmd.AddCommand(srsApproveCmd(verbose))
	cmd.AddCommand(srsRejectCmd(verbose))
	cmd.AddCommand(srsSubmitCmd(verbose))

	return cmd
}

func srsShowCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current SRS draft or approved SRS",
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

			return srsShowAction(application, projectID, cmd.OutOrStdout())
		},
	}
}

func srsShowAction(application *app.App, projectID string, w io.Writer) error {
	run, err := findActiveRun(application, projectID)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Run:    %s\n", run.ID)
	fmt.Fprintf(w, "Status: %s\n", run.Status)

	if run.InitialPrompt != "" {
		fmt.Fprintf(w, "Prompt: %s\n", run.InitialPrompt)
	}

	switch run.Status {
	case state.RunDraftSRS:
		// Try to read a pending draft
		content, err := srs.ReadDraft(application.ProjectRoot, run.ID)
		if err != nil {
			fmt.Fprintf(w, "\nNo SRS draft submitted yet.\n")
			fmt.Fprintf(w, "Waiting for external orchestrator to submit SRS via:\n")
			fmt.Fprintf(w, "  CLI:  axiom srs submit <file>\n")
			fmt.Fprintf(w, "  API:  POST /api/v1/projects/%s/srs/submit\n", projectID)
			return nil
		}
		fmt.Fprintf(w, "\n--- SRS Draft ---\n%s\n", content)

	case state.RunAwaitingSRSApproval:
		content, err := srs.ReadDraft(application.ProjectRoot, run.ID)
		if err != nil {
			fmt.Fprintf(w, "\nSRS draft awaiting approval (draft file not found).\n")
			return nil
		}
		fmt.Fprintf(w, "\n--- SRS Draft (awaiting approval) ---\n%s\n", content)
		fmt.Fprintf(w, "\nApprove: axiom srs approve\n")
		fmt.Fprintf(w, "Reject:  axiom srs reject --feedback \"...\"\n")

	default:
		// Try to read the approved SRS
		srsPath := application.ProjectRoot + "/.axiom/srs.md"
		data, err := os.ReadFile(srsPath)
		if err != nil {
			fmt.Fprintf(w, "\nNo SRS available for current run status (%s).\n", run.Status)
			return nil
		}
		fmt.Fprintf(w, "\n--- Approved SRS ---\n%s\n", string(data))
	}

	return nil
}

func srsApproveCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "approve",
		Short: "Approve the pending SRS draft and transition to active",
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

			if err := application.Engine.ApproveSRS(run.ID); err != nil {
				return fmt.Errorf("approving SRS: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "SRS approved. Run %s is now active.\n", run.ID)
			return nil
		},
	}
}

func srsRejectCmd(verbose *bool) *cobra.Command {
	var feedback string

	cmd := &cobra.Command{
		Use:   "reject",
		Short: "Reject the pending SRS draft with feedback",
		RunE: func(cmd *cobra.Command, args []string) error {
			if feedback == "" {
				return errors.New("--feedback is required when rejecting an SRS")
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

			if err := application.Engine.RejectSRS(run.ID, feedback); err != nil {
				return fmt.Errorf("rejecting SRS: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "SRS rejected. Run %s returned to draft_srs for revision.\n", run.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&feedback, "feedback", "", "rejection feedback for the orchestrator")
	return cmd
}

func srsSubmitCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "submit <file>",
		Short: "Submit an SRS draft from a file",
		Long: `Submit an SRS draft from a file.

If <file> is "-", the SRS content is read from stdin instead of from disk.
The stdin form is intended for orchestrators operating under restrictive
hook policies (for example, Claude Code with the Axiom guard hook) that
cannot legitimately write the draft to the filesystem first.`,
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

			run, err := findActiveRun(application, projectID)
			if err != nil {
				return err
			}

			return srsSubmitAction(application, run.ID, args[0], cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// srsSubmitAction performs the SRS submission given a pre-resolved run ID.
// It is extracted from the cobra RunE so tests can exercise the stdin path
// without wiring through openApp.
func srsSubmitAction(application *app.App, runID, source string, stdin io.Reader, w io.Writer) error {
	content, err := readSRSSource(stdin, source)
	if err != nil {
		return err
	}

	if err := application.Engine.SubmitSRS(runID, content); err != nil {
		return fmt.Errorf("submitting SRS: %w", err)
	}

	fmt.Fprintf(w, "SRS submitted. Run %s is now awaiting approval.\n", runID)
	fmt.Fprintf(w, "Review:  axiom srs show\n")
	fmt.Fprintf(w, "Approve: axiom srs approve\n")
	return nil
}

// readSRSSource loads SRS draft content either from disk or from the given
// reader when source is "-". This keeps the orchestrator-side workflow
// compatible with hook policies that forbid direct filesystem writes.
func readSRSSource(stdin io.Reader, source string) (string, error) {
	if source == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("reading SRS from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("reading SRS file: %w", err)
	}
	return string(data), nil
}
