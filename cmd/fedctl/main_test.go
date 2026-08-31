package main

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/acme/superset-federation/internal/jsonio"
)

// TestPrintCLIError_PlainErrorsAreNotMangled is a regression test: an
// earlier version routed every printCLIError call through jsonio.AsError,
// whose fallback silently replaces any error that isn't a *jsonio.Error
// with the generic string "internal error" — appropriate for a --json API
// response (a different trust boundary) but useless for a plain CLI
// error a human operator needs to actually troubleshoot (e.g. a docker
// command failure). Plain errors must print their real message; a
// genuine jsonio.Error must still print its Message/Hint.
func TestPrintCLIError_PlainErrorsAreNotMangled(t *testing.T) {
	captured := captureStderr(t, func() {
		printCLIError(errors.New("docker compose up failed: exit status 1"))
	})
	if !bytes.Contains(captured, []byte("docker compose up failed: exit status 1")) {
		t.Errorf("expected the real error message, got: %q", captured)
	}
	if bytes.Contains(captured, []byte("internal error")) {
		t.Errorf("plain error was mangled into a generic message: %q", captured)
	}
}

func TestPrintCLIError_JsonioErrorShowsMessageAndHint(t *testing.T) {
	err := jsonio.NewError(jsonio.CodeHostNotAllowed, "host is denied", "add it to the allowlist")
	captured := captureStderr(t, func() { printCLIError(err) })
	if !bytes.Contains(captured, []byte("host is denied")) || !bytes.Contains(captured, []byte("add it to the allowlist")) {
		t.Errorf("expected message and hint both present, got: %q", captured)
	}
}

func captureStderr(t *testing.T, fn func()) []byte {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.Bytes()
}

// TestGuardCredentialFlags exercises the actual argv-parsing wired into
// main() (main.go:27), not just the underlying validate.CredentialFlagName
// predicate — a prior review noted no committed test covered this
// argv-splitting logic directly.
func TestGuardCredentialFlags(t *testing.T) {
	rejected := [][]string{
		{"source", "add", "--password", "hunter2"},
		{"source", "add", "--password=hunter2"},
		{"source", "test", "-p", "hunter2"},
		{"source", "add", "--db-pass", "x"},
		{"source", "add", "--auth-token=abc"},
	}
	for _, args := range rejected {
		if err := guardCredentialFlags(args); err == nil {
			t.Errorf("guardCredentialFlags(%v): expected rejection", args)
		}
	}

	allowed := [][]string{
		{"version"},
		{"source", "list"},
		{"source", "add", "--name", "fed_sales", "--type", "postgres", "--host", "h"},
		{"apply", "-f", "config/sources.yaml"},
		{"seed", "sqlite", "--from", "./app.sqlite", "--as", "app.sqlite"},
		{"preflight", "--skip-images"},
	}
	for _, args := range allowed {
		if err := guardCredentialFlags(args); err != nil {
			t.Errorf("guardCredentialFlags(%v): expected no rejection, got %v", args, err)
		}
	}
}
