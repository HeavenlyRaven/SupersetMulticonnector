// Package health implements the healthcheck targets fedctl exposes via
// `fedctl check <target>`. This is what every container HEALTHCHECK
// directive invokes directly (never curl/wget/nc, which vary by base
// image) and what `fedctl up` polls while waiting for the stack to become
// healthy.
package health

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/acme/superset-federation/internal/config"
	"github.com/acme/superset-federation/internal/hub"
	"github.com/acme/superset-federation/internal/jsonio"
)

// Targets fedctl recognizes.
const (
	TargetClickHouse = "clickhouse"
	TargetSuperset   = "superset"
)

var ValidTargets = []string{TargetClickHouse, TargetSuperset}

// Check runs the healthcheck for target and returns a non-nil error if
// unhealthy.
func Check(ctx context.Context, target string, cfg config.App) error {
	switch target {
	case TargetClickHouse:
		return checkClickHouse(ctx, cfg)
	case TargetSuperset:
		return checkSuperset(ctx, cfg)
	default:
		return jsonio.NewError(jsonio.CodeInvalidRequest,
			"unknown healthcheck target: "+target,
			"valid targets: "+strings.Join(ValidTargets, ", "))
	}
}

func checkClickHouse(ctx context.Context, cfg config.App) error {
	if cfg.FedAdminPassword == "" {
		return jsonio.NewError(jsonio.CodeInternal, "FEDCTL_ADMIN_PASSWORD is not set", "check .env against .env.example")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := hub.Open(ctx, hub.DialConfig{
		Host:     cfg.ClickHouseHost,
		Port:     9000,
		User:     cfg.FedAdminUser,
		Password: cfg.FedAdminPassword,
	}, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Ping(ctx)
}

// checkSuperset always dials localhost:8088, never cfg.SupersetBaseURL:
// this only ever runs from inside the superset container itself (Docker's
// HEALTHCHECK directive execs `fedctl check superset` in-container), where
// gunicorn always listens on its fixed internal port 8088 regardless of
// what host port compose.yaml publishes it as (${SUPERSET_PORT:-8088},
// user-configurable — a different, host-side concern; see
// cmd/fedctl/superset.go's registerClickHouseConnection, which runs on
// the host and does need that configurable port).
func checkSuperset(ctx context.Context, _ config.App) error {
	url := "http://localhost:8088/health"
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return jsonio.NewError(jsonio.CodeInternal, "building health request: "+err.Error(), "")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return jsonio.NewError(jsonio.CodeConnectionFailed, "superset health check failed: "+err.Error(), "")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return jsonio.NewError(jsonio.CodeConnectionFailed,
			fmt.Sprintf("superset health endpoint returned HTTP %d: %s", resp.StatusCode, string(body)), "")
	}
	return nil
}
