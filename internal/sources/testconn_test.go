package sources

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteType_TestConnection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE a (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE b (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ty, _ := Get("sqlite")
	count, err := ty.TestConnection(context.Background(), dir, Params{"path": path})
	if err != nil {
		t.Fatalf("expected successful test connection, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 tables, got %d", count)
	}
}

func TestSQLiteType_TestConnection_MissingFile(t *testing.T) {
	ty, _ := Get("sqlite")
	// t.TempDir() is fresh and auto-cleaned per test — using the shared
	// OS temp dir here would let SQLite's create-on-open behavior leave
	// a stray file that makes a later run of this exact test pass for
	// the wrong reason.
	missing := filepath.Join(t.TempDir(), "does-not-exist-xyz.sqlite")
	_, err := ty.TestConnection(context.Background(), t.TempDir(), Params{"path": missing})
	if err == nil {
		t.Fatal("expected an error opening a nonexistent sqlite file's tables")
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Error("TestConnection must not create the file as a side effect of checking it")
	}
}

// TestPostgresConnConfig_NoFallbackLeak is a regression test for two real
// bugs found in review, in sequence.
//
// First: pgx.ParseConfig("") followed by overwriting Host/Port leaves
// pgx's libpq-style Fallbacks (127.0.0.1, ::1) intact, so pgx would still
// dial loopback addresses that never passed through validate.Host — a
// genuine SSRF-guard bypass.
//
// Second (the original fix for the first bug, here corrected): clearing
// every fallback unconditionally broke sslmode=prefer's legitimate
// TLS-then-plaintext retry, which pgx implements via a same-host,
// same-port Fallback entry (pgconn.FallbackConfig's own doc comment:
// "used for TLS fallback such as sslmode=prefer") — live-verified against
// a real Postgres server with no TLS configured, where the
// fallbacks-always-nil version made "Test connection" hard-fail with
// "tls error: server refused TLS connection" even though the identical
// server accepts a plain libpq/psql connection fine, and even though
// ClickHouse's own PostgreSQL engine connects to it without any issue.
//
// So the correct invariant, asserted here: every surviving fallback must
// target the exact same host:port as cfg itself — never a different
// address — regardless of whether the list is empty or not.
func TestPostgresConnConfig_NoFallbackLeak(t *testing.T) {
	for _, host := range []string{"pg.example.internal", "203.0.113.5"} {
		cfg, err := postgresConnConfig(Params{
			"host": host, "port": "5432", "user": "u", "password": "p", "database": "d",
		})
		if err != nil {
			t.Fatalf("host %q: unexpected error: %v", host, err)
		}
		for _, fb := range cfg.Fallbacks {
			if fb.Host != cfg.Host || fb.Port != cfg.Port {
				t.Errorf("host %q: fallback dials %s:%d, a different address than the validated host %s:%d",
					host, fb.Host, fb.Port, cfg.Host, cfg.Port)
			}
		}
		if cfg.Host != host {
			t.Errorf("host %q: cfg.Host = %q, want exact match", host, cfg.Host)
		}
		if cfg.Port != 5432 {
			t.Errorf("host %q: cfg.Port = %d, want 5432", host, cfg.Port)
		}
	}
}

func TestPostgresType_TestConnection_RefusesFast(t *testing.T) {
	ty, _ := Get("postgres")
	// Port 1 is reserved and essentially never listening; this should
	// fail fast with a connection error, not hang for the full timeout.
	_, err := ty.TestConnection(context.Background(), "", Params{
		"host": "127.0.0.1", "port": "1", "user": "u", "password": "p", "database": "d",
	})
	if err == nil {
		t.Fatal("expected connection failure against a closed port")
	}
}

func TestMySQLType_TestConnection_RefusesFast(t *testing.T) {
	ty, _ := Get("mysql")
	_, err := ty.TestConnection(context.Background(), "", Params{
		"host": "127.0.0.1", "port": "1", "user": "u", "password": "p", "database": "d",
	})
	if err == nil {
		t.Fatal("expected connection failure against a closed port")
	}
}
