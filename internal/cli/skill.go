package cli

import (
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/skill"
	"github.com/spf13/cobra"
)

// skillAppOverride is set in tests to inject a test app.
var skillAppOverride *app.App

// SkillCmd creates the `axiom skill` command (Phase 17).
func SkillCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage skill generation",
	}

	var runtime string
	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate skill file for specified runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			application := skillAppOverride
			if application == nil {
				var err error
				application, err = openApp(verbose)
				if err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Phase 17: skill generation requires an initialized Axiom project (%v).\n", err)
					return nil
				}
				defer application.Close()
			}

			return skillGenerateAction(application, runtime, cmd.OutOrStdout())
		},
	}

	generateCmd.Flags().StringVar(&runtime, "runtime", "", "target runtime (claw, claude-code, codex, opencode)")
	_ = generateCmd.MarkFlagRequired("runtime")
	cmd.AddCommand(generateCmd)
	return cmd
}

func skillGenerateAction(application *app.App, runtime string, w io.Writer) error {
	gen := skill.NewGenerator(application.ProjectRoot, application.Config)
	rt := skill.Runtime(runtime)
	artifacts, err := gen.Generate(rt)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Phase 17: Generated %d skill artifact(s) for %s.\n", len(artifacts), runtime)
	for _, artifact := range artifacts {
		fmt.Fprintf(w, "Generated: %s\n", artifact.Path)
	}

	warnings := skill.Warnings(rt)
	if len(warnings) > 0 {
		fmt.Fprintln(w)
		// First warning line gets the warning marker; subsequent lines are
		// indented to keep the block visually cohesive.
		for i, line := range warnings {
			if i == 0 {
				fmt.Fprintf(w, "WARNING: %s\n", line)
			} else {
				fmt.Fprintf(w, "         %s\n", line)
			}
		}
	}
	return nil
}
