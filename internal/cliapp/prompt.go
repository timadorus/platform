package cliapp

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

// promptForID interactively asks for an id on stderr/stdin (never stdout — see
// handleResponse's doc comment) and parses it as a UUID.
func promptForID(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("cliapp: read id: %w", err)
	}
	id := strings.TrimSpace(line)
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("cliapp: %q is not a valid id: %w", id, err)
	}
	return id, nil
}

// resolveUniverseID implements the narrow id-defaulting rule for the *parent* universe id
// (plan §14): explicit flag wins, then the tracked current universe, then an interactive
// prompt — whose answer is persisted as the new current universe so it isn't asked again.
func resolveUniverseID(cfg *Config, flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if cfg.CurrentUniverseID != "" {
		return cfg.CurrentUniverseID, nil
	}
	id, err := promptForID("No current universe set. Enter universe id")
	if err != nil {
		return "", err
	}
	cfg.CurrentUniverseID = id
	if err := cfg.Save(); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "Saved as current universe.")
	return id, nil
}

// resolveCampaignID is resolveUniverseID's counterpart for the parent campaign id.
func resolveCampaignID(cfg *Config, flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if cfg.CurrentCampaignID != "" {
		return cfg.CurrentCampaignID, nil
	}
	id, err := promptForID("No current campaign set. Enter campaign id")
	if err != nil {
		return "", err
	}
	cfg.CurrentCampaignID = id
	if err := cfg.Save(); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "Saved as current campaign.")
	return id, nil
}
