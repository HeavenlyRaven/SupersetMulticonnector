//go:build integration

// Package integration runs the full stack against real Docker and
// asserts the four-way live join actually works. It is NOT run by `go
// test ./...` — only `go test -tags integration ./test/integration/...`,
// with a real docker daemon and a populated .env at the repo root (see
// .env.example). This exercises `fedctl up --dev`, `source add` for all
// three source types (through the same `docker compose exec superset
// fedctl ... --json` path the Python shim uses, so hostnames like
// "dev-postgres" resolve exactly as they do in production), and a join
// spanning all four storage backends: PostgreSQL, MySQL, SQLite, and a
// native ClickHouse table.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/acme/superset-federation/internal/hub"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// test/integration -> repo root
	return filepath.Dir(filepath.Dir(wd))
}

func composeArgs(root string, extra ...string) []string {
	args := []string{
		"compose",
		"-f", filepath.Join(root, "compose.yaml"),
		"-f", filepath.Join(root, "compose.dev.yaml"),
		"-f", filepath.Join(root, "test", "integration", "compose.test.yaml"),
		"--profile", "dev",
	}
	return append(args, extra...)
}

func runDocker(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", composeArgs(root, args...)...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %v failed: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// execFedctl runs `fedctl <args...> --json` inside the running superset
// container — the same path the Python shim uses — piping req as the
// stdin request body, so source hostnames resolve on the docker network
// exactly as they do in production.
func execFedctl(t *testing.T, root string, req any, args ...string) map[string]any {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	full := append([]string{"exec", "-T", "superset", "fedctl"}, args...)
	full = append(full, "--json")
	cmd := exec.Command("docker", composeArgs(root, full...)...)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(body)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	_ = cmd.Run() // fedctl --json always writes a Response envelope; check ok below, not the exit code alone
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("fedctl %v returned non-JSON output: %v\nstdout: %s\nstderr: %s", args, err, out.String(), errOut.String())
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("fedctl %v failed: %v", args, resp["error"])
	}
	return resp
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available; skipping integration test")
	}
}

func requireEnv(t *testing.T, keys ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var missing []string
	for _, k := range keys {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
		}
		out[k] = v
	}
	if len(missing) > 0 {
		t.Skipf("missing required env vars for integration test: %s (see .env.example)", strings.Join(missing, ", "))
	}
	return out
}

// makeSampleSQLite creates a tiny reference-data SQLite file using the
// pure-Go modernc.org/sqlite driver — no sqlite3 CLI required.
func makeSampleSQLite(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "region_notes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		"CREATE TABLE region_notes (region TEXT PRIMARY KEY, note TEXT NOT NULL)",
		"INSERT INTO region_notes (region, note) VALUES ('us-east', 'largest region by revenue')",
		"INSERT INTO region_notes (region, note) VALUES ('eu-west', 'GDPR applies')",
		"INSERT INTO region_notes (region, note) VALUES ('us-west', 'newest region')",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seeding sample sqlite: %v", err)
		}
	}
	return path
}

func TestFourWayFederatedJoin(t *testing.T) {
	requireDocker(t)
	env := requireEnv(t, "FEDCTL_ADMIN_PASSWORD", "SUPERSET_CH_PASSWORD", "SQLALCHEMY_DATABASE_URI")
	root := repoRoot(t)

	binDir := t.TempDir()
	fedctlBin := filepath.Join(binDir, "fedctl")
	build := exec.Command("go", "build", "-o", fedctlBin, "./cmd/fedctl")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building fedctl: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		runDocker(t, root, "down", "--volumes")
	})

	runDocker(t, root, "up", "-d", "--build")
	waitForHealthy(t, root, 5*time.Minute)

	// Seed the sqlite reference table into ch-user-files via the
	// throwaway-container path (spec section 4) — runs on the host,
	// where `docker` and the built fedctl binary both are.
	sqlitePath := makeSampleSQLite(t)
	seed := exec.Command(fedctlBin, "seed", "sqlite", "--from", sqlitePath, "--as", "region_notes.sqlite")
	seed.Dir = root
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("fedctl seed sqlite: %v\n%s", err, out)
	}

	execFedctl(t, root, map[string]any{
		"name": "dev_postgres", "type": "postgres",
		"params": map[string]string{
			"host": "dev-postgres", "port": "5432", "user": "sample", "password": "sample", "database": "sampledb",
		},
	}, "source", "add")

	execFedctl(t, root, map[string]any{
		"name": "dev_mysql", "type": "mysql",
		"params": map[string]string{
			"host": "dev-mysql", "port": "3306", "user": "sample", "password": "sample", "database": "sampledb",
		},
	}, "source", "add")

	execFedctl(t, root, map[string]any{
		"name": "dev_sqlite_ref", "type": "sqlite",
		"params": map[string]string{"path": "region_notes.sqlite"},
	}, "source", "add")

	// Dial the hub directly (test-only published port, see
	// compose.test.yaml) to set up a native table and run the join —
	// four distinct storage backends in one query.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := hub.Open(ctx, hub.DialConfig{
		Host: "localhost", Port: 19000, User: "fed_admin", Password: env["FEDCTL_ADMIN_PASSWORD"],
	}, nil)
	if err != nil {
		t.Fatalf("dialing hub for assertions: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS default.region_managers (region String, manager String) ENGINE = MergeTree ORDER BY region"); err != nil {
		t.Fatalf("creating native table: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO default.region_managers (region, manager) VALUES ('us-east', 'Dana'), ('eu-west', 'Priya'), ('us-west', 'Sam')"); err != nil {
		t.Fatalf("seeding native table: %v", err)
	}

	rows, err := conn.Query(ctx, `
		SELECT c.name, o.amount_cents, rn.note, rm.manager
		FROM fed_dev_postgres.customers AS c
		JOIN fed_dev_mysql.orders AS o ON o.customer_id = c.id
		JOIN fed_dev_sqlite_ref.region_notes AS rn ON rn.region = c.region
		JOIN default.region_managers AS rm ON rm.region = c.region
		ORDER BY c.name, o.id
	`)
	if err != nil {
		t.Fatalf("four-way join query failed: %v", err)
	}
	defer rows.Close()

	type row struct {
		name        string
		amountCents int64
		note        string
		manager     string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.amountCents, &r.note, &r.manager); err != nil {
			t.Fatalf("scanning row: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) == 0 {
		t.Fatal("four-way join returned zero rows — expected sample data from all four backends")
	}
	for _, r := range got {
		if r.name == "" || r.note == "" || r.manager == "" {
			t.Errorf("row missing data from one of the four sources: %+v", r)
		}
	}

	found := map[string]bool{}
	for _, r := range got {
		found[fmt.Sprintf("%s|%d", r.name, r.amountCents)] = true
	}
	if !found["Acme Corp|15000"] || !found["Acme Corp|4200"] {
		t.Errorf("expected Acme Corp's two orders in the joined result, got: %+v", got)
	}
}

func waitForHealthy(t *testing.T, root string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out := runDocker(t, root, "ps", "--format", "json")
		if strings.Count(out, `"Health":"healthy"`) >= 2 || strings.Count(out, "healthy") >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for services to become healthy:\n%s", out)
		}
		time.Sleep(3 * time.Second)
	}
}
