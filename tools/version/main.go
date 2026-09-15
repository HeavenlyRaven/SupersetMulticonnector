// Command version keeps this project's version string in sync.
//
// The VERSION file at the repository root is the single source of truth.
// Several other files have to carry a literal copy of it — compose.yaml
// pins the container image tags an installed copy pulls, and the extension
// manifests declare their own package versions — and a copy that drifts
// out of sync ships users the wrong containers. This tool rewrites them
// all from one place, and verifies they agree.
//
//	go run ./tools/version check        # do all files match VERSION?
//	go run ./tools/version set 0.1.2    # bump VERSION and every copy
//
// It is deliberately Go rather than a shell script, per the project's
// no-shell-scripts rule: this is provisioning tooling, and provisioning
// tooling here is written in Go.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// versionFile holds the authoritative version string.
const versionFile = "VERSION"

// semverRe is deliberately strict: a release version is X.Y.Z and nothing
// else. Pre-release suffixes would have to be taught to every consumer
// below (Debian and RPM disagree about how to order them), so they are
// rejected rather than half-supported.
var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// target is one file carrying a copy of the version. Each pattern must
// capture exactly the version substring in group 1, and must match `want`
// times — a count mismatch means the file was restructured and this tool
// would otherwise silently skip it.
type target struct {
	path string
	re   *regexp.Regexp
	want int
	why  string
}

func targets() []target {
	return []target{
		{
			path: "compose.yaml",
			re:   regexp.MustCompile(`ghcr\.io/heavenlyraven/supersetmulticonnector/(?:clickhouse|superset):([0-9]+\.[0-9]+\.[0-9]+)`),
			want: 2,
			why:  "image tags an installed copy pulls",
		},
		{
			path: filepath.Join("extension", "extension.json"),
			re:   regexp.MustCompile(`"version"\s*:\s*"([0-9]+\.[0-9]+\.[0-9]+)"`),
			want: 1,
			why:  "extension manifest",
		},
		{
			path: filepath.Join("extension", "backend", "pyproject.toml"),
			re:   regexp.MustCompile(`(?m)^version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"`),
			want: 1,
			why:  "extension backend package",
		},
		{
			path: filepath.Join("installer", "windows", "superset-federation.iss"),
			re:   regexp.MustCompile(`#define AppVersion\s+"([0-9]+\.[0-9]+\.[0-9]+)"`),
			want: 1,
			why:  "installer fallback version for local builds",
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = check()
	case "set":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		err = set(os.Args[2])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "version: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/version check | set <x.y.z>")
}

// readVersion returns the trimmed contents of the VERSION file.
func readVersion() (string, error) {
	b, err := os.ReadFile(versionFile)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", versionFile, err)
	}
	v := strings.TrimSpace(string(b))
	if !semverRe.MatchString(v) {
		return "", fmt.Errorf("%s contains %q, which is not an X.Y.Z version", versionFile, v)
	}
	return v, nil
}

// check reports every file whose copy of the version disagrees with
// VERSION, rather than stopping at the first, so one run tells you
// everything that needs fixing.
func check() error {
	want, err := readVersion()
	if err != nil {
		return err
	}

	var problems []string
	for _, t := range targets() {
		found, err := versionsIn(t)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for _, got := range found {
			if got != want {
				problems = append(problems, fmt.Sprintf("%s has %s, want %s (%s)", t.path, got, want, t.why))
			}
		}
	}

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		return fmt.Errorf("%d file(s) out of sync with %s — run `go run ./tools/version set %s`", len(problems), versionFile, want)
	}

	fmt.Printf("all files agree on version %s\n", want)
	return nil
}

// versionsIn returns every version string the target's pattern captures,
// erroring if the match count is not what the target expects.
func versionsIn(t target) ([]string, error) {
	b, err := os.ReadFile(t.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", t.path, err)
	}
	matches := t.re.FindAllStringSubmatch(string(b), -1)
	if len(matches) != t.want {
		return nil, fmt.Errorf("%s: matched %d version(s), expected %d — the file's structure changed and this tool needs updating",
			t.path, len(matches), t.want)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out, nil
}

// set writes the new version to VERSION and rewrites every target.
func set(v string) error {
	if !semverRe.MatchString(v) {
		return fmt.Errorf("%q is not an X.Y.Z version", v)
	}

	// Rewrite the copies first: if one of them is structurally unexpected,
	// fail before VERSION itself has changed, leaving the tree consistent.
	for _, t := range targets() {
		n, err := rewrite(t, v)
		if err != nil {
			return err
		}
		fmt.Printf("  %-46s %d occurrence(s)\n", t.path, n)
	}

	if err := os.WriteFile(versionFile, []byte(v+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", versionFile, err)
	}
	fmt.Printf("  %-46s set to %s\n", versionFile, v)

	fmt.Printf("\nversion is now %s — commit, then tag v%s to release\n", v, v)
	return nil
}

// rewrite replaces just the group-1 span of every match, leaving the rest
// of each line untouched.
func rewrite(t target, v string) (int, error) {
	b, err := os.ReadFile(t.path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", t.path, err)
	}
	content := string(b)

	idx := t.re.FindAllStringSubmatchIndex(content, -1)
	if len(idx) != t.want {
		return 0, fmt.Errorf("%s: matched %d version(s), expected %d — the file's structure changed and this tool needs updating",
			t.path, len(idx), t.want)
	}

	// Walk backwards so earlier offsets stay valid as we splice.
	for i := len(idx) - 1; i >= 0; i-- {
		start, end := idx[i][2], idx[i][3]
		content = content[:start] + v + content[end:]
	}

	if err := os.WriteFile(t.path, []byte(content), 0o644); err != nil {
		return 0, fmt.Errorf("writing %s: %w", t.path, err)
	}
	return len(idx), nil
}
