package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/cli"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/version"
	"github.com/spf13/cobra"
)

var verbose bool

func main() {
	root := &cobra.Command{
		Use:   "axiom",
		Short: "Axiom — AI software orchestration system",
		Long:  "Axiom is a local-first AI software orchestration system that manages project lifecycle through isolated, disposable AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose logging")

	root.AddCommand(versionCmd())
	root.AddCommand(initCmd())
	root.AddCommand(statusCmd())

	// Phase 14: Register all CLI commands from Section 27.
	for _, cmd := range cli.Commands(&verbose) {
		root.AddCommand(cmd)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show Axiom version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}

func initCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Axiom project in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := app.NewLogger(verbose)

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}

			// Default name to directory basename
			if name == "" {
				name = filepath.Base(cwd)
			}

			log.Info("initializing axiom project", "dir", cwd, "name", name)

			if err := project.Init(cwd, name); err != nil {
				return err
			}

			// Open database and run migrations to create initial schema
			dbPath := project.DBPath(cwd)
			db, err := state.Open(dbPath, log)
			if err != nil {
				return fmt.Errorf("creating database: %w", err)
			}
			defer db.Close()

			if err := db.Migrate(); err != nil {
				return fmt.Errorf("running migrations: %w", err)
			}

			// Validate the generated config
			cfg, err := config.Load(cwd)
			if err != nil {
				return fmt.Errorf("loading generated config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("generated config is invalid: %w", err)
			}

			slug := project.Slugify(name)

			// Create the project record in the database so subsequent
			// commands (status, session, run) can find it.
			proj := &state.Project{
				ID:       slug,
				RootPath: cwd,
				Name:     name,
				Slug:     slug,
			}
			if err := db.CreateProject(proj); err != nil {
				return fmt.Errorf("creating project record: %w", err)
			}

			fmt.Printf("Axiom project initialized in %s\n", cwd)
			fmt.Printf("  Project: %s\n", name)
			fmt.Printf("  Slug:    %s\n", slug)
			fmt.Printf("  Config:  %s\n", project.ConfigPath(cwd))
			fmt.Printf("  Branch:  %s\n", project.WorkBranch(slug))
			fmt.Println("\nNext: run 'axiom run \"<prompt>\"' to start a project.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "project name (defaults to directory name)")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project status",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := app.NewLogger(verbose)

			application, err := app.Open(log)
			if err != nil {
				return err
			}
			defer application.Close()

			// Find or create project record
			proj, err := application.DB.GetProjectByRootPath(application.ProjectRoot)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					fmt.Printf("Axiom project: %s\n", application.Config.Project.Name)
					fmt.Printf("  Root:   %s\n", application.ProjectRoot)
					fmt.Printf("  Status: no project record (run 'axiom init' first)\n")
					return nil
				}
				return err
			}

			status, err := application.Engine.GetRunStatus(proj.ID)
			if err != nil {
				return fmt.Errorf("getting status: %w", err)
			}

			fmt.Printf("Axiom project: %s\n", status.ProjectName)
			fmt.Printf("  Root:   %s\n", application.ProjectRoot)

			if status.Run == nil {
				fmt.Printf("  Status: idle (no active run)\n")
				fmt.Printf("  Budget: $%.2f (configured maximum)\n", application.Config.Budget.MaxUSD)
			} else {
				fmt.Printf("  Run:    %s\n", status.Run.ID)
				fmt.Printf("  Status: %s\n", status.Run.Status)
				fmt.Printf("  Branch: %s\n", status.Run.WorkBranch)
				fmt.Printf("  Budget: $%.2f / $%.2f",
					status.Budget.SpentUSD, status.Budget.MaxUSD)
				if status.Budget.WarnReached {
					fmt.Printf(" [WARNING: %d%% threshold reached]", status.Budget.WarnPercent)
				}
				fmt.Println()

				if status.Tasks.Total > 0 {
					fmt.Printf("  Tasks:  %d total", status.Tasks.Total)
					if status.Tasks.Done > 0 {
						fmt.Printf(", %d done", status.Tasks.Done)
					}
					if status.Tasks.InProgress > 0 {
						fmt.Printf(", %d running", status.Tasks.InProgress)
					}
					if status.Tasks.Queued > 0 {
						fmt.Printf(", %d queued", status.Tasks.Queued)
					}
					if status.Tasks.Failed > 0 {
						fmt.Printf(", %d failed", status.Tasks.Failed)
					}
					if status.Tasks.Blocked > 0 {
						fmt.Printf(", %d blocked", status.Tasks.Blocked)
					}
					fmt.Println()
				}
			}
			return nil
		},
	}
}
