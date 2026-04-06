package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubMessage returns a consistent message for commands that are planned
// but not yet implemented, referencing the target phase.
func stubMessage(command, phase string) string {
	return fmt.Sprintf("%s is not yet implemented (planned for %s).", command, phase)
}

// TUICmd creates the `axiom tui` stub command (Phase 15).
func TUICmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Interactive TUI", "Phase 15"))
			return nil
		},
	}

	cmd.Flags().Bool("plain", false, "force plain-text renderer")
	return cmd
}

// SessionCmd creates the `axiom session` stub command with subcommands (Phase 15).
func SessionCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage interactive sessions",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List resumable sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Session list", "Phase 15"))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a persisted session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Session resume", "Phase 15"))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Session export", "Phase 15"))
			return nil
		},
	})

	return cmd
}

// APICmd creates the `axiom api` stub command with subcommands (Phase 16).
func APICmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage API server",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the REST + WebSocket API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("API server start", "Phase 16"))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("API server stop", "Phase 16"))
			return nil
		},
	})

	tokenCmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API authentication tokens",
	}

	generateTokenCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("API token generate", "Phase 16"))
			return nil
		},
	}
	generateTokenCmd.Flags().String("scope", "", "token scope (read-only or full-control)")
	tokenCmd.AddCommand(generateTokenCmd)

	tokenCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List active API tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("API token list", "Phase 16"))
			return nil
		},
	})

	tokenCmd.AddCommand(&cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Revoke a specific API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("API token revoke", "Phase 16"))
			return nil
		},
	})

	cmd.AddCommand(tokenCmd)
	return cmd
}

// TunnelCmd creates the `axiom tunnel` stub command with subcommands (Phase 16).
func TunnelCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage Cloudflare Tunnel for remote access",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start Cloudflare Tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Tunnel start", "Phase 16"))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Tunnel stop", "Phase 16"))
			return nil
		},
	})

	return cmd
}

// SkillCmd creates the `axiom skill` stub command (Phase 17).
func SkillCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage skill generation",
	}

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate skill file for specified runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Skill generate", "Phase 17"))
			return nil
		},
	}

	generateCmd.Flags().String("runtime", "", "target runtime (claw, claude-code, codex, opencode)")
	cmd.AddCommand(generateCmd)
	return cmd
}

// DoctorCmd creates the `axiom doctor` stub command (Phase 19).
func DoctorCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system requirements",
		Long:  "Check Docker, BitNet, network, resources, warm-pool images, and secret scanner regexes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), stubMessage("Doctor", "Phase 19"))
			return nil
		},
	}
}
