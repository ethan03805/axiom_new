package cli

import (
	"bytes"
	"fmt"
	"os"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/session"
	"github.com/openaxiom/axiom/internal/tui"
	"github.com/spf13/cobra"
)

// sessionAppOverride is set in tests to inject a test app.
var sessionAppOverride *app.App

// tuiAppOverride is set in tests to inject a test app.
var tuiAppOverride *app.App

// newSessionManager creates a session manager from the app.
func newSessionManager(application *app.App) *session.Manager {
	return session.New(application.Engine, application.Config, application.Log)
}

// SessionCmd creates the `axiom session` command with real implementations (Phase 15).
func SessionCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage interactive sessions",
	}

	cmd.AddCommand(sessionListCmd(verbose))
	cmd.AddCommand(sessionResumeCmd(verbose))
	cmd.AddCommand(sessionExportCmd(verbose))

	return cmd
}

func sessionListCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List resumable sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			application := sessionAppOverride
			if application == nil {
				var err error
				application, err = openApp(verbose)
				if err != nil {
					return err
				}
				defer application.Close()
			}

			projID, err := findProjectID(application)
			if err != nil {
				return err
			}

			mgr := newSessionManager(application)
			renderer := tui.NewPlainRenderer(application.Engine, mgr, application.Config, projID, application.Log)

			var buf bytes.Buffer
			if err := renderer.RenderSessionList(&buf, projID); err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), buf.String())
			return nil
		},
	}
}

func sessionResumeCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a persisted session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application := sessionAppOverride
			if application == nil {
				var err error
				application, err = openApp(verbose)
				if err != nil {
					return err
				}
				defer application.Close()
			}

			mgr := newSessionManager(application)
			sess, err := mgr.ResumeSession(args[0])
			if err != nil {
				return fmt.Errorf("resuming session: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Resumed session %s (mode: %s)\n", sess.ID[:8], sess.Mode)
			return nil
		},
	}
}

func sessionExportCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application := sessionAppOverride
			if application == nil {
				var err error
				application, err = openApp(verbose)
				if err != nil {
					return err
				}
				defer application.Close()
			}

			projID, err := findProjectID(application)
			if err != nil {
				return err
			}

			mgr := newSessionManager(application)
			export, err := mgr.ExportSession(args[0])
			if err != nil {
				return fmt.Errorf("exporting session: %w", err)
			}

			renderer := tui.NewPlainRenderer(application.Engine, mgr, application.Config, projID, application.Log)
			var buf bytes.Buffer
			renderer.RenderExport(&buf, export)
			fmt.Fprint(cmd.OutOrStdout(), buf.String())
			return nil
		},
	}
}

// TUICmd creates the `axiom tui` command (Phase 15).
func TUICmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			plain, _ := cmd.Flags().GetBool("plain")

			application := tuiAppOverride
			if application == nil {
				var err error
				application, err = openApp(verbose)
				if err != nil {
					return err
				}
				defer application.Close()
			}

			projID, err := findProjectID(application)
			if err != nil {
				return err
			}

			mgr := newSessionManager(application)

			if plain || !isInteractive() {
				return runPlainMode(cmd, application, mgr, projID)
			}

			return runTUIMode(application, mgr, projID)
		},
	}

	cmd.Flags().Bool("plain", false, "force plain-text renderer")
	return cmd
}

func runPlainMode(cmd *cobra.Command, application *app.App, mgr *session.Manager, projID string) error {
	renderer := tui.NewPlainRenderer(application.Engine, mgr, application.Config, projID, application.Log)

	var buf bytes.Buffer
	if err := renderer.RenderStartup(&buf); err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), buf.String())
	return nil
}

func runTUIMode(application *app.App, mgr *session.Manager, projID string) error {
	cfg := application.Config
	m := tui.NewModel(application.Engine, mgr, cfg, projID, application.Log)

	p := tui.NewProgram(m)
	_, err := p.Run()
	return err
}

// isInteractive checks if stdout is a TTY.
func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
