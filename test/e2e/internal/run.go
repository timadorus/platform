// Package e2eutil provides the shared building blocks for the Kubernetes end-to-end test
// suite in test/e2e: detecting/provisioning a cluster, installing dependencies via Helm,
// building and loading the platform's own images, and reaching the installed services. Every
// cluster interaction shells out to kubectl/helm/kind/docker — no Kubernetes Go client
// library — matching this repo's existing no-framework, shell-out convention
// (scripts/migrate-up.sh).
package e2eutil

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ProjectDir returns the repository root, regardless of whether the caller's working
// directory is the repo root itself or test/e2e (Ginkgo's default working directory when
// running `go test ./test/e2e/...`).
func ProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("e2eutil: get working directory: %w", err)
	}
	if idx := strings.Index(wd, "/test/e2e"); idx >= 0 {
		return wd[:idx], nil
	}
	return wd, nil
}

// Run executes cmd with its working directory set to the repository root, returning combined
// stdout+stderr. On failure the error wraps that output, so callers don't need to capture it
// separately for diagnostics.
func Run(cmd *exec.Cmd) (string, error) {
	dir, err := ProjectDir()
	if err != nil {
		return "", err
	}
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, string(output))
	}
	return string(output), nil
}

// NonEmptyLines splits output on newlines, dropping empty lines.
func NonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
