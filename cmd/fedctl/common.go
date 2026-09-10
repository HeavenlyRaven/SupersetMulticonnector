package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/acme/superset-federation/internal/config"
	"github.com/acme/superset-federation/internal/hub"
	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

// loadApp loads config.App and, on error, either writes a JSON error
// envelope (jsonMode) or a plain stderr message, then returns the error so
// the caller can propagate a non-zero exit.
func loadApp(jsonMode bool) (config.App, error) {
	app, err := config.LoadApp()
	if err != nil {
		if jsonMode {
			_ = jsonio.WriteError(os.Stdout, jsonio.CodeInternal, err.Error(), "check .env against .env.example")
		} else {
			fmt.Fprintln(os.Stderr, "fedctl: "+err.Error())
		}
		return config.App{}, err
	}
	return app, nil
}

// stderrLogger writes redacted diagnostic lines to stderr — used as the
// hub.Logger for every command, JSON mode or not, since stdout in JSON
// mode is reserved for exactly one Response envelope.
func stderrLogger(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// openHub opens a hub.Conn authenticated as fed_admin — the only role
// fedctl itself ever uses; bi_ro is exclusively for Superset's own
// connection, configured separately in section 12. config.LoadApp is
// deliberately permissive (some commands never touch the hub at all), so
// this is where FEDCTL_ADMIN_PASSWORD is actually required.
func openHub(ctx context.Context, app config.App) (*hub.Conn, error) {
	if app.FedAdminPassword == "" {
		return nil, jsonio.NewError(jsonio.CodeInternal,
			"FEDCTL_ADMIN_PASSWORD is not set", "check .env against .env.example")
	}
	return hub.Open(ctx, hub.DialConfig{
		Host:     app.ClickHouseHost,
		Port:     9000,
		User:     app.FedAdminUser,
		Password: app.FedAdminPassword,
	}, stderrLogger)
}

// openBIReadOnly opens a hub.Conn authenticated as bi_ro — used only for
// Probe's actual data-reading touch query. fed_admin deliberately has no
// SELECT privilege on federated databases (see clickhouse/users.d/roles.xml:
// it only gets CREATE/DROP DATABASE, NAMED COLLECTION CONTROL, TABLE ENGINE,
// and SHOW TABLES — never SELECT on federated data), so a probe run as
// fed_admin fails with "Not enough privileges" — live-verified. bi_ro is the
// same role Superset itself connects as, so this proves exactly what
// Superset would see.
func openBIReadOnly(ctx context.Context, app config.App) (*hub.Conn, error) {
	if app.BIReadOnlyPassword == "" {
		return nil, jsonio.NewError(jsonio.CodeInternal,
			"SUPERSET_CH_PASSWORD is not set", "check .env against .env.example")
	}
	return hub.Open(ctx, hub.DialConfig{
		Host:     app.ClickHouseHost,
		Port:     9000,
		User:     app.BIReadOnlyUser,
		Password: app.BIReadOnlyPassword,
	}, stderrLogger)
}

// allowlist builds the SSRF allowlist from app config.
func allowlist(app config.App) *validate.Allowlist {
	return validate.NewAllowlist(app.AllowedSourceHosts)
}

// readJSONRequest decodes exactly one JSON object from stdin into v.
func readJSONRequest(v any) error {
	return jsonio.ReadRequest(os.Stdin, v)
}

// writeJSONResult writes result as a successful envelope if err is nil,
// otherwise an error envelope built from err. It always writes exactly
// one envelope to stdout and returns err unchanged so the caller can set
// the process exit code without double-reporting.
func writeJSONResult(result any, err error) error {
	if err != nil {
		_ = jsonio.WriteErr(os.Stdout, err)
		return err
	}
	return jsonio.WriteOK(os.Stdout, result)
}

// printHuman renders v as indented JSON to stdout for plain (non---json)
// CLI output — every fedctl command that returns structured data uses the
// same rendering in both modes, just wrapped in the envelope for --json.
func printHuman(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
