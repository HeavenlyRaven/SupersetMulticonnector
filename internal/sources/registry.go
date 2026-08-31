// Package sources defines the supported federated source engines
// (postgres, mysql, sqlite) behind one registry interface. Adding a new
// source type means implementing Type and registering it in an init() —
// no other package, and no frontend code, needs to change: the admin UI's
// Add Source form renders itself from Fields() via GET /source-types.
package sources

import (
	"context"
	"sort"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

// Field describes one input the admin UI must render for a source type.
type Field struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Type       string `json:"type"` // "text", "password", "number", "path"
	Required   bool   `json:"required"`
	Default    string `json:"default,omitempty"`
	HelperText string `json:"helperText,omitempty"`
}

// Params holds raw string field values, keyed by Field.Name — the shape
// HTTP form input and JSON request bodies naturally take.
type Params map[string]string

// HostAllowlist and Resolver let callers thread SSRF-check configuration
// and (in tests) a fake resolver through to types that connect over the
// network. SQLite ignores both.
type HostAllowlist = validate.Allowlist

// Type is one supported federated source engine.
type Type interface {
	// Name is the source type identifier, e.g. "postgres". Also used as
	// the ClickHouse ENGINE name's lowercase form.
	Name() string
	// Engine is the exact ClickHouse database ENGINE name, e.g. "PostgreSQL".
	Engine() string
	// HasCredentials reports whether this type stores a named collection
	// (false only for sqlite, which has no network credentials).
	HasCredentials() bool
	Fields() []Field
	// Validate checks and normalizes params. userFilesDir is only used by
	// the sqlite type. It does not check host reachability — callers that
	// need SSRF protection call validate.Host separately using the
	// "host" field this returns, both at admin time and again immediately
	// before connecting.
	Validate(userFilesDir string, params Params) (Params, error)
	// TestConnection dials the source directly, using this type's own
	// pure-Go driver (spec section 4's compatibility table), and returns
	// a table count on success. This is deliberately independent of
	// ClickHouse: "source test" must succeed or fail fast without
	// creating any ClickHouse-side database or named collection. Callers
	// MUST call validate.Host on params["host"] before this, for
	// postgres/mysql — TestConnection does not itself guard against SSRF.
	TestConnection(ctx context.Context, userFilesDir string, params Params) (int, error)
}

var registry = map[string]Type{}

func Register(t Type) { registry[t.Name()] = t }

func Get(name string) (Type, bool) {
	t, ok := registry[name]
	return t, ok
}

// All returns every registered type, sorted by name for stable output.
func All() []Type {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Type, len(names))
	for i, n := range names {
		out[i] = registry[n]
	}
	return out
}

func unknownType(name string) error {
	return jsonio.NewError(jsonio.CodeUnknownSourceType, "unknown source type: "+name,
		"valid types: postgres, mysql, sqlite")
}

// Validate looks up typeName and validates params against it, returning
// jsonio.CodeUnknownSourceType if typeName is not registered.
func Validate(ctx context.Context, typeName, userFilesDir string, params Params) (Type, Params, error) {
	t, ok := Get(typeName)
	if !ok {
		return nil, nil, unknownType(typeName)
	}
	normalized, err := t.Validate(userFilesDir, params)
	if err != nil {
		return nil, nil, err
	}
	return t, normalized, nil
}
