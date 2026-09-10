// Package ddl generates the SQL statements that attach and detach a
// federated source on the ClickHouse hub. It never executes anything —
// internal/hub does that — so every statement here is a pure, testable
// string plus its bind arguments.
//
// Named-collection parameter names (spec section 9 rule 5: "verify the
// parameter names against the pinned ClickHouse version before
// finalising"): host, port, user, password, database, and schema for
// PostgreSQL were checked against ClickHouse 26.8's own docs
// (sql-reference/statements/create/named-collection and
// engines/database-engines/postgresql) during this build — all six are
// documented, recognized parameter names for the PostgreSQL database
// engine; MySQL uses the same set minus schema. Every parameter used here
// is supported, so the "fall back to positional arguments" clause in the
// same rule does not apply — there is nothing unsupported to fall back
// from. Re-verify this against clickhouse.com/docs if the pinned
// ClickHouse version (see cmd/fedctl/images.go) ever changes.
package ddl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

// Arg is one bind value plus whether it is a secret (currently only
// passwords). internal/hub uses Secret to redact precisely — enough to
// keep host/user/database visible in error diagnostics without ever
// leaking a credential.
type Arg struct {
	Value  any
	Secret bool
}

func plain(v any) Arg  { return Arg{Value: v} }
func secret(v any) Arg { return Arg{Value: v, Secret: true} }

// Statement is one DDL statement paired with its bind values. Text
// contains only "?" placeholders where a value belongs, so Text alone is
// always safe to log or return in `fedctl plan` output — Args must never
// be logged verbatim (use Statement.Secrets to redact first).
type Statement struct {
	Text string `json:"text"`
	Args []Arg  `json:"-"`
}

// Values returns just the bind values, in order, for passing to a driver
// Exec call.
func (s Statement) Values() []any {
	out := make([]any, len(s.Args))
	for i, a := range s.Args {
		out[i] = a.Value
	}
	return out
}

// Secrets returns the string form of every secret arg in s, for redaction.
func (s Statement) Secrets() []string {
	var out []string
	for _, a := range s.Args {
		if a.Secret {
			if str, ok := a.Value.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}

// Step is one statement plus how to undo it if a later step in the same
// Plan fails. Undo is nil for steps that need no rollback.
type Step struct {
	Do   Statement
	Undo *Statement
}

// Plan is the ordered set of steps to attach or detach one source. Steps
// execute in order; on failure, internal/hub rolls back completed steps in
// reverse order using each Step's Undo.
type Plan struct {
	Steps []Step
}

// Statements returns just the Do statements, in order — used by `fedctl
// plan` to print what would run without executing it.
func (p Plan) Statements() []Statement {
	out := make([]Statement, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Do
	}
	return out
}

func CollectionName(sourceName string) string { return "fed_" + sourceName + "_creds" }
func DatabaseName(sourceName string) string   { return "fed_" + sourceName }

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeInvalidPort, fmt.Sprintf("port %q is not a number", s), "")
	}
	if err := validate.Port(p); err != nil {
		return 0, err
	}
	return p, nil
}

func requireFields(p map[string]string, fields ...string) error {
	for _, f := range fields {
		if strings.TrimSpace(p[f]) == "" {
			return jsonio.NewError(jsonio.CodeInvalidRequest, fmt.Sprintf("missing required field %q", f), "")
		}
	}
	return nil
}

