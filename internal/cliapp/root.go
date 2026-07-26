package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// App wires the verb-first command tree (see docs/PLAN.md §14): each top-level command is a
// verb (create, rename, archive, add, delete, set, get, list); each resource file
// (user.go, universe.go, ...) attaches its subcommands to the relevant verb(s).
type App struct {
	Root *cobra.Command

	createCmd  *cobra.Command
	renameCmd  *cobra.Command
	archiveCmd *cobra.Command
	addCmd     *cobra.Command
	deleteCmd  *cobra.Command
	setCmd     *cobra.Command
	getCmd     *cobra.Command
	listCmd    *cobra.Command
}

// version is overridable via -ldflags "-X github.com/timadorus/platform/internal/cliapp.version=...".
var version = "dev"

func newApp() *App {
	root := &cobra.Command{
		Use:           "timadorusctl",
		Short:         "Command-line client for the Timadorus CQRS/ES platform's command and query APIs.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	a := &App{
		Root: root,
		createCmd: &cobra.Command{
			Use:   "create",
			Short: "Create a new aggregate (POST .../<resource>)",
		},
		renameCmd: &cobra.Command{
			Use:   "rename",
			Short: "Rename an existing aggregate (PATCH .../<resource>/{id})",
		},
		archiveCmd: &cobra.Command{
			Use:   "archive",
			Short: "Archive an aggregate — soft, idempotent, never a hard delete (POST .../archive)",
		},
		addCmd: &cobra.Command{
			Use:   "add",
			Short: "Add a user to a collection relationship (creator/gamemaster)",
		},
		deleteCmd: &cobra.Command{
			Use:   "delete",
			Short: "Remove a user from a collection relationship (creator/gamemaster)",
		},
		setCmd: &cobra.Command{
			Use:   "set",
			Short: "Reassign a reference (player) or mutate a single-value field (description, references)",
		},
		getCmd: &cobra.Command{
			Use:   "get",
			Short: "Fetch a single aggregate/projection by id",
		},
		listCmd: &cobra.Command{
			Use:   "list",
			Short: "List a collection, scoped to a parent or bare for User/Universe/Ruleset",
		},
	}

	root.AddCommand(a.createCmd, a.renameCmd, a.archiveCmd, a.addCmd, a.deleteCmd, a.setCmd, a.getCmd, a.listCmd)
	return a
}

// client loads the persisted config and builds a Client from it. Returns the config too
// since resolveUniverseID/resolveCampaignID may mutate and persist it.
func (a *App) client() (*Client, Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, Config{}, err
	}
	return NewClient(cfg), cfg, nil
}

// Execute builds the full command tree and runs it. This is the only exported entry point —
// cmd/timadorusctl/main.go just calls this.
func Execute() error {
	app := newApp()
	registerAuthAndConfigCommands(app)
	registerUserCommands(app)
	registerUniverseCommands(app)
	registerCampaignCommands(app)
	registerEntityCommands(app)
	registerObjectCommands(app)
	registerCharacterCommands(app)
	registerRulesetCommands(app)

	if err := app.Root.Execute(); err != nil {
		return fmt.Errorf("timadorusctl: %w", err)
	}
	return nil
}
