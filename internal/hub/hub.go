// Package hub wraps the ClickHouse connection used to execute DDL plans
// and run health/introspection queries against the federation hub. It is
// the only package that talks to ClickHouse over the wire.
package hub

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/acme/superset-federation/internal/ddl"
	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

// Logger receives redacted diagnostic lines, meant for stderr — never
// stdout, which in --json mode carries only the Response envelope.
type Logger func(format string, args ...any)

func noopLogger(string, ...any) {}

// Conn is a live connection to the ClickHouse hub, opened with credentials
// for one of the two roles defined in clickhouse/users.d/roles.xml:
// fed_admin (DDL) or bi_ro (read-only, used only by doctor/probe-style
// read queries run as a sanity check, never by Superset itself — Superset
// connects independently via clickhouse-connect).
type Conn struct {
	db  *sql.DB
	log Logger
}

// DialConfig is everything needed to open a Conn. Port is the ClickHouse
// native protocol port (default 9000) — fedctl talks native, Superset
// talks HTTP via clickhouse-connect on 8123; the two are independent.
type DialConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// Open dials ClickHouse and pings it. The returned error is already a
// *jsonio.Error with CodeHubUnavailable on failure.
func Open(ctx context.Context, cfg DialConfig, log Logger) (*Conn, error) {
	if cfg.Database == "" {
		cfg.Database = "default"
	}
	if cfg.Port == 0 {
		cfg.Port = 9000
	}
	if log == nil {
		log = noopLogger
	}
	db := clickhouse.OpenDB(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
	})
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, jsonio.NewError(jsonio.CodeHubUnavailable,
			fmt.Sprintf("cannot reach ClickHouse hub at %s:%d", cfg.Host, cfg.Port), err.Error())
	}
	return &Conn{db: db, log: log}, nil
}

func (c *Conn) Close() error { return c.db.Close() }

// Exec and Query are thin pass-throughs for callers that need to run
// arbitrary SQL against the hub directly — e.g. the integration test
// setting up a native ClickHouse table to join against, or `fedctl plan`
// -style tooling. Every other method in this package is a named,
// narrower operation built on top of these two.
func (c *Conn) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *Conn) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return c.db.QueryContext(ctx, query, args...)
}

// redact scrubs every secret arg in plan from s, for safe logging of
// otherwise-raw driver errors.
func redact(plan ddl.Plan, s string) string {
	var secrets []string
	for _, step := range plan.Steps {
		secrets = append(secrets, step.Do.Secrets()...)
	}
	return validate.NewRedactor(secrets...).Redact(s)
}

// Attach executes plan's steps in order against the hub. If a step fails,
// it rolls back every previously-succeeded step in this plan, in reverse
// order, before returning the original (redacted) error — so a failure
// partway through never leaves an orphaned named collection or database.
// Rollback failures are logged but do not shadow the original error: the
// original attach failure is what the caller needs to see.
func (c *Conn) Attach(ctx context.Context, plan ddl.Plan) error {
	for i, step := range plan.Steps {
		if _, err := c.db.ExecContext(ctx, step.Do.Text, step.Do.Values()...); err != nil {
			c.rollback(ctx, plan, plan.Steps[:i])
			return jsonio.NewError(jsonio.CodeInternal,
				"attach failed: "+redact(plan, err.Error()), "")
		}
	}
	return nil
}

func (c *Conn) rollback(ctx context.Context, plan ddl.Plan, completed []ddl.Step) {
	for i := len(completed) - 1; i >= 0; i-- {
		step := completed[i]
		if step.Undo == nil {
			continue
		}
		if _, err := c.db.ExecContext(ctx, step.Undo.Text, step.Undo.Values()...); err != nil {
			c.log("rollback step failed (%s): %s", step.Undo.Text, redact(plan, err.Error()))
		}
	}
}

// Detach executes a Detach plan. Unlike Attach, there is nothing to roll
// back: every statement is DROP ... IF EXISTS, so a failure partway
// through simply leaves the remaining objects for the caller to retry.
func (c *Conn) Detach(ctx context.Context, plan ddl.Plan) error {
	for _, step := range plan.Steps {
		if _, err := c.db.ExecContext(ctx, step.Do.Text, step.Do.Values()...); err != nil {
			return jsonio.NewError(jsonio.CodeInternal, "detach failed: "+redact(plan, err.Error()), "")
		}
	}
	return nil
}

// Ping checks hub connectivity, for `fedctl check clickhouse`.
func (c *Conn) Ping(ctx context.Context) error {
	if err := c.db.PingContext(ctx); err != nil {
		return jsonio.NewError(jsonio.CodeHubUnavailable, "ClickHouse hub unreachable", err.Error())
	}
	return nil
}

// DatabaseExists reports whether a database with this name already exists
// on the hub.
func (c *Conn) DatabaseExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx,
		"SELECT count() FROM system.databases WHERE name = ?", name).Scan(&n)
	if err != nil {
		return false, jsonio.NewError(jsonio.CodeInternal, "checking database existence: "+err.Error(), "")
	}
	return n > 0, nil
}

// TableInfo is one row of `fedctl source tables`.
type TableInfo struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
}

// Tables lists the tables ClickHouse currently sees inside a federated
// database, which for PostgreSQL/MySQL/SQLite engines reflects the live
// upstream schema (there is no caching or materialization to go stale).
func (c *Conn) Tables(ctx context.Context, database string) ([]TableInfo, error) {
	rows, err := c.db.QueryContext(ctx,
		"SELECT name, engine FROM system.tables WHERE database = ? ORDER BY name", database)
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "listing tables: "+err.Error(), "")
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.Engine); err != nil {
			return nil, jsonio.NewError(jsonio.CodeInternal, "reading table row: "+err.Error(), "")
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "listing tables: "+err.Error(), "")
	}
	return out, nil
}

