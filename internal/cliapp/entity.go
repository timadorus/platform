package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerEntityCommands wires the Entity aggregate's CLI surface: structurally identical to
// Object's (both are parented directly by a Universe with no collection invariant), and
// nearly identical to Universe's Create/Rename/Archive/Get shape — see docs/PLAN.md §14 for
// why every new parented aggregate type follows this exact template.
func registerEntityCommands(a *App) {
	createEntityCmd := &cobra.Command{
		Use:   "entity <name>",
		Short: "Create a new Entity under a Universe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(universeFlag)
			universeID, err := resolveUniverseID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Command("POST", "/universes/"+universeID+"/entities", map[string]any{"name": args[0]})
		},
	}
	addUniverseFlag(createEntityCmd)
	a.createCmd.AddCommand(createEntityCmd)

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "entity <entityId> <name>",
		Short: "Rename an Entity",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/entities/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "entity <entityId>",
		Short: "Archive an Entity (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/entities/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "entity <entityId>",
		Short: "Get an Entity by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/entities/" + args[0])
		},
	})

	listEntityCmd := &cobra.Command{
		Use:   "entity",
		Short: "List non-archived Entities under a Universe",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(universeFlag)
			universeID, err := resolveUniverseID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Query("/universes/" + universeID + "/entities")
		},
	}
	addUniverseFlag(listEntityCmd)
	a.listCmd.AddCommand(listEntityCmd)
}
