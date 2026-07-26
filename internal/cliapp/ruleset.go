package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerRulesetCommands wires the Ruleset aggregate's CLI surface. Ruleset has no parent
// (plan §2) — every id is always explicit, and `list ruleset` is bare, matching User's own
// no-parent shape. Unlike User/Entity/Object, Ruleset has two additional mutable fields
// beyond name, each getting its own `set` subcommand alongside Character's `set player`.
func registerRulesetCommands(a *App) {
	a.createCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <name> <description> [reference...]",
		Short: "Create a new Ruleset",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", "/rulesets", map[string]any{
				"name":        args[0],
				"description": args[1],
				"references":  args[2:],
			})
		},
	})

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId> <name>",
		Short: "Rename a Ruleset",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/rulesets/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId>",
		Short: "Archive a Ruleset (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/rulesets/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId>",
		Short: "Get a Ruleset by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/rulesets/" + args[0])
		},
	})

	a.listCmd.AddCommand(&cobra.Command{
		Use:   "ruleset",
		Short: "List non-archived Rulesets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/rulesets")
		},
	})

	a.setCmd.AddCommand(&cobra.Command{
		Use:   "description <rulesetId> <description>",
		Short: "Replace a Ruleset's description",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/rulesets/"+args[0]+"/description", map[string]any{"description": args[1]})
		},
	})

	a.setCmd.AddCommand(&cobra.Command{
		Use:   "references <rulesetId> [reference...]",
		Short: "Replace a Ruleset's references list",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/rulesets/"+args[0]+"/references", map[string]any{"references": args[1:]})
		},
	})
}
