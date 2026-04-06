// Package cli provides the plain CLI command surface for Axiom (Phase 14).
// All commands rely on engine projections and service methods rather than
// reading SQLite directly, per Architecture Section 26.
package cli

import "github.com/spf13/cobra"

// Commands returns all CLI commands to be registered on the root command.
// This includes fully functional commands backed by existing engine services
// and stub commands for subsystems that will be implemented in later phases.
func Commands(verbose *bool) []*cobra.Command {
	return []*cobra.Command{
		RunCmd(verbose),
		PauseCmd(verbose),
		ResumeCmd(verbose),
		CancelCmd(verbose),
		SRSCmd(verbose),
		ExportCmd(verbose),
		ModelsCmd(verbose),
		BitnetCmd(verbose),
		IndexCmd(verbose),
		SessionCmd(verbose),
		APICmd(verbose),
		TunnelCmd(verbose),
		SkillCmd(verbose),
		TUICmd(verbose),
		DoctorCmd(verbose),
	}
}
