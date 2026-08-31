package sources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_All(t *testing.T) {
	types := All()
	names := map[string]bool{}
	for _, ty := range types {
		names[ty.Name()] = true
	}
	for _, want := range []string{"postgres", "mysql", "sqlite"} {
		if !names[want] {
			t.Errorf("expected registered type %q, got %v", want, names)
		}
	}
}

func TestRegistry_Get(t *testing.T) {
	if _, ok := Get("postgres"); !ok {
		t.Error("expected postgres to be registered")
	}
	if _, ok := Get("mssql"); ok {
		t.Error("expected mssql to be unregistered")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	if _, _, err := Validate(context.Background(), "mssql", "", Params{}); err == nil {
		t.Fatal("expected rejection of unknown source type")
	}
}

func TestPostgresType_Validate(t *testing.T) {
	ty, _ := Get("postgres")
	out, err := ty.Validate("", Params{"host": "h", "port": "5432", "user": "u", "password": "p", "database": "d"})
	if err != nil {
		t.Fatal(err)
	}
	if out["schema"] != "public" {
		t.Errorf("expected default schema public, got %q", out["schema"])
	}
	if _, err := ty.Validate("", Params{"host": "h"}); err == nil {
		t.Fatal("expected rejection of incomplete params")
	}
	if _, err := ty.Validate("", Params{"host": "h", "port": "not-a-port", "user": "u", "password": "p", "database": "d"}); err == nil {
		t.Fatal("expected rejection of bad port")
	}
}

func TestMySQLType_Validate(t *testing.T) {
	ty, _ := Get("mysql")
	if _, err := ty.Validate("", Params{"host": "h", "port": "3306", "user": "u", "password": "p", "database": "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ty.Validate("", Params{}); err == nil {
		t.Fatal("expected rejection of empty params")
	}
}

func TestSQLiteType_Validate(t *testing.T) {
	ty, _ := Get("sqlite")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.sqlite"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ty.Validate(dir, Params{"path": "app.sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(out["path"]) != "app.sqlite" {
		t.Errorf("unexpected resolved path: %s", out["path"])
	}
	if _, err := ty.Validate(dir, Params{"path": "../../etc/passwd"}); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := ty.Validate(dir, Params{"path": "nope.sqlite"}); err == nil {
		t.Fatal("expected missing-file rejection")
	}
}
