// Package config loads fedctl's runtime configuration from the
// environment (never from CLI flags — see the credential-flag rejection
// rule in internal/validate) and parses the declarative config/sources.yaml
// file used by `fedctl apply` and `fedctl plan`.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// App is fedctl's environment-sourced configuration. Every field maps to
// exactly one .env.example key, documented there.
type App struct {
	ClickHouseHost      string
	ClickHouseHTTPPort  int
	FedAdminUser        string
	FedAdminPassword    string
	BIReadOnlyUser      string
	BIReadOnlyPassword  string
	UserFilesDir        string
	AllowedSourceHosts  []string
	SupersetBaseURL     string
	SupersetMetadataURI string
	SupersetAdminUser   string
	SupersetAdminPass   string
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// LoadApp reads App from the process environment. It is deliberately
// permissive — it does not require FedAdminPassword, the metadata URI, or
// any other single field, because different fedctl commands need
// different subsets (e.g. `fedctl check clickhouse`, run from inside the
// clickhouse container's own HEALTHCHECK, needs no Superset metadata
// config at all). Callers that need a specific field validate it
// themselves: openHub checks FedAdminPassword, `up`/`preflight` call
// ValidateMetadataURI explicitly. LoadApp deliberately does NOT accept a
// --sqlalchemy-uri-style flag: every credential here comes from the
// environment, matching the "no secrets in compose.yaml, everything from
// .env" rule.
func LoadApp() (App, error) {
	a := App{
		ClickHouseHost:      getenvDefault("CLICKHOUSE_HOST", "clickhouse"),
		FedAdminUser:        getenvDefault("FEDCTL_ADMIN_USER", "fed_admin"),
		FedAdminPassword:    os.Getenv("FEDCTL_ADMIN_PASSWORD"),
		BIReadOnlyUser:      getenvDefault("SUPERSET_CH_USER", "bi_ro"),
		BIReadOnlyPassword:  os.Getenv("SUPERSET_CH_PASSWORD"),
		UserFilesDir:        getenvDefault("CLICKHOUSE_USER_FILES_DIR", "/var/lib/clickhouse/user_files"),
		SupersetBaseURL:     getenvDefault("SUPERSET_BASE_URL", "http://superset:8088"),
		SupersetMetadataURI: os.Getenv("SQLALCHEMY_DATABASE_URI"),
		SupersetAdminUser:   os.Getenv("SUPERSET_ADMIN_USER"),
		SupersetAdminPass:   os.Getenv("SUPERSET_ADMIN_PASSWORD"),
	}

	port := getenvDefault("CLICKHOUSE_HTTP_PORT", "8123")
	p, err := strconv.Atoi(port)
	if err != nil {
		return App{}, fmt.Errorf("CLICKHOUSE_HTTP_PORT must be numeric, got %q", port)
	}
	a.ClickHouseHTTPPort = p

	if raw := os.Getenv("FEDCTL_ALLOWED_SOURCE_HOSTS"); raw != "" {
		for _, h := range strings.Split(raw, ",") {
			if h = strings.TrimSpace(h); h != "" {
				a.AllowedSourceHosts = append(a.AllowedSourceHosts, h)
			}
		}
	}

	return a, nil
}

// ValidateMetadataURI enforces spec section 1: Superset's metadata
// database must be the external PostgreSQL, never unset, never SQLite.
func ValidateMetadataURI(uri string) error {
	if strings.TrimSpace(uri) == "" {
		return fmt.Errorf("SQLALCHEMY_DATABASE_URI is unset: Superset's metadata database must be an external PostgreSQL instance, not SQLite, and not left to default")
	}
	lower := strings.ToLower(uri)
	if strings.HasPrefix(lower, "sqlite") {
		return fmt.Errorf("SQLALCHEMY_DATABASE_URI points at SQLite (%q): this is a reliability problem, not a style preference — point it at the external PostgreSQL instance", uri)
	}
	if !strings.HasPrefix(lower, "postgresql") {
		return fmt.Errorf("SQLALCHEMY_DATABASE_URI must be a postgresql:// URI, got scheme from %q", uri)
	}
	return nil
}

// SourceSpec is one entry in config/sources.yaml. Password (and any other
// secret) is never a literal field here — it is read from PasswordEnv at
// apply time, so the yaml file itself is safe to commit.
type SourceSpec struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Host        string `yaml:"host,omitempty"`
	Port        int    `yaml:"port,omitempty"`
	User        string `yaml:"user,omitempty"`
	PasswordEnv string `yaml:"password_env,omitempty"`
	Database    string `yaml:"database,omitempty"`
	Schema      string `yaml:"schema,omitempty"`
	Path        string `yaml:"path,omitempty"` // sqlite only
}

// ResolveParams turns a SourceSpec into the map[string]string shape
// sources.Type.Validate and the ddl.AttachX functions expect, reading the
// password from the environment variable PasswordEnv names.
func (s SourceSpec) ResolveParams() (map[string]string, error) {
	p := map[string]string{}
	if s.Host != "" {
		p["host"] = s.Host
	}
	if s.Port != 0 {
		p["port"] = strconv.Itoa(s.Port)
	}
	if s.User != "" {
		p["user"] = s.User
	}
	if s.PasswordEnv != "" {
		pw := os.Getenv(s.PasswordEnv)
		if pw == "" {
			return nil, fmt.Errorf("source %q: environment variable %q (password_env) is unset or empty", s.Name, s.PasswordEnv)
		}
		p["password"] = pw
	}
	if s.Database != "" {
		p["database"] = s.Database
	}
	if s.Schema != "" {
		p["schema"] = s.Schema
	}
	if s.Path != "" {
		p["path"] = s.Path
	}
	return p, nil
}

// SourcesFile is the root of config/sources.yaml.
type SourcesFile struct {
	Sources []SourceSpec `yaml:"sources"`
}

// LoadSourcesFile reads and parses path. It does not validate individual
// sources — that is internal/sources.Validate's job, run per-source by the
// caller so one bad entry doesn't block reporting problems with the rest.
func LoadSourcesFile(path string) (SourcesFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourcesFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var sf SourcesFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return SourcesFile{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, s := range sf.Sources {
		if seen[s.Name] {
			return SourcesFile{}, fmt.Errorf("%s: duplicate source name %q", path, s.Name)
		}
		seen[s.Name] = true
	}
	return sf, nil
}
