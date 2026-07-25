package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerUserCommands wires the User aggregate's CLI surface. User has no "current" context
// of its own and no parent — every id is always explicit (plan §14).
func registerUserCommands(a *App) {
	a.createCmd.AddCommand(&cobra.Command{
		Use:   "user <name>",
		Short: "Create a new User (POST /users)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", "/users", map[string]any{"name": args[0]})
		},
	})

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "user <userId> <name>",
		Short: "Rename a User (PATCH /users/{userId})",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/users/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "user <userId>",
		Short: "Archive a User, idempotent (POST /users/{userId}/archive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/users/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "user <userId>",
		Short: "Get a User by id (GET /users/{userId})",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/users/" + args[0])
		},
	})
}
