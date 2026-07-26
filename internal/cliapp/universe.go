package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

const universeFlag = "universe"

func addUniverseFlag(cmd *cobra.Command) {
	cmd.Flags().String(universeFlag, "", "Universe id to use instead of the current universe (see: timadorusctl config set-universe)")
}

// registerUniverseCommands wires the Universe aggregate's CLI surface, including the
// Creators collection (add/delete/list) whose parent universe id follows the narrow
// id-defaulting rule (plan §14): --universe flag, else the tracked current universe, else an
// interactive prompt.
func registerUniverseCommands(a *App) {
	a.createCmd.AddCommand(&cobra.Command{
		Use:   "universe <name> <creatorUserId>...",
		Short: "Create a new Universe",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", "/universes", map[string]any{
				"name":           args[0],
				"creatorUserIds": args[1:],
			})
		},
	})

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "universe <universeId> <name>",
		Short: "Rename a Universe",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/universes/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "universe <universeId>",
		Short: "Archive a Universe (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/universes/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "universe <universeId>",
		Short: "Get a Universe by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/universes/" + args[0])
		},
	})

	addCreatorCmd := &cobra.Command{
		Use:   "creator <userId>",
		Short: "Add a Creator to a Universe (idempotent)",
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
			return client.Command("POST", fmt.Sprintf("/universes/%s/creators/%s", universeID, args[0]), nil)
		},
	}
	addUniverseFlag(addCreatorCmd)
	a.addCmd.AddCommand(addCreatorCmd)

	deleteCreatorCmd := &cobra.Command{
		Use:   "creator <userId>",
		Short: "Remove a Creator from a Universe; rejected if it's the last one",
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
			return client.Command("DELETE", fmt.Sprintf("/universes/%s/creators/%s", universeID, args[0]), nil)
		},
	}
	addUniverseFlag(deleteCreatorCmd)
	a.deleteCmd.AddCommand(deleteCreatorCmd)

	listCreatorCmd := &cobra.Command{
		Use:   "creator",
		Short: "List a Universe's Creators",
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
			return client.Query("/universes/" + universeID + "/creators")
		},
	}
	addUniverseFlag(listCreatorCmd)
	a.listCmd.AddCommand(listCreatorCmd)

	a.listCmd.AddCommand(&cobra.Command{
		Use:   "universe",
		Short: "List non-archived Universes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/universes")
		},
	})
}
