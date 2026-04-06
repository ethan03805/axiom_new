package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/openaxiom/axiom/internal/app"
	"github.com/openaxiom/axiom/internal/index"
	"github.com/spf13/cobra"
)

// IndexCmd creates the `axiom index` parent command with subcommands.
func IndexCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage semantic index",
	}

	cmd.AddCommand(indexRefreshCmd(verbose))
	cmd.AddCommand(indexQueryCmd(verbose))
	return cmd
}

func indexRefreshCmd(verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force full re-index of the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return indexRefreshAction(application, cmd.OutOrStdout())
		},
	}
}

func indexQueryCmd(verbose *bool) *cobra.Command {
	var queryType, name, pkg string

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query the semantic index",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				return err
			}
			defer application.Close()

			return indexQueryAction(application, queryType, name, pkg, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&queryType, "type", "", "query type: lookup_symbol, reverse_dependencies, list_exports, find_implementations, module_graph")
	cmd.Flags().StringVar(&name, "name", "", "symbol name (for lookup_symbol, reverse_dependencies, find_implementations)")
	cmd.Flags().StringVar(&pkg, "package", "", "package path (for list_exports)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// indexRefreshAction performs a full project re-index.
func indexRefreshAction(application *app.App, w io.Writer) error {
	ctx := context.Background()
	indexer := index.NewIndexer(application.DB, application.Log)

	if err := indexer.Index(ctx, application.ProjectRoot); err != nil {
		return fmt.Errorf("indexing project: %w", err)
	}

	fmt.Fprintln(w, "Index refreshed.")
	return nil
}

// indexQueryAction performs a semantic index query of the given type.
func indexQueryAction(application *app.App, queryType, name, pkg string, w io.Writer) error {
	ctx := context.Background()
	indexer := index.NewIndexer(application.DB, application.Log)

	switch queryType {
	case "lookup_symbol":
		if name == "" {
			return fmt.Errorf("--name is required for lookup_symbol")
		}
		results, err := indexer.LookupSymbol(ctx, name, "")
		if err != nil {
			return fmt.Errorf("lookup_symbol: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(w, "No symbols found.")
			return nil
		}
		for _, r := range results {
			fmt.Fprintf(w, "%s:%d  %s %s\n", r.FilePath, r.Line, r.Kind, r.Name)
			if r.Signature != nil && *r.Signature != "" {
				fmt.Fprintf(w, "  %s\n", *r.Signature)
			}
		}

	case "reverse_dependencies":
		if name == "" {
			return fmt.Errorf("--name is required for reverse_dependencies")
		}
		results, err := indexer.ReverseDependencies(ctx, name)
		if err != nil {
			return fmt.Errorf("reverse_dependencies: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(w, "No references found.")
			return nil
		}
		for _, r := range results {
			fmt.Fprintf(w, "%s:%d  %s (%s)\n", r.FilePath, r.Line, r.SymbolName, r.UsageType)
		}

	case "list_exports":
		if pkg == "" {
			return fmt.Errorf("--package is required for list_exports")
		}
		results, err := indexer.ListExports(ctx, pkg)
		if err != nil {
			return fmt.Errorf("list_exports: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(w, "No exports found.")
			return nil
		}
		for _, r := range results {
			sig := ""
			if r.Signature != nil {
				sig = *r.Signature
			}
			fmt.Fprintf(w, "%s  %s  %s\n", r.Kind, r.Name, sig)
		}

	case "find_implementations":
		if name == "" {
			return fmt.Errorf("--name is required for find_implementations")
		}
		results, err := indexer.FindImplementations(ctx, name)
		if err != nil {
			return fmt.Errorf("find_implementations: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(w, "No implementations found.")
			return nil
		}
		for _, r := range results {
			fmt.Fprintf(w, "%s:%d  %s\n", r.FilePath, r.Line, r.SymbolName)
		}

	case "module_graph":
		result, err := indexer.ModuleGraph(ctx, application.ProjectRoot)
		if err != nil {
			return fmt.Errorf("module_graph: %w", err)
		}
		if result == nil || len(result.Packages) == 0 {
			fmt.Fprintln(w, "No packages found.")
			return nil
		}
		fmt.Fprintln(w, "Packages:")
		for _, p := range result.Packages {
			fmt.Fprintf(w, "  %s (%s)\n", p.Path, p.Dir)
		}
		if len(result.Edges) > 0 {
			fmt.Fprintln(w, "Dependencies:")
			for _, e := range result.Edges {
				fmt.Fprintf(w, "  %s -> %s\n", e.From, e.To)
			}
		}

	default:
		return fmt.Errorf("unknown query type %q (valid: lookup_symbol, reverse_dependencies, list_exports, find_implementations, module_graph)", queryType)
	}

	return nil
}
