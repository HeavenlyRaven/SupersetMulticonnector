package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each target's pattern is security-adjacent in the release sense: if one
// stops matching, `set` would silently leave that file on the old version
// and ship users mismatched artifacts. These tests pin the patterns
// against realistic file content.

func TestTargetPatterns_MatchRealFileShapes(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		content string
		want    []string
	}{
		{
			name: "compose.yaml both image tags",
			path: "compose.yaml",
			content: `services:
  clickhouse:
    image: ${FEDERATION_CLICKHOUSE_IMAGE:-ghcr.io/heavenlyraven/supersetmulticonnector/clickhouse:1.2.3}
  superset:
    image: ${FEDERATION_SUPERSET_IMAGE:-ghcr.io/heavenlyraven/supersetmulticonnector/superset:1.2.3}
`,
			want: []string{"1.2.3", "1.2.3"},
		},
		{
			name:    "extension manifest",
			path:    filepath.Join("extension", "extension.json"),
			content: "{\n  \"name\": \"ch-federation\",\n  \"version\": \"1.2.3\",\n  \"license\": \"Apache-2.0\"\n}\n",
			want:    []string{"1.2.3"},
		},
		{
			name:    "pyproject",
			path:    filepath.Join("extension", "backend", "pyproject.toml"),
			content: "[project]\nname = \"acme-ch_federation\"\nversion = \"1.2.3\"\n",
			want:    []string{"1.2.3"},
		},
		{
			name:    "inno setup define",
			path:    filepath.Join("installer", "windows", "superset-federation.iss"),
			content: "#ifndef AppVersion\n  #define AppVersion   \"1.2.3\"\n#endif\n",
			want:    []string{"1.2.3"},
		},
	}

	byPath := map[string]target{}
	for _, tg := range targets() {
		byPath[tg.path] = tg
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg, ok := byPath[c.path]
			if !ok {
				t.Fatalf("no target registered for %s", c.path)
			}
			got := tg.re.FindAllStringSubmatch(c.content, -1)
			if len(got) != len(c.want) {
				t.Fatalf("matched %d version(s), want %d", len(got), len(c.want))
			}
			for i, m := range got {
				if m[1] != c.want[i] {
					t.Errorf("match %d: captured %q, want %q", i, m[1], c.want[i])
				}
			}
			if tg.want != len(c.want) {
				t.Errorf("target.want is %d but this shape yields %d matches", tg.want, len(c.want))
			}
		})
	}
}

// pyproject.toml also contains requires-python = ">=3.10", which must not
// be mistaken for a version assignment — hence the anchored ^version.
func TestPyprojectPattern_IgnoresRequiresPython(t *testing.T) {
	var tg target
	for _, candidate := range targets() {
		if strings.HasSuffix(candidate.path, "pyproject.toml") {
			tg = candidate
		}
	}
	content := "[project]\nversion = \"1.2.3\"\nrequires-python = \">=3.10\"\n"
	got := tg.re.FindAllStringSubmatch(content, -1)
	if len(got) != 1 {
		t.Fatalf("matched %d version(s), want exactly 1 — requires-python must not match", len(got))
	}
	if got[0][1] != "1.2.3" {
		t.Errorf("captured %q, want 1.2.3", got[0][1])
	}
}

func TestSemverRe(t *testing.T) {
	for _, ok := range []string{"0.0.0", "1.2.3", "10.20.30"} {
		if !semverRe.MatchString(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	// Pre-release and build suffixes are rejected on purpose: Debian and
	// RPM order them differently, so supporting them would need per-
	// packager translation rather than one shared string.
	for _, bad := range []string{"", "1.2", "v1.2.3", "1.2.3-rc1", "1.2.3+build", "dev", "1.2.3.4"} {
		if semverRe.MatchString(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// rewrite must touch only the captured span, leaving the surrounding line
// — registry path, quoting, indentation — byte-identical.
func TestRewrite_ReplacesOnlyTheVersionSpan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	original := `    image: ${FEDERATION_CLICKHOUSE_IMAGE:-ghcr.io/heavenlyraven/supersetmulticonnector/clickhouse:0.1.0}
    image: ${FEDERATION_SUPERSET_IMAGE:-ghcr.io/heavenlyraven/supersetmulticonnector/superset:0.1.0}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tg := target{
		path: path,
		re:   regexp.MustCompile(`ghcr\.io/heavenlyraven/supersetmulticonnector/(?:clickhouse|superset):([0-9]+\.[0-9]+\.[0-9]+)`),
		want: 2,
	}
	n, err := rewrite(tg, "9.8.7")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("rewrote %d occurrences, want 2", n)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(original, "0.1.0", "9.8.7")
	if string(got) != want {
		t.Errorf("rewrite changed more than the version:\n got: %q\nwant: %q", got, want)
	}
}

// A file whose structure changed must fail loudly rather than be skipped,
// because a silent skip is exactly how a stale version ships.
func TestRewrite_FailsOnUnexpectedMatchCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("image: somewhere/else:0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tg := target{
		path: path,
		re:   regexp.MustCompile(`ghcr\.io/heavenlyraven/supersetmulticonnector/(?:clickhouse|superset):([0-9]+\.[0-9]+\.[0-9]+)`),
		want: 2,
	}
	if _, err := rewrite(tg, "9.8.7"); err == nil {
		t.Fatal("expected an error when the pattern matches 0 times but 2 are expected")
	}
}

// The real repository must always be self-consistent: this runs the same
// check the release workflow gates on, against the actual files.
func TestRepositoryIsInSync(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	want, err := readVersion()
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets() {
		found, err := versionsIn(tg)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		for _, got := range found {
			if got != want {
				t.Errorf("%s has version %s, but VERSION says %s", tg.path, got, want)
			}
		}
	}
}
