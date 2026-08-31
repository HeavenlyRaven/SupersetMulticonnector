package sources

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

func init() { Register(postgresType{}) }

type postgresType struct{}

func (postgresType) Name() string         { return "postgres" }
func (postgresType) Engine() string       { return "PostgreSQL" }
func (postgresType) HasCredentials() bool { return true }

func (postgresType) Fields() []Field {
	return []Field{
		{Name: "host", Label: "Host", Type: "text", Required: true},
		{Name: "port", Label: "Port", Type: "number", Required: true, Default: "5432"},
		{Name: "user", Label: "User", Type: "text", Required: true},
		{Name: "password", Label: "Password", Type: "password", Required: true},
		{Name: "database", Label: "Database", Type: "text", Required: true},
		{Name: "schema", Label: "Schema", Type: "text", Required: false, Default: "public"},
	}
}

func (postgresType) Validate(_ string, params Params) (Params, error) {
	out := Params{}
	for k, v := range params {
		out[k] = v
	}
	for _, req := range []string{"host", "port", "user", "password", "database"} {
		if strings.TrimSpace(out[req]) == "" {
			return nil, jsonio.NewError(jsonio.CodeInvalidRequest,
				fmt.Sprintf("postgres source requires %q", req), "")
		}
	}
	if out["schema"] == "" {
		out["schema"] = "public"
	}
	if err := validate.Port(atoiOrZero(out["port"])); err != nil {
		return nil, err
	}
	return out, nil
}

// postgresConnConfig builds a *pgx.ConnConfig for exactly p["host"]:port —
// split out from TestConnection so its no-fallback-leakage guarantee can
// be unit tested without a network dial.
//
// It deliberately does NOT call pgx.ParseConfig("") and then overwrite
// Host/Port: an empty DSN makes pgx populate libpq-style Fallbacks
// (127.0.0.1 and ::1, among others) that pgx will still dial on
// connection failure even after Host/Port are overwritten — an SSRF
// guard bypass, since those addresses never pass through validate.Host.
// Building cfg from an explicit connection string avoids the fallback
// population in the first place; clearing Fallbacks afterward is
// defense in depth in case pgx's parser ever adds one anyway.
func postgresConnConfig(p Params) (*pgx.ConnConfig, error) {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(p["user"], p["password"]),
		Host:   fmt.Sprintf("%s:%d", p["host"], atoiOrZero(p["port"])),
		Path:   "/" + p["database"],
	}
	q := u.Query()
	q.Set("sslmode", "prefer")
	q.Set("connect_timeout", "10")
	u.RawQuery = q.Encode()

	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "building postgres connection config: "+err.Error(), "")
	}
	cfg.Fallbacks = nil
	if cfg.Host != p["host"] || int(cfg.Port) != atoiOrZero(p["port"]) {
		// Defense in depth: if pgx ever resolved to a different primary
		// host/port than what we asked for, refuse rather than dial it.
		return nil, jsonio.NewError(jsonio.CodeInternal, "postgres connection config did not match the requested host/port", "")
	}
	return cfg, nil
}

// TestConnection dials postgres directly with pgx/v5 — the pure-Go driver
// from spec section 4's compatibility table — independent of ClickHouse,
// so "Test connection" is fast and leaves no ClickHouse-side state.
func (postgresType) TestConnection(ctx context.Context, _ string, p Params) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cfg, err := postgresConnConfig(p)
	if err != nil {
		return 0, err
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeConnectionFailed, "cannot connect to postgres source: "+err.Error(), "")
	}
	defer conn.Close(ctx)

	schema := p["schema"]
	if schema == "" {
		schema = "public"
	}
	var count int
	err = conn.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema = $1", schema).Scan(&count)
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeConnectionFailed, "connected, but listing tables failed: "+err.Error(), "")
	}
	return count, nil
}
