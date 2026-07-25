package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerObjectCommands wires the Object aggregate's CLI surface — a copy-adapt of
// entity.go's, matching how Object itself is a near-copy of Entity at the domain layer (plan
// §12, Phase 4).
func registerObjectCommands(a *App) {
	createObjectCmd := &cobra.Command{
		Use:   "object <name>",
		Short: "Create a new Object under a Universe",
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
			return client.Command("POST", "/universes/"+universeID+"/objects", map[string]any{"name": args[0]})
		},
	}
	addUniverseFlag(createObjectCmd)
	a.createCmd.AddCommand(createObjectCmd)

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "object <objectId> <name>",
		Short: "Rename an Object",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/objects/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "object <objectId>",
		Short: "Archive an Object (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/objects/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "object <objectId>",
		Short: "Get an Object by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/objects/" + args[0])
		},
	})

	listObjectCmd := &cobra.Command{
		Use:   "object",
		Short: "List non-archived Objects under a Universe",
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
			return client.Query("/universes/" + universeID + "/objects")
		},
	}
	addUniverseFlag(listObjectCmd)
	a.listCmd.AddCommand(listObjectCmd)
}
