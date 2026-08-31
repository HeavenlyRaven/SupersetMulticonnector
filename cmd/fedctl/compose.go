package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// runComposeArgs builds the `docker compose` argv, adding the dev overlay
// and profile when dev is true. Compose Specification format, no
// `version:` key, per spec section 4 — that's enforced in compose.yaml
// itself, not here.
func runComposeArgs(dev bool, rest ...string) []string {
	args := []string{"compose", "-f", "compose.yaml"}
	if dev {
		args = append(args, "-f", "compose.dev.yaml", "--profile", "dev")
	}
	return append(args, rest...)
}

// runDockerCompose execs `docker <args...>` directly — argv array, no
// shell — streaming stdout/stderr to this process's own so the operator
// sees normal docker compose output.
func runDockerCompose(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runDockerComposeOutput is the same, but captures stdout for parsing
// (e.g. `compose ps --format json`) instead of streaming it.
func runDockerComposeOutput(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func dockerAvailable(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("the `docker` CLI was not found on PATH: %w", err)
	}
	return nil
}
