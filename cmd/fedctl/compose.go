package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// composeProjectName asks Compose for the resolved project name (from
// compose.yaml's `name:` field, or COMPOSE_PROJECT_NAME if that
// overrides it — Compose's own precedence, not ours to guess at) rather
// than assuming either — a prior version of this code hardcoded a
// derived volume name and silently wrote into a different, unprefixed
// volume than the one the running stack actually uses.
func composeProjectName(ctx context.Context) (string, error) {
	out, err := runDockerComposeOutput(ctx, runComposeArgs(false, "config", "--format", "json"))
	if err != nil {
		return "", fmt.Errorf("docker compose config failed: %w: %s", err, out)
	}
	var cfg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil || cfg.Name == "" {
		return "", fmt.Errorf("could not determine the compose project name from `docker compose config`: %s", out)
	}
	return cfg.Name, nil
}

// resolveComposeVolume returns the actual Docker volume name backing a
// compose.yaml volume declared as `logicalName` (e.g. "ch-user-files"),
// found via Compose's own com.docker.compose.volume/project labels — not
// by reconstructing Compose's project-name-to-volume-name convention by
// hand, which breaks silently under a COMPOSE_PROJECT_NAME override.
// Returns an error if the volume doesn't exist yet (the stack has never
// been brought up).
func resolveComposeVolume(ctx context.Context, logicalName string) (string, error) {
	project, err := composeProjectName(ctx)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "docker", "volume", "ls",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.volume="+logicalName,
		"--format", "{{.Name}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker volume ls failed: %w: %s", err, out.String())
	}
	name := strings.TrimSpace(out.String())
	if name == "" {
		return "", fmt.Errorf("volume %q not found for compose project %q — run `fedctl up` at least once first", logicalName, project)
	}
	// If multiple lines came back (shouldn't happen given both label
	// filters, but be defensive), use the first.
	return strings.SplitN(name, "\n", 2)[0], nil
}
