// Command timadorusctl is a cobra-based CLI client for the Timadorus platform's
// command-api/query-api. See docs/PLAN.md §14 for the command-naming and id-defaulting
// conventions.
package main

import (
	"fmt"
	"os"

	"github.com/timadorus/platform/internal/cliapp"
)

func main() {
	if err := cliapp.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