// AttachPostgres builds the plan for
//
//	CREATE NAMED COLLECTION IF NOT EXISTS fed_<name>_creds AS
//	    host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?;
//	CREATE DATABASE IF NOT EXISTS fed_<name> ENGINE = PostgreSQL(fed_<name>_creds);
func AttachPostgres(sourceName string, p map[string]string) (Plan, error) {
	if err := validate.SourceName(sourceName); err != nil {
		return Plan{}, err
	}
	if err := requireFields(p, "host", "port", "user", "password", "database"); err != nil {
		return Plan{}, err
	}
	port, err := parsePort(p["port"])
	if err != nil {
		return Plan{}, err
	}
	schema := p["schema"]
	if schema == "" {
		schema = "public"
	}
	coll := validate.QuoteIdentifier(CollectionName(sourceName))
	db := validate.QuoteIdentifier(DatabaseName(sourceName))
	return Plan{Steps: []Step{
		{
			Do: Statement{
				Text: fmt.Sprintf("CREATE NAMED COLLECTION IF NOT EXISTS %s AS host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?", coll),
				Args: []Arg{plain(p["host"]), plain(port), plain(p["user"]), secret(p["password"]), plain(p["database"]), plain(schema)},
			},
			Undo: &Statement{Text: fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s", coll)},
		},
		{
			// COMMENT stashes the host non-secretly on the database
			// itself, readable back via system.databases.comment — live-
			// verified this is the only way to recover it later: ClickHouse
			// redacts EVERY field in a named collection as [HIDDEN] when
			// read back via system.named_collections, including
			// non-secret ones like host, and "SHOW CREATE NAMED
			// COLLECTION" (an earlier attempt at this) isn't even valid
			// syntax. See hub.go's SourceHost, which reads this comment.
			Do:   Statement{Text: fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s ENGINE = PostgreSQL(%s) COMMENT ?", db, coll), Args: []Arg{plain(hostComment(p["host"]))}},
			Undo: &Statement{Text: fmt.Sprintf("DROP DATABASE IF EXISTS %s", db)},
		},
	}}, nil
}

// hostComment is the exact, parseable format hub.SourceHost expects.
func hostComment(host string) string { return "host=" + host }

// AttachMySQL builds the equivalent plan with ENGINE = MySQL(...) and no
// schema parameter.
func AttachMySQL(sourceName string, p map[string]string) (Plan, error) {
	if err := validate.SourceName(sourceName); err != nil {
		return Plan{}, err
	}
	if err := requireFields(p, "host", "port", "user", "password", "database"); err != nil {
		return Plan{}, err
	}
	port, err := parsePort(p["port"])
	if err != nil {
		return Plan{}, err
	}
	coll := validate.QuoteIdentifier(CollectionName(sourceName))
	db := validate.QuoteIdentifier(DatabaseName(sourceName))
	return Plan{Steps: []Step{
		{
			Do: Statement{
				Text: fmt.Sprintf("CREATE NAMED COLLECTION IF NOT EXISTS %s AS host = ?, port = ?, user = ?, password = ?, database = ?", coll),
				Args: []Arg{plain(p["host"]), plain(port), plain(p["user"]), secret(p["password"]), plain(p["database"])},
			},
			Undo: &Statement{Text: fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s", coll)},
		},
		{
			Do:   Statement{Text: fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s ENGINE = MySQL(%s) COMMENT ?", db, coll), Args: []Arg{plain(hostComment(p["host"]))}},
			Undo: &Statement{Text: fmt.Sprintf("DROP DATABASE IF EXISTS %s", db)},
		},
	}}, nil
}

// AttachSQLite builds:
//
//	CREATE DATABASE IF NOT EXISTS fed_<name> ENGINE = SQLite(?);
//
// path must already be validated and resolved by validate.SQLitePath —
// this function does not touch the filesystem. There is no named
// collection: SQLite has no credentials.
func AttachSQLite(sourceName, resolvedPath string) (Plan, error) {
	if err := validate.SourceName(sourceName); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(resolvedPath) == "" {
		return Plan{}, jsonio.NewError(jsonio.CodeInvalidPath, "sqlite path must not be empty", "")
	}
	db := validate.QuoteIdentifier(DatabaseName(sourceName))
	return Plan{Steps: []Step{
		{
			Do:   Statement{Text: fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s ENGINE = SQLite(?)", db), Args: []Arg{plain(resolvedPath)}},
			Undo: &Statement{Text: fmt.Sprintf("DROP DATABASE IF EXISTS %s", db)},
		},
	}}, nil
}

// Detach builds the plan to remove a source: drop its database, then its
// named collection if it has one (hasCollection is false for sqlite).
func Detach(sourceName string, hasCollection bool) (Plan, error) {
	if err := validate.SourceName(sourceName); err != nil {
		return Plan{}, err
	}
	db := validate.QuoteIdentifier(DatabaseName(sourceName))
	steps := []Step{
		{Do: Statement{Text: fmt.Sprintf("DROP DATABASE IF EXISTS %s", db)}},
	}
	if hasCollection {
		coll := validate.QuoteIdentifier(CollectionName(sourceName))
		steps = append(steps, Step{Do: Statement{Text: fmt.Sprintf("DROP NAMED COLLECTION IF EXISTS %s", coll)}})
	}
	return Plan{Steps: steps}, nil
}
