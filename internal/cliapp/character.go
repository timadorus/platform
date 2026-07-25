package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerCharacterCommands wires the Character aggregate's CLI surface, including `set
// player` (PUT reassign, no "unset" — matches the domain's own invariant) and the
// characters-under-a-campaign list. Character creation is the platform's one cross-aggregate
// creation flow (plan §4.4): the command-api response includes both the new characterId and
// the atomically-created entityId, and the CLI just prints that response as-is.
func registerCharacterCommands(a *App) {
	createCharacterCmd := &cobra.Command{
		Use:   "character <name> <playerUserId>",
		Short: "Create a new Character under a Campaign, atomically creating its paired Entity",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(campaignFlag)
			campaignID, err := resolveCampaignID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Command("POST", "/campaigns/"+campaignID+"/characters", map[string]any{
				"name":         args[0],
				"playerUserId": args[1],
			})
		},
	}
	addCampaignFlag(createCharacterCmd)
	a.createCmd.AddCommand(createCharacterCmd)

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "character <characterId> <name>",
		Short: "Rename a Character",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/characters/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "character <characterId>",
		Short: "Archive a Character (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/characters/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "character <characterId>",
		Short: "Get a Character by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/characters/" + args[0])
		},
	})

	a.setCmd.AddCommand(&cobra.Command{
		Use:   "player <characterId> <userId>",
		Short: `Reassign a Character's Player; there is no "unset" operation.`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/characters/"+args[0]+"/player", map[string]any{"userId": args[1]})
		},
	})

	listCharacterCmd := &cobra.Command{
		Use:   "character",
		Short: "List non-archived Characters under a Campaign",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(campaignFlag)
			campaignID, err := resolveCampaignID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Query("/campaigns/" + campaignID + "/characters")
		},
	}
	addCampaignFlag(listCharacterCmd)
	a.listCmd.AddCommand(listCharacterCmd)
}
