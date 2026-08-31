// Package preflight implements `fedctl preflight`: host-environment checks
// that must pass before `fedctl up` is attempted. Every check is
// independent and all are run, so one failure doesn't hide the next.
package preflight

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/acme/superset-federation/internal/config"
)

// Result is one check's outcome.
type Result struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// PinnedImage is one image reference preflight verifies has both required
// platform manifests, e.g. "apache/superset:6.1.0".
type PinnedImage struct {
	Name string
	Ref  string
}

// Options configures which checks Run performs. Image manifest checks and
// the metadata-DB reachability check are optional because `fedctl
// preflight` in a minimal/offline dev loop shouldn't hard-require network
// access to Docker Hub — callers that need the full production gate (CI,
// `fedctl up`) pass PinnedImages and MetadataURI.
type Options struct {
	PinnedImages []PinnedImage
	MetadataURI  string
}

// Run executes every check and returns all results — never stops early —
// plus a bool reporting whether every check passed.
func Run(ctx context.Context, opts Options) ([]Result, bool) {
	var results []Result
	results = append(results, checkComposeV2(ctx))
	results = append(results, checkBuildx(ctx))
	for _, img := range opts.PinnedImages {
		results = append(results, checkImageManifests(ctx, img))
	}
	results = append(results, checkCgroupVersion())
	results = append(results, checkSELinux())
	results = append(results, diskAndMemoryChecks()...)
	if opts.MetadataURI != "" {
		results = append(results, checkMetadataURIShape(opts.MetadataURI))
		results = append(results, checkPostgresReachable(ctx, opts.MetadataURI))
	}

	ok := true
	for _, r := range results {
		if !r.OK {
			ok = false
		}
	}
	return results, ok
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func checkComposeV2(ctx context.Context) Result {
	// "docker compose" (space-separated subcommand) only exists in the v2
	// CLI plugin — v1 was only ever reachable via the hyphenated
	// "docker-compose" binary — so success here already implies v2.
	out, err := runCmd(ctx, "docker", "compose", "version")
	if err == nil {
		return Result{Name: "compose-v2", OK: true, Message: strings.TrimSpace(out)}
	}
	// docker compose (v2 plugin) failed — check for a v1 docker-compose
	// binary specifically, so the error message is actionable rather than
	// just "docker compose not found".
	if legacyOut, legacyErr := runCmd(ctx, "docker-compose", "--version"); legacyErr == nil {
		return Result{
			Name: "compose-v2", OK: false,
			Message: "found Compose v1 (`docker-compose`): " + strings.TrimSpace(legacyOut),
			Hint:    "this stack requires Compose v2 (`docker compose`, no hyphen) — upgrade Docker Desktop or install the compose-plugin package",
		}
	}
	return Result{
		Name: "compose-v2", OK: false,
		Message: "`docker compose version` failed: " + strings.TrimSpace(out),
		Hint:    "install Docker with the Compose v2 plugin (`docker compose version` should print a v2.x version)",
	}
}

func checkBuildx(ctx context.Context) Result {
	out, err := runCmd(ctx, "docker", "buildx", "version")
	if err != nil {
		return Result{
			Name: "buildx", OK: false,
			Message: "`docker buildx version` failed: " + strings.TrimSpace(out),
			Hint:    "install the docker-buildx-plugin, or use a Docker Desktop version that bundles it",
		}
	}
	return Result{Name: "buildx", OK: true, Message: strings.TrimSpace(out)}
}

// checkImageManifests shells out to `docker buildx imagetools inspect`,
// which handles registry auth via the caller's existing docker config —
// reimplementing OCI registry auth in Go would be a lot of surface for no
// benefit here. This is not a shell script: argv is passed as a slice,
// never through a shell interpreter.
func checkImageManifests(ctx context.Context, img PinnedImage) Result {
	out, err := runCmd(ctx, "docker", "buildx", "imagetools", "inspect", img.Ref)
	if err != nil {
		return Result{
			Name: "image-manifest:" + img.Name, OK: false,
			Message: fmt.Sprintf("could not inspect %s: %s", img.Ref, strings.TrimSpace(out)),
			Hint:    "check network access to the registry and that the pinned tag+digest in compose.yaml still exists",
		}
	}
	missing := []string{}
	for _, plat := range []string{"linux/amd64", "linux/arm64"} {
		if !strings.Contains(out, plat) {
			missing = append(missing, plat)
		}
	}
	if len(missing) > 0 {
		return Result{
			Name: "image-manifest:" + img.Name, OK: false,
			Message: fmt.Sprintf("%s is missing platform manifest(s): %s", img.Ref, strings.Join(missing, ", ")),
			Hint:    "re-pin to a tag+digest that publishes both linux/amd64 and linux/arm64",
		}
	}
	return Result{Name: "image-manifest:" + img.Name, OK: true, Message: img.Ref + ": linux/amd64 and linux/arm64 present"}
}

func checkMetadataURIShape(uri string) Result {
	if err := config.ValidateMetadataURI(uri); err != nil {
		return Result{Name: "metadata-uri", OK: false, Message: err.Error(),
			Hint: "set SQLALCHEMY_DATABASE_URI to a postgresql:// URI for the external metadata database"}
	}
	return Result{Name: "metadata-uri", OK: true, Message: "SQLALCHEMY_DATABASE_URI is a postgresql:// URI"}
}

func checkPostgresReachable(ctx context.Context, uri string) Result {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		return Result{Name: "postgres-reachable", OK: false,
			Message: "cannot connect to the external PostgreSQL metadata database: " + err.Error(),
			Hint:    "check SQLALCHEMY_DATABASE_URI, network access, and that the database accepts connections from this host"}
	}
	defer conn.Close(ctx)
	if err := conn.Ping(ctx); err != nil {
		return Result{Name: "postgres-reachable", OK: false, Message: "connected but ping failed: " + err.Error()}
	}
	return Result{Name: "postgres-reachable", OK: true, Message: "external PostgreSQL metadata database is reachable"}
}
