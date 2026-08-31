package main

import (
	"context"
	"fmt"

	"github.com/acme/superset-federation/internal/config"
	"github.com/acme/superset-federation/internal/ddl"
	"github.com/acme/superset-federation/internal/hub"
	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/sources"
	"github.com/acme/superset-federation/internal/validate"
)

func buildAttachPlan(ty sources.Type, name string, params map[string]string) (ddl.Plan, error) {
	switch ty.Name() {
	case "postgres":
		return ddl.AttachPostgres(name, params)
	case "mysql":
		return ddl.AttachMySQL(name, params)
	case "sqlite":
		return ddl.AttachSQLite(name, params["path"])
	default:
		return ddl.Plan{}, jsonio.NewError(jsonio.CodeUnknownSourceType, "unknown source type: "+ty.Name(), "")
	}
}

// attachSource validates params, builds the DDL plan, and executes it.
// This is the single reconcile implementation spec section 9 requires:
// `source add` and `apply` both call this, relying on the plan's own
// "IF NOT EXISTS" clauses for idempotence rather than duplicating logic.
func attachSource(ctx context.Context, conn *hub.Conn, app config.App, name, typeName string, rawParams map[string]string) error {
	ty, params, err := sources.Validate(ctx, typeName, app.UserFilesDir, rawParams)
	if err != nil {
		return err
	}
	if host, ok := params["host"]; ok {
		if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil {
			return err
		}
	}
	plan, err := buildAttachPlan(ty, name, params)
	if err != nil {
		return err
	}
	// Re-check the host immediately before the DDL that causes ClickHouse
	// to dial it — DNS can change between admin-time validation and this
	// moment; see validate.Host's TOCTOU note.
	if host, ok := params["host"]; ok {
		if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil {
			return err
		}
	}
	return conn.Attach(ctx, plan)
}

type addSourceRequest struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
}

func doAddSource(ctx context.Context, conn *hub.Conn, app config.App, req addSourceRequest) (any, error) {
	if err := validate.SourceName(req.Name); err != nil {
		return nil, err
	}
	exists, err := conn.DatabaseExists(ctx, ddl.DatabaseName(req.Name))
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, jsonio.NewError(jsonio.CodeSourceExists, fmt.Sprintf("source %q already exists", req.Name), "")
	}
	if err := attachSource(ctx, conn, app, req.Name, req.Type, req.Params); err != nil {
		return nil, err
	}
	return map[string]any{"name": req.Name, "type": req.Type, "database": ddl.DatabaseName(req.Name)}, nil
}

func doRemoveSource(ctx context.Context, conn *hub.Conn, name string) (any, error) {
	if err := validate.SourceName(name); err != nil {
		return nil, err
	}
	engine, err := conn.DatabaseEngine(ctx, ddl.DatabaseName(name))
	if err != nil {
		return nil, err
	}
	hasCollection := engine != "SQLite"
	// Look up host/type before dropping anything — the Python shim's
	// audit log needs them (spec section 10), and once Detach runs the
	// named collection (and its host) is gone.
	sourceType := hub.EngineToType(engine)
	host := ""
	if hasCollection {
		host = conn.SourceHost(ctx, name)
	}

	plan, err := ddl.Detach(name, hasCollection)
	if err != nil {
		return nil, err
	}
	if err := conn.Detach(ctx, plan); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "type": sourceType, "host": host, "removed": true}, nil
}

func doListSources(ctx context.Context, conn *hub.Conn) (any, error) {
	return conn.List(ctx)
}

func doSourceTypes() any {
	var out []map[string]any
	for _, ty := range sources.All() {
		out = append(out, map[string]any{
			"name":           ty.Name(),
			"engine":         ty.Engine(),
			"hasCredentials": ty.HasCredentials(),
			"fields":         ty.Fields(),
		})
	}
	return out
}

func doTables(ctx context.Context, conn *hub.Conn, name string) (any, error) {
	if err := validate.SourceName(name); err != nil {
		return nil, err
	}
	if _, err := conn.DatabaseEngine(ctx, ddl.DatabaseName(name)); err != nil {
		return nil, err
	}
	return conn.Tables(ctx, ddl.DatabaseName(name))
}

func doProbe(ctx context.Context, conn *hub.Conn, name string) (any, error) {
	if err := validate.SourceName(name); err != nil {
		return nil, err
	}
	if _, err := conn.DatabaseEngine(ctx, ddl.DatabaseName(name)); err != nil {
		return nil, err
	}
	if err := conn.Probe(ctx, ddl.DatabaseName(name)); err != nil {
		return nil, err
	}
	tables, err := conn.Tables(ctx, ddl.DatabaseName(name))
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "reachable": true, "tableCount": len(tables)}, nil
}

type testSourceRequest struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
}

// doTestSource dials the source directly with its own pure-Go driver
// (sources.Type.TestConnection — pgx/v5, go-sql-driver/mysql, or
// modernc.org/sqlite per spec section 4) and never touches ClickHouse at
// all: no named collection, no database, nothing to clean up. This is
// deliberately independent of doAddSource so "Test connection" stays fast
// and side-effect-free ahead of "Add".
func doTestSource(ctx context.Context, app config.App, req testSourceRequest) (any, error) {
	ty, params, err := sources.Validate(ctx, req.Type, app.UserFilesDir, req.Params)
	if err != nil {
		return nil, err
	}
	if host, ok := params["host"]; ok {
		// TestConnection dials this host itself, so this is the one and
		// only check needed here — no "recheck before connecting" step,
		// since there is no separate connecting step: this call IS it.
		if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil {
			return nil, err
		}
	}
	tableCount, err := ty.TestConnection(ctx, app.UserFilesDir, params)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "tableCount": tableCount}, nil
}
