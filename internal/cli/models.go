package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/spf13/cobra"
)

// ModelsCmd creates the `axiom models` parent command with subcommands.
func ModelsCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage model registry",
	}

	cmd.AddCommand(modelsRefreshCmd(verbose))
	cmd.AddCommand(modelsListCmd(verbose))
	cmd.AddCommand(modelsInfoCmd(verbose))
	return cmd
}

func modelsRefreshCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Update model registry from OpenRouter + local",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return modelsRefreshAction(application, cmd.OutOrStdout())
		},
	}
}

func modelsListCmd(verbose *bool) *cobra.Command {
	var tier, family string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered models",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return modelsListAction(application, tier, family, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&tier, "tier", "", "filter by tier (local, cheap, standard, premium)")
	cmd.Flags().StringVar(&family, "family", "", "filter by model family")
	return cmd
}

func modelsInfoCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "info <model-id>",
		Short: "Show model details + historical performance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return modelsInfoAction(application, args[0], cmd.OutOrStdout())
		},
	}
}

// modelsRefreshAction refreshes the model registry from all available sources.
func modelsRefreshAction(application *app.App, w io.Writer) error {
	// Always refresh shipped models
	if err := application.Registry.RefreshShipped(); err != nil {
		fmt.Fprintf(w, "Warning: failed to refresh shipped models: %v\n", err)
	}

	// Try OpenRouter if configured
	if application.Config.Inference.OpenRouterAPIKey != "" {
		ctx := context.Background()
		if err := application.Registry.RefreshOpenRouter(ctx, application.Config.Inference.OpenRouterBase); err != nil {
			fmt.Fprintf(w, "Warning: failed to refresh OpenRouter models: %v\n", err)
		}
	}

	// Try BitNet if enabled
	if application.Config.BitNet.Enabled {
		ctx := context.Background()
		baseURL := fmt.Sprintf("http://%s:%d", application.Config.BitNet.Host, application.Config.BitNet.Port)
		if err := application.Registry.RefreshBitNet(ctx, baseURL); err != nil {
			fmt.Fprintf(w, "Warning: failed to refresh BitNet models: %v\n", err)
		}
	}

	fmt.Fprintln(w, "Model registry refreshed.")
	return nil
}

// modelsListAction lists models with optional tier and family filters.
func modelsListAction(application *app.App, tier, family string, w io.Writer) error {
	models, err := application.Registry.List(tier, family)
	if err != nil {
		return fmt.Errorf("listing models: %w", err)
	}

	if len(models) == 0 {
		fmt.Fprintln(w, "No models found.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFamily\tTier\tContext\tSource")
	fmt.Fprintln(tw, "──\t──────\t────\t───────\t──────")
	for _, m := range models {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			m.ID, m.Family, m.Tier, m.ContextWindow, m.Source)
	}
	tw.Flush()
	return nil
}

// modelsInfoAction shows detailed information about a single model.
func modelsInfoAction(application *app.App, modelID string, w io.Writer) error {
	model, err := application.Registry.Get(modelID)
	if err != nil {
		return fmt.Errorf("model %q not found: %w", modelID, err)
	}

	fmt.Fprintf(w, "Model: %s\n", model.ID)
	fmt.Fprintf(w, "  Family:         %s\n", model.Family)
	fmt.Fprintf(w, "  Source:         %s\n", model.Source)
	fmt.Fprintf(w, "  Tier:           %s\n", model.Tier)
	fmt.Fprintf(w, "  Context Window: %d\n", model.ContextWindow)
	fmt.Fprintf(w, "  Max Output:     %d\n", model.MaxOutput)
	fmt.Fprintf(w, "  Prompt Cost:    $%.4f / 1M tokens\n", model.PromptPerMillion)
	fmt.Fprintf(w, "  Completion:     $%.4f / 1M tokens\n", model.CompletionPerMillion)
	fmt.Fprintf(w, "  Tools:          %v\n", model.SupportsTools)
	fmt.Fprintf(w, "  Vision:         %v\n", model.SupportsVision)
	fmt.Fprintf(w, "  Grammar:        %v\n", model.SupportsGrammar)

	if len(model.Strengths) > 0 {
		fmt.Fprintf(w, "  Strengths:      %s\n", strings.Join(model.Strengths, ", "))
	}
	if len(model.Weaknesses) > 0 {
		fmt.Fprintf(w, "  Weaknesses:     %s\n", strings.Join(model.Weaknesses, ", "))
	}
	if len(model.RecommendedFor) > 0 {
		fmt.Fprintf(w, "  Recommended:    %s\n", strings.Join(model.RecommendedFor, ", "))
	}
	if len(model.NotRecommendedFor) > 0 {
		fmt.Fprintf(w, "  Not Recommended: %s\n", strings.Join(model.NotRecommendedFor, ", "))
	}

	return nil
}
