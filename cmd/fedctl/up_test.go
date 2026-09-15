package main

import (
	"os"
	"path/filepath"
	"testing"
)

// chdir moves into dir for the duration of the test, restoring the old
// working directory afterwards. resolveBuildMode inspects relative paths,
// so the tests below need control over what "here" means.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// writeDockerfiles creates the two files resolveBuildMode looks for.
func writeDockerfiles(t *testing.T, root string) {
	t.Helper()
	for _, rel := range dockerfilePaths {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveBuildMode_ExplicitFlagsWin(t *testing.T) {
	// An empty directory: auto would say "don't build", but the explicit
	// modes must not consult the filesystem at all.
	chdir(t, t.TempDir())

	if !resolveBuildMode(buildAlways) {
		t.Error("buildAlways must build even with no Dockerfiles present")
	}
	if resolveBuildMode(buildNever) {
		t.Error("buildNever must not build")
	}
}

func TestResolveBuildMode_AutoBuildsInASourceCheckout(t *testing.T) {
	dir := t.TempDir()
	writeDockerfiles(t, dir)
	chdir(t, dir)

	if !resolveBuildMode(buildAuto) {
		t.Error("expected auto to build when both Dockerfiles are present (a source checkout)")
	}
}

func TestResolveBuildMode_AutoPullsInAnInstalledCopy(t *testing.T) {
	// An installed copy — from the Windows installer or a .deb — ships
	// compose.yaml and the ClickHouse config but no images/ directory,
	// so there is nothing to build and the published image must be used.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	if resolveBuildMode(buildAuto) {
		t.Error("expected auto to pull when images/ is absent (an installed copy)")
	}
}

func TestResolveBuildMode_AutoPullsWhenOnlyOneDockerfileExists(t *testing.T) {
	// A half-populated directory is not a usable source checkout: building
	// would fail partway. Default-deny is the safer reading.
	dir := t.TempDir()
	writeDockerfiles(t, dir)
	if err := os.Remove(filepath.Join(dir, dockerfilePaths[0])); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	if resolveBuildMode(buildAuto) {
		t.Error("expected auto to pull when only one of the two Dockerfiles is present")
	}
}
