package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/cli"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/gitops"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/openaxiom/axiom/internal/version"
	"github.com/spf13/cobra"
)

var verbose bool

func main() {
	root := &cobra.Command{
		Use:           "axiom",
		Short:         "Axiom — AI software orchestration system",
		Long:          "Axiom is a local-first AI software orchestration system that manages project lifecycle through isolated, disposable AI agents.",
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
	var noGit bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Axiom project in the current directory",
		Long: `Initialize a new Axiom project in the current directory.

If the directory is not already a git repository, 'axiom init' will run
'git init -b main' automatically so downstream commands like 'axiom run'
(which require a clean git work tree) succeed out of the box. Pass
--no-git to skip the auto-init step if you want to set up git manually
or are intentionally operating outside of version control for testing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
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

			// Ensure cwd is a git repo before handing off to project.Init,
			// so that downstream commands (axiom run, ValidateClean,
			// SetupWorkBranch) don't fail on a missing .git directory.
			// Users can opt out with --no-git.
			gitMgr := gitops.New(log)
			isRepo, repoErr := gitMgr.IsRepo(cwd)
			if repoErr != nil {
				return fmt.Errorf("checking git repo status: %w", repoErr)
			}
			switch {
			case isRepo:
				fmt.Fprintf(out, "Git repository detected: %s\n", cwd)
			case noGit:
				fmt.Fprintf(out, "Warning: %s is not a git repository and --no-git was set.\n", cwd)
				fmt.Fprintln(out, "         Downstream commands like 'axiom run' will fail until you run 'git init' manually.")
			default:
				if err := gitMgr.InitRepo(cwd); err != nil {
					return fmt.Errorf("auto-initializing git repo: %w", err)
				}
				fmt.Fprintf(out, "Initialized empty git repository in %s\n", cwd)
			}

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

			fmt.Fprintf(out, "Axiom project initialized in %s\n", cwd)
			fmt.Fprintf(out, "  Project: %s\n", name)
			fmt.Fprintf(out, "  Slug:    %s\n", slug)
			fmt.Fprintf(out, "  Config:  %s\n", project.ConfigPath(cwd))
			fmt.Fprintf(out, "  Branch:  %s\n", project.WorkBranch(slug))
			fmt.Fprintln(out, "\nNext: run 'axiom run \"<prompt>\"' to start a project.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "project name (defaults to directory name)")
	cmd.Flags().BoolVar(&noGit, "no-git", false, "skip automatic 'git init' when the directory is not a git repository")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project status",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
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
					fmt.Fprintf(out, "Axiom project: %s\n", application.Config.Project.Name)
					fmt.Fprintf(out, "  Root:   %s\n", application.ProjectRoot)
					fmt.Fprintf(out, "  Status: no project record (run 'axiom init' first)\n")
					return nil
				}
				return err
			}

			status, err := application.Engine.GetRunStatus(proj.ID)
			if err != nil {
				return fmt.Errorf("getting status: %w", err)
			}

			fmt.Fprintf(out, "Axiom project: %s\n", status.ProjectName)
			fmt.Fprintf(out, "  Root:   %s\n", application.ProjectRoot)

			if status.Run == nil {
				fmt.Fprintf(out, "  Status: idle (no active run)\n")
				fmt.Fprintf(out, "  Budget: $%.2f (configured maximum)\n", application.Config.Budget.MaxUSD)
			} else {
				fmt.Fprintf(out, "  Run:    %s\n", status.Run.ID)
				fmt.Fprintf(out, "  Status: %s\n", status.Run.Status)
				fmt.Fprintf(out, "  Branch: %s\n", status.Run.WorkBranch)
				fmt.Fprintf(out, "  Mode:   %s\n", status.Run.OrchestratorMode)
				if status.Run.Status == "draft_srs" && status.Run.OrchestratorMode == "external" {
					fmt.Fprintf(out, "  Waiting: external orchestrator to submit SRS draft\n")
				}
				if status.Run.InitialPrompt != "" {
					prompt := status.Run.InitialPrompt
					if len(prompt) > 80 {
						prompt = prompt[:77] + "..."
					}
					fmt.Fprintf(out, "  Prompt: %s\n", prompt)
				}
				fmt.Fprintf(out, "  Budget: $%.2f / $%.2f",
					status.Budget.SpentUSD, status.Budget.MaxUSD)
				if status.Budget.WarnReached {
					fmt.Fprintf(out, " [WARNING: %d%% threshold reached]", status.Budget.WarnPercent)
				}
				fmt.Fprintln(out)

				if status.Tasks.Total > 0 {
					fmt.Fprintf(out, "  Tasks:  %d total", status.Tasks.Total)
					if status.Tasks.Done > 0 {
						fmt.Fprintf(out, ", %d done", status.Tasks.Done)
					}
					if status.Tasks.InProgress > 0 {
						fmt.Fprintf(out, ", %d running", status.Tasks.InProgress)
					}
					if status.Tasks.Queued > 0 {
						fmt.Fprintf(out, ", %d queued", status.Tasks.Queued)
					}
					if status.Tasks.Failed > 0 {
						fmt.Fprintf(out, ", %d failed", status.Tasks.Failed)
					}
					if status.Tasks.Blocked > 0 {
						fmt.Fprintf(out, ", %d blocked", status.Tasks.Blocked)
					}
					fmt.Fprintln(out)
				}
			}
			return nil
		},
	}
}
