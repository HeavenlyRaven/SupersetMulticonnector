package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# a comment\n\nFOO=bar\nQUOTED=\"has spaces\"\nSINGLE='also quoted'\nMALFORMED\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"FOO", "QUOTED", "SINGLE", "ALREADY_SET"} {
		os.Unsetenv(k)
	}
	t.Setenv("ALREADY_SET", "from-shell")

	loadDotEnv(path)
	defer os.Unsetenv("FOO")
	defer os.Unsetenv("QUOTED")
	defer os.Unsetenv("SINGLE")

	if v := os.Getenv("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want bar", v)
	}
	if v := os.Getenv("QUOTED"); v != "has spaces" {
		t.Errorf("QUOTED = %q, want %q", v, "has spaces")
	}
	if v := os.Getenv("SINGLE"); v != "also quoted" {
		t.Errorf("SINGLE = %q, want %q", v, "also quoted")
	}
}

func TestLoadDotEnv_DoesNotOverrideExplicitlySetVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("ALREADY_SET=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALREADY_SET", "from-shell")

	loadDotEnv(path)

	if v := os.Getenv("ALREADY_SET"); v != "from-shell" {
		t.Errorf("expected an explicitly-set shell variable to win over .env, got %q", v)
	}
}

func TestLoadDotEnv_MissingFileIsNotAnError(t *testing.T) {
	// Must not panic or otherwise fail — most commands (e.g. `version`)
	// don't need a .env at all.
	loadDotEnv(filepath.Join(t.TempDir(), "does-not-exist.env"))
}
