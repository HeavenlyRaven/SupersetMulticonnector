package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateMetadataURI(t *testing.T) {
	bad := []string{
		"",
		"sqlite:////app/superset_home/superset.db",
		"SQLite:///tmp/x.db",
		"mysql://user:pass@host/db",
	}
	for _, uri := range bad {
		if err := ValidateMetadataURI(uri); err == nil {
			t.Errorf("ValidateMetadataURI(%q): expected rejection", uri)
		}
	}
	good := []string{
		"postgresql://user:pass@postgres.internal:5432/superset",
		"postgresql+psycopg2://user:pass@postgres.internal:5432/superset",
	}
	for _, uri := range good {
		if err := ValidateMetadataURI(uri); err != nil {
			t.Errorf("ValidateMetadataURI(%q): expected valid, got %v", uri, err)
		}
	}
}

func TestSourceSpec_ResolveParams(t *testing.T) {
	t.Setenv("TEST_SRC_PW", "hunter2")
	s := SourceSpec{Name: "s1", Type: "postgres", Host: "h", Port: 5432, User: "u", PasswordEnv: "TEST_SRC_PW", Database: "d"}
	p, err := s.ResolveParams()
	if err != nil {
		t.Fatal(err)
	}
	if p["password"] != "hunter2" || p["host"] != "h" || p["port"] != "5432" {
		t.Errorf("unexpected params: %+v", p)
	}
}

func TestSourceSpec_ResolveParams_MissingEnv(t *testing.T) {
	os.Unsetenv("TEST_SRC_PW_MISSING")
	s := SourceSpec{Name: "s1", PasswordEnv: "TEST_SRC_PW_MISSING"}
	if _, err := s.ResolveParams(); err == nil {
		t.Fatal("expected error for missing password env var")
	}
}

func TestLoadSourcesFile_RejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	content := `
sources:
  - name: dup
    type: postgres
  - name: dup
    type: mysql
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSourcesFile(path); err == nil {
		t.Fatal("expected rejection of duplicate source names")
	}
}

func TestLoadSourcesFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.yaml")
	content := `
sources:
  - name: fed_sales
    type: postgres
    host: pg.internal
    port: 5432
    user: svc
    password_env: FED_SALES_PASSWORD
    database: salesdb
  - name: ref_data
    type: sqlite
    path: app.sqlite
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sf, err := LoadSourcesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sf.Sources))
	}
}