// engineToType maps a ClickHouse ENGINE name back to our source type name.
var engineToType = map[string]string{
	"PostgreSQL": "postgres",
	"MySQL":      "mysql",
	"SQLite":     "sqlite",
}

// EngineToType exports the same mapping for callers (e.g. cmd/fedctl's
// doRemoveSource) that need to report a source's type without duplicating
// this table.
func EngineToType(engine string) string { return engineToType[engine] }

// SourceSummary is one row of `fedctl source list`.
type SourceSummary struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Database   string `json:"database"`
	TableCount int    `json:"tableCount"`
	Host       string `json:"host,omitempty"`
}

const databasePrefix = "fed_"

// List enumerates every federated database on the hub (every ClickHouse
// database named "fed_*" that isn't itself a named collection). Health and
// exact table counts are computed live on every call — there is no cache
// to go stale, matching the rest of this system's "no materialization"
// design.
func (c *Conn) List(ctx context.Context) ([]SourceSummary, error) {
	rows, err := c.db.QueryContext(ctx,
		"SELECT name, engine FROM system.databases WHERE name LIKE ? ORDER BY name", databasePrefix+"%")
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "listing sources: "+err.Error(), "")
	}
	defer rows.Close()

	var out []SourceSummary
	for rows.Next() {
		var dbName, engine string
		if err := rows.Scan(&dbName, &engine); err != nil {
			return nil, jsonio.NewError(jsonio.CodeInternal, "reading source row: "+err.Error(), "")
		}
		s := SourceSummary{
			Name:     strings.TrimPrefix(dbName, databasePrefix),
			Type:     engineToType[engine],
			Database: dbName,
		}
		if tables, err := c.Tables(ctx, dbName); err == nil {
			s.TableCount = len(tables)
		}
		if host, err := c.databaseHost(ctx, dbName); err == nil {
			s.Host = host
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, jsonio.NewError(jsonio.CodeInternal, "listing sources: "+err.Error(), "")
	}
	return out, nil
}

// databaseHost extracts the host embedded in a federated database's own
// COMMENT (set at CREATE DATABASE time by internal/ddl's hostComment — see
// AttachPostgres/AttachMySQL). This is the only retrievable place a
// federated source's host lives: ClickHouse redacts EVERY field of a named
// collection, including non-secret ones like host, as [HIDDEN] when read
// back via system.named_collections — live-verified there is no SQL-level
// way to recover it from there, for anyone, ever. "SHOW CREATE NAMED
// COLLECTION" (an earlier attempt at this) isn't even valid ClickHouse
// syntax. A parse failure (e.g. sqlite sources, which have no host) just
// yields "", not an error, since host display is informational, not
// load-bearing.
func (c *Conn) databaseHost(ctx context.Context, dbName string) (string, error) {
	row := c.db.QueryRowContext(ctx, "SELECT comment FROM system.databases WHERE name = ?", dbName)
	var comment string
	if err := row.Scan(&comment); err != nil {
		return "", err
	}
	m := hostCommentRe.FindStringSubmatch(comment)
	if len(m) != 2 {
		return "", jsonio.NewError(jsonio.CodeInternal, "no host recorded in database comment", "")
	}
	return m[1], nil
}

var hostCommentRe = regexp.MustCompile(`^host=(.*)$`)

// SourceHost is the exported form of databaseHost for a source by its
// (un-prefixed) name — used by `source remove` to report host in its
// response, which the Python shim's audit log needs (spec section 10:
// "Audit every mutation ... user, source name, type, host, timestamp").
// Empty string, no error, for sqlite sources (no host at all).
func (c *Conn) SourceHost(ctx context.Context, sourceName string) string {
	host, err := c.databaseHost(ctx, databasePrefix+sourceName)
	if err != nil {
		return ""
	}
	return host
}

// Probe runs a cheap query against a federated database to confirm the
// upstream source is actually reachable through ClickHouse right now
// (distinct from Attach succeeding, which only proves the named
// collection/database DDL was accepted — the upstream connection is lazy).
//
// The catalog checks (self-check, table listing) run as c, but the final
// data-touching query runs as reader, which must be bi_ro-authenticated:
// fed_admin (what c always is, in every caller) deliberately has no SELECT
// grant on federated data — live-verified a fed_admin-run SELECT against a
// federated table fails with "Not enough privileges" — so reusing c here
// would make every probe fail. reader is exactly the role Superset itself
// queries as, so this proves what Superset would actually see.
func (c *Conn) Probe(ctx context.Context, reader *Conn, database string) error {
	row := c.db.QueryRowContext(ctx, "SELECT 1 FROM system.one")
	var one int
	if err := row.Scan(&one); err != nil {
		return jsonio.NewError(jsonio.CodeConnectionFailed, "hub self-check failed: "+err.Error(), "")
	}
	tables, err := c.Tables(ctx, database)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return jsonio.NewError(jsonio.CodeConnectionFailed,
			fmt.Sprintf("federated database %q has no visible tables", database),
			"the upstream source may be unreachable, or has no tables in the configured schema")
	}
	// Touch the first table to force ClickHouse to actually open the
	// upstream connection, not just read its own catalog.
	q := fmt.Sprintf("SELECT count() FROM %s.%s", validate.QuoteIdentifier(database), validate.QuoteIdentifier(tables[0].Name))
	if _, err := reader.db.ExecContext(ctx, q); err != nil {
		return jsonio.NewError(jsonio.CodeConnectionFailed, "probe query against upstream failed: "+err.Error(), "")
	}
	return nil
}
