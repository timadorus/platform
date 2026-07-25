package cliapp

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// registerAuthAndConfigCommands adds `auth` (token management) and `config` (tracked
// context + API URLs) as top-level commands — they're config/context management, not
// resource verbs, so they sit alongside the verb tree rather than under it.
func registerAuthAndConfigCommands(a *App) {
	authCmd := &cobra.Command{Use: "auth", Short: "Manage the bearer token used for every API request"}
	authCmd.AddCommand(&cobra.Command{
		Use:   "set-token <jwt>",
		Short: "Persist a bearer token obtained from your identity provider (there is no login flow — see docs/PLAN.md §10/§14)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			cfg.Token = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "Token saved.")
			return nil
		},
	})
	a.Root.AddCommand(authCmd)

	configCmd := &cobra.Command{Use: "config", Short: "Manage tracked context (user/universe/campaign) and API endpoints"}
	configCmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Print the current config (token redacted)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}
				return printJSON(cfg.Redacted())
			},
		},
		setIDCommand("set-user", "Set the tracked authenticated user id (informational — never auto-fills a command argument, see docs/PLAN.md §14)", func(cfg *Config, id string) { cfg.CurrentUserID = id }),
		setIDCommand("set-universe", "Set the current universe id (defaults the parent universe id on create/add/delete/list commands)", func(cfg *Config, id string) { cfg.CurrentUniverseID = id }),
		setIDCommand("set-campaign", "Set the current campaign id (defaults the parent campaign id on create/add/delete/list commands)", func(cfg *Config, id string) { cfg.CurrentCampaignID = id }),
		&cobra.Command{
			Use:   "set-command-api-url <url>",
			Short: "Set the command-api base URL (default http://localhost:8081)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}
				cfg.CommandAPIURL = args[0]
				return cfg.Save()
			},
		},
		&cobra.Command{
			Use:   "set-query-api-url <url>",
			Short: "Set the query-api base URL (default http://localhost:8082)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := LoadConfig()
				if err != nil {
					return err
				}
				cfg.QueryAPIURL = args[0]
				return cfg.Save()
			},
		},
	)
	a.Root.AddCommand(configCmd)
}

// setIDCommand builds a `config set-<name> <id>` command that validates id as a UUID before
// persisting it via set.
func setIDCommand(use, short string, set func(cfg *Config, id string)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("%q is not a valid id: %w", args[0], err)
			}
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			set(&cfg, args[0])
			return cfg.Save()
		},
	}
}
