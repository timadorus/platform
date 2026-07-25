package cliapp

import (
	"encoding/json"
	"fmt"
)

// printJSON marshals v indented and prints it to stdout — used by commands (like `config
// show`) that don't go through an HTTP round trip but still need to honor the "stdout is
// always JSON" contract (see client.go's handleResponse doc comment).
func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("cliapp: marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
