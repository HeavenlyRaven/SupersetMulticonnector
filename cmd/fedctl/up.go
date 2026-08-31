package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/config"
)

func newUpCmd() *cobra.Command {
	var dev bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Bring the stack up and wait for both containers to become healthy",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runUp(cmd.Context(), dev, timeout); err != nil {
				printCLIError(err)
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "also start compose.dev.yaml's sample source databases")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for both containers to become healthy")
	return cmd
}

// runUp is `up`'s actual implementation, factored out so `fedctl install`
// can call straight into it after writing .env instead of shelling out to
// itself. Every error is returned already wrapped with enough context to
// print directly — callers should not need to add their own prefix.
func runUp(ctx context.Context, dev bool, timeout time.Duration) error {
	if err := dockerAvailable(ctx); err != nil {
		return err
	}
	// Fail fast and clearly, before starting any container, if the
	// metadata database isn't configured correctly — spec section 1.
	preApp, err := config.LoadApp()
	if err != nil {
		return err
	}
	if err := config.ValidateMetadataURI(preApp.SupersetMetadataURI); err != nil {
		return err
	}

	upArgs := runComposeArgs(dev, "up", "-d", "--build")
	if err := runDockerCompose(ctx, upArgs); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	fmt.Println("waiting for services to become healthy...")
	if err := waitHealthy(ctx, dev, timeout); err != nil {
		return err
	}
	fmt.Println("all services healthy")

	app, err := config.LoadApp()
	if err != nil {
		return err
	}

	// The stack is exactly two containers (spec section 1) — no third
	// "init" container — so the one-time Superset bootstrap (metadata
	// schema migration, admin user, default roles) runs here via
	// `docker compose exec`, not a separate service.
	if err := bootstrapSuperset(ctx, dev, app); err != nil {
		return fmt.Errorf("stack is healthy, but Superset bootstrap failed: %w", err)
	}

	if err := registerClickHouseConnection(ctx, app); err != nil {
		// Non-fatal to the caller's exit code decision, but still an
		// error: the stack is up and healthy even if the idempotent
		// Superset registration step hits a transient error (e.g.
		// Superset still finishing first-boot init).
		return fmt.Errorf("stack is healthy, but registering the ClickHouse connection in Superset failed: %w", err)
	}
	fmt.Println("ClickHouse connection registered in Superset")
	return nil
}

type composePsRow struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// waitHealthy polls `docker compose ps` — which reflects each container's
// own HEALTHCHECK directive, i.e. the same `fedctl check <target>` run
// from inside the container where service hostnames actually resolve —
// rather than fedctl (running on the host) trying to dial the internal
// docker network directly.
func waitHealthy(ctx context.Context, dev bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastStatus string
	for {
		rows, err := composePS(ctx, dev)
		if err == nil {
			allHealthy := len(rows) > 0
			var statuses []string
			for _, r := range rows {
				h := r.Health
				if h == "" {
					h = r.State // service defines no healthcheck; fall back to running-state
				}
				statuses = append(statuses, r.Service+"="+h)
				if h != "healthy" && h != "running" {
					allHealthy = false
				}
			}
			lastStatus = strings.Join(statuses, ", ")
			if allHealthy {
				return nil
			}
		} else {
			lastStatus = err.Error()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for services to become healthy (last status: %s)", timeout, lastStatus)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func composePS(ctx context.Context, dev bool) ([]composePsRow, error) {
	out, err := runDockerComposeOutput(ctx, runComposeArgs(dev, "ps", "--format", "json"))
	if err != nil {
		return nil, fmt.Errorf("docker compose ps failed: %w: %s", err, out)
	}
	var rows []composePsRow
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row composePsRow
		if err := json.Unmarshal([]byte(line), &row); err == nil && row.Service != "" {
			rows = append(rows, row)
			continue
		}
		// Some compose versions print one JSON array instead of JSONL.
		var arr []composePsRow
		if err := json.Unmarshal([]byte(line), &arr); err == nil {
			rows = append(rows, arr...)
		}
	}
	return rows, nil
}
