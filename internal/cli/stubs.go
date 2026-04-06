package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/openaxiom/axiom/internal/api"
	"github.com/openaxiom/axiom/internal/state"
	"github.com/spf13/cobra"
)

// stubMessage returns a consistent message for commands that are planned
// but not yet implemented, referencing the target phase.
func stubMessage(command, phase string) string {
	return fmt.Sprintf("%s is not yet implemented (planned for %s).", command, phase)
}

// APICmd creates the `axiom api` command with subcommands.
// Per Architecture Section 24.2, the API server exposes REST + WebSocket.
func APICmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage API server",
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the REST + WebSocket API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Phase 16: API server start requires an initialized Axiom project (%v).\n", err)
				return nil
			}
			defer application.Close()

			cfg := api.ServerConfig{
				Port:         application.Config.API.Port,
				RateLimitRPM: application.Config.API.RateLimitRPM,
				AllowedIPs:   application.Config.API.AllowedIPs,
			}

			srv := api.NewServer(application.Engine, application.DB, cfg)
			fmt.Fprintf(cmd.OutOrStdout(), "Starting API server on port %d...\n", cfg.Port)

			ctx := context.Background()
			return srv.Start(ctx)
		},
	}
	cmd.AddCommand(startCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "Phase 16: API server stop via SIGINT to the running server process.")
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
			application, err := openApp(verbose)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Phase 16: API token generation requires an initialized Axiom project (%v).\n", err)
				return nil
			}
			defer application.Close()

			scope, _ := cmd.Flags().GetString("scope")
			if scope == "" {
				scope = api.ScopeFullControl
			}
			if scope != api.ScopeReadOnly && scope != api.ScopeFullControl {
				return fmt.Errorf("scope must be %q or %q", api.ScopeReadOnly, api.ScopeFullControl)
			}

			expiresStr, _ := cmd.Flags().GetString("expires")
			expiresDur := 24 * time.Hour
			if expiresStr != "" {
				d, err := time.ParseDuration(expiresStr)
				if err != nil {
					return fmt.Errorf("invalid --expires duration: %w", err)
				}
				expiresDur = d
			}

			rawToken, tokenID, err := api.GenerateToken()
			if err != nil {
				return fmt.Errorf("generating token: %w", err)
			}

			if err := application.DB.CreateAPIToken(&state.APIToken{
				ID:          tokenID,
				TokenHash:   api.HashToken(rawToken),
				TokenPrefix: api.TokenPrefix(rawToken),
				Scope:       scope,
				ExpiresAt:   time.Now().Add(expiresDur),
			}); err != nil {
				return fmt.Errorf("storing token: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), rawToken)
			fmt.Fprintf(cmd.OutOrStdout(), "Token ID: %s\n", tokenID)
			fmt.Fprintf(cmd.OutOrStdout(), "Scope: %s\n", scope)
			fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", time.Now().Add(expiresDur).Format(time.RFC3339))
			return nil
		},
	}
	generateTokenCmd.Flags().String("scope", "full-control", "token scope (read-only or full-control)")
	generateTokenCmd.Flags().String("expires", "24h", "token expiration duration")
	tokenCmd.AddCommand(generateTokenCmd)

	tokenCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List active API tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Phase 16: API token listing requires an initialized Axiom project (%v).\n", err)
				return nil
			}
			defer application.Close()

			tokens, err := application.DB.ListAPITokens()
			if err != nil {
				return fmt.Errorf("listing tokens: %w", err)
			}

			if len(tokens) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No API tokens found.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-18s %-14s %-25s %-10s\n",
				"ID", "PREFIX", "SCOPE", "EXPIRES", "STATUS")
			for _, t := range tokens {
				status := "active"
				if t.RevokedAt != nil {
					status = "revoked"
				} else if time.Now().After(t.ExpiresAt) {
					status = "expired"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-18s %-14s %-25s %-10s\n",
					t.ID, t.TokenPrefix, t.Scope,
					t.ExpiresAt.Format(time.RFC3339), status)
			}
			return nil
		},
	})

	tokenCmd.AddCommand(&cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Revoke a specific API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Phase 16: API token revocation requires an initialized Axiom project (%v).\n", err)
				return nil
			}
			defer application.Close()

			if err := application.DB.RevokeAPIToken(args[0]); err != nil {
				return fmt.Errorf("revoking token: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Token %s revoked.\n", args[0])
			return nil
		},
	})

	cmd.AddCommand(tokenCmd)
	return cmd
}

// TunnelCmd creates the `axiom tunnel` command with subcommands.
// Per Architecture Section 24.4, supports Cloudflare Tunnel for remote Claw access.
func TunnelCmd(verbose *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage Cloudflare Tunnel for remote access",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start Cloudflare Tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(verbose)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Phase 16: tunnel start requires an initialized Axiom project (%v).\n", err)
				return nil
			}
			defer application.Close()

			addr := fmt.Sprintf("localhost:%d", application.Config.API.Port)
			tun := api.NewTunnel(addr)
			if err := tun.Start(); err != nil {
				return fmt.Errorf("starting tunnel: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Tunnel started for %s\n", addr)
			if url := tun.URL(); url != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Public URL: %s\n", url)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "Phase 16: tunnel stop via SIGINT to the running tunnel process.")
			return nil
		},
	})

	return cmd
}
