package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/spf13/cobra"
)

// BitnetCmd creates the `axiom bitnet` parent command with subcommands.
func BitnetCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bitnet",
		Short: "Manage local BitNet inference server",
	}

	cmd.AddCommand(bitnetStartCmd(verbose))
	cmd.AddCommand(bitnetStopCmd(verbose))
	cmd.AddCommand(bitnetStatusCmd(verbose))
	cmd.AddCommand(bitnetModelsCmd(verbose))
	return cmd
}

func bitnetStartCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start local inference server",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return bitnetStartAction(application, cmd.OutOrStdout())
		},
	}
}

func bitnetStopCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop local inference server",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return bitnetStopAction(application, cmd.OutOrStdout())
		},
	}
}

func bitnetStatusCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server status, resource usage, active requests",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return bitnetStatusAction(application, cmd.OutOrStdout())
		},
	}
}

func bitnetModelsCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List available local models",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return bitnetModelsAction(application, cmd.OutOrStdout())
		},
	}
}

// bitnetStartAction starts the BitNet server.
func bitnetStartAction(application *app.App, w io.Writer) error {
	ctx := context.Background()
	if err := application.BitNet.Start(ctx); err != nil {
		return err
	}
	fmt.Fprintln(w, "BitNet server started.")
	return nil
}

// bitnetStopAction stops the BitNet server.
func bitnetStopAction(application *app.App, w io.Writer) error {
	ctx := context.Background()
	if err := application.BitNet.Stop(ctx); err != nil {
		return err
	}
	fmt.Fprintln(w, "BitNet server stopped.")
	return nil
}

// bitnetStatusAction shows BitNet server status.
func bitnetStatusAction(application *app.App, w io.Writer) error {
	ctx := context.Background()
	status := application.BitNet.Status(ctx)

	fmt.Fprintln(w, "BitNet Status:")
	if !application.Config.BitNet.Enabled {
		fmt.Fprintln(w, "  Status: disabled (enable in config.toml)")
		return nil
	}

	fmt.Fprintf(w, "  Endpoint: %s\n", application.BitNet.BaseURL())
	if status.Running {
		fmt.Fprintln(w, "  Status:   running")
		fmt.Fprintf(w, "  Models:   %d loaded\n", status.ModelCount)
	} else {
		fmt.Fprintln(w, "  Status:   not running")
	}
	return nil
}

// bitnetModelsAction lists models loaded in the BitNet server.
func bitnetModelsAction(application *app.App, w io.Writer) error {
	ctx := context.Background()
	models, err := application.BitNet.ListModels(ctx)
	if err != nil {
		return err
	}

	if len(models) == 0 {
		fmt.Fprintln(w, "No models loaded.")
		return nil
	}

	fmt.Fprintln(w, "BitNet Models:")
	for _, m := range models {
		fmt.Fprintf(w, "  %s (owned by: %s)\n", m.ID, m.OwnedBy)
	}
	return nil
}
