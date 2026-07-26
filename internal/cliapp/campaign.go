package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

const campaignFlag = "campaign"

func addCampaignFlag(cmd *cobra.Command) {
	cmd.Flags().String(campaignFlag, "", "Campaign id to use instead of the current campaign (see: timadorusctl config set-campaign)")
}

// registerCampaignCommands wires the Campaign aggregate's CLI surface, including the
// Gamemasters collection (add/delete/list) and the campaigns-under-a-universe list, per the
// narrow id-defaulting rule (plan §14).
func registerCampaignCommands(a *App) {
	createCampaignCmd := &cobra.Command{
		Use:   "campaign <name> <rulesetId> <gamemasterUserId>...",
		Short: "Create a new Campaign under a Universe",
		Args:  cobra.MinimumNArgs(3),
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
			return client.Command("POST", "/universes/"+universeID+"/campaigns", map[string]any{
				"name":              args[0],
				"rulesetId":         args[1],
				"gamemasterUserIds": args[2:],
			})
		},
	}
	addUniverseFlag(createCampaignCmd)
	a.createCmd.AddCommand(createCampaignCmd)

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "campaign <campaignId> <name>",
		Short: "Rename a Campaign",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/campaigns/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "campaign <campaignId>",
		Short: "Archive a Campaign, idempotent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/campaigns/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "campaign <campaignId>",
		Short: "Get a Campaign by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/campaigns/" + args[0])
		},
	})

	addGamemasterCmd := &cobra.Command{
		Use:   "gamemaster <userId>",
		Short: "Add a Gamemaster to a Campaign, idempotent",
		Args:  cobra.ExactArgs(1),
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
			return client.Command("POST", fmt.Sprintf("/campaigns/%s/gamemasters/%s", campaignID, args[0]), nil)
		},
	}
	addCampaignFlag(addGamemasterCmd)
	a.addCmd.AddCommand(addGamemasterCmd)

	// timadorusctl delete gamemaster <userId>
	deleteGamemasterCmd := &cobra.Command{
		Use:   "gamemaster <userId>",
		Short: "Remove a Gamemaster from a Campaign; rejected if it's the last one",
		Args:  cobra.ExactArgs(1),
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
			return client.Command("DELETE", fmt.Sprintf("/campaigns/%s/gamemasters/%s", campaignID, args[0]), nil)
		},
	}
	addCampaignFlag(deleteGamemasterCmd)
	a.deleteCmd.AddCommand(deleteGamemasterCmd)

	listGamemasterCmd := &cobra.Command{
		Use:   "gamemaster",
		Short: "List a Campaign's Gamemasters",
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
			return client.Query("/campaigns/" + campaignID + "/gamemasters")
		},
	}
	addCampaignFlag(listGamemasterCmd)
	a.listCmd.AddCommand(listGamemasterCmd)

	listCampaignCmd := &cobra.Command{
		Use:   "campaign",
		Short: "List non-archived Campaigns under a Universe",
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
			return client.Query("/universes/" + universeID + "/campaigns")
		},
	}
	addUniverseFlag(listCampaignCmd)
	a.listCmd.AddCommand(listCampaignCmd)
}
