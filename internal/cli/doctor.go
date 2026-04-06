package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openaxiom/axiom/internal/bitnet"
	"github.com/openaxiom/axiom/internal/config"
	"github.com/openaxiom/axiom/internal/doctor"
	"github.com/openaxiom/axiom/internal/project"
	"github.com/spf13/cobra"
)

// DoctorCmd creates the `axiom doctor` command.
func DoctorCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system requirements",
		Long:  "Check Docker, BitNet, network, resources, cache readiness, and secret scanner configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, root, err := loadDoctorConfig()
			if err != nil {
				return err
			}
			return doctorAction(cfg, root, cmd.OutOrStdout())
		},
	}
}

func loadDoctorConfig() (*config.Config, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	root := ""
	if discovered, err := project.Discover(cwd); err == nil {
		root = discovered
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}
	if cfg.Project.Name == "" {
		cfg.Project.Name = "doctor"
	}
	if cfg.Project.Slug == "" {
		cfg.Project.Slug = "doctor"
	}
	return cfg, root, nil
}

func doctorAction(cfg *config.Config, root string, w io.Writer) error {
	bitnetSvc := bitnet.NewService(cfg)
	report := doctor.New(doctor.Options{
		Config:      cfg,
		ProjectRoot: root,
		BitNetStatus: func(ctx context.Context) bitnet.ServiceStatus {
			return bitnetSvc.Status(ctx)
		},
	}).Run(context.Background())

	fmt.Fprintln(w, "Phase 19 Doctor Report")
	for _, check := range report.Checks {
		fmt.Fprintf(w, "[%s] %s: %s\n", strings.ToUpper(string(check.Status)), check.Name, check.Summary)
	}
	return nil
}
