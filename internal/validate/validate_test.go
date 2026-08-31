package validate

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/acme/superset-federation/internal/jsonio"
)

func errCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	je := jsonio.AsError(err)
	return je.Info.Code
}

func TestSourceName(t *testing.T) {
	valid := []string{"abc", "fed_sales", "a12", "source_1"}
	for _, name := range valid {
		if err := SourceName(name); err != nil {
			t.Errorf("SourceName(%q): expected valid, got %v", name, err)
		}
	}

	invalid := []string{
		"",
		"AB",                         // uppercase
		"1abc",                       // must start with a letter
		"ab",                         // too short (min 3 total: 1 + {2,40})
		"foo; DROP DATABASE default", // injection attempt from acceptance criteria
		"foo-bar",                    // hyphen not allowed
		"foo.bar",                    // dot not allowed
		"foo bar",                    // space not allowed
		"foo`bar",                    // backtick not allowed
		"foo'bar",                    // quote not allowed
		string(make([]byte, 50)),     // way too long / null bytes
	}
	for _, name := range invalid {
		if err := SourceName(name); err == nil {
			t.Errorf("SourceName(%q): expected rejection, got nil error", name)
		} else if code := errCode(t, err); code != jsonio.CodeInvalidName {
			t.Errorf("SourceName(%q): expected code %s, got %s", name, jsonio.CodeInvalidName, code)
		}
	}
}

func TestQuoteIdentifier(t *testing.T) {
	got := QuoteIdentifier("fed_sales")
	if got != "`fed_sales`" {
		t.Errorf("QuoteIdentifier: got %q", got)
	}
	got = QuoteIdentifier("weird`name")
	if got != "`weird``name`" {
		t.Errorf("QuoteIdentifier embedded backtick: got %q", got)
	}
}

func TestPort(t *testing.T) {
	for _, p := range []int{1, 80, 5432, 65535} {
		if err := Port(p); err != nil {
			t.Errorf("Port(%d): expected valid, got %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536, 100000} {
		if err := Port(p); err == nil {
			t.Errorf("Port(%d): expected rejection", p)
		} else if code := errCode(t, err); code != jsonio.CodeInvalidPort {
			t.Errorf("Port(%d): expected code %s, got %s", p, jsonio.CodeInvalidPort, code)
		}
	}
}

// fakeResolver lets tests control DNS resolution without a network.
type fakeResolver struct {
	answers map[string][]net.IPAddr
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	addrs, ok := f.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

func ipAddr(s string) net.IPAddr { return net.IPAddr{IP: net.ParseIP(s)} }

func TestHost_DeniedRanges(t *testing.T) {
	cases := []struct {
		name string
		host string
		ip   string
	}{
		{"loopback-v4", "loop.internal", "127.0.0.1"},
		{"loopback-v6", "loop6.internal", "::1"},
		{"link-local", "metadata.internal", "169.254.169.254"}, // cloud metadata endpoint
		{"rfc1918-10", "priv10.internal", "10.1.2.3"},
		{"rfc1918-172", "priv172.internal", "172.20.0.5"},
		{"rfc1918-192", "priv192.internal", "192.168.1.1"},
		{"ula-v6", "ula.internal", "fd00::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolver := fakeResolver{answers: map[string][]net.IPAddr{c.host: {ipAddr(c.ip)}}}
			_, err := Host(context.Background(), resolver, c.host, nil)
			if err == nil {
				t.Fatalf("Host(%s -> %s): expected rejection, got nil", c.host, c.ip)
			}
			if code := errCode(t, err); code != jsonio.CodeHostNotAllowed {
				t.Errorf("expected code %s, got %s", jsonio.CodeHostNotAllowed, code)
			}
		})
	}
}

func TestHost_PublicAllowed(t *testing.T) {
	resolver := fakeResolver{answers: map[string][]net.IPAddr{"db.example.com": {ipAddr("203.0.113.10")}}}
	ips, err := Host(context.Background(), resolver, "db.example.com", nil)
	if err != nil {
		t.Fatalf("expected public host allowed, got %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("203.0.113.10")) {
		t.Errorf("unexpected ips: %v", ips)
	}
}

func TestHost_MultiAddressRejectsIfAnyDenied(t *testing.T) {
	resolver := fakeResolver{answers: map[string][]net.IPAddr{
		"mixed.internal": {ipAddr("203.0.113.10"), ipAddr("10.0.0.5")},
	}}
	_, err := Host(context.Background(), resolver, "mixed.internal", nil)
	if err == nil {
		t.Fatal("expected rejection when any resolved address is denied")
	}
}

func TestHost_AllowlistOverridesByHostname(t *testing.T) {
	resolver := fakeResolver{answers: map[string][]net.IPAddr{"dev-postgres": {ipAddr("172.20.0.5")}}}
	allow := NewAllowlist([]string{"dev-postgres"})
	if _, err := Host(context.Background(), resolver, "dev-postgres", allow); err != nil {
		t.Fatalf("expected allowlisted hostname to pass, got %v", err)
	}
}

func TestHost_AllowlistOverridesByCIDR(t *testing.T) {
	resolver := fakeResolver{answers: map[string][]net.IPAddr{"dev-postgres": {ipAddr("172.20.0.5")}}}
	allow := NewAllowlist([]string{"172.20.0.0/16"})
	if _, err := Host(context.Background(), resolver, "dev-postgres", allow); err != nil {
		t.Fatalf("expected CIDR-allowlisted address to pass, got %v", err)
	}
	// A different host in a non-allowlisted private range must still be denied.
	resolver2 := fakeResolver{answers: map[string][]net.IPAddr{"other.internal": {ipAddr("172.30.0.5")}}}
	if _, err := Host(context.Background(), resolver2, "other.internal", allow); err == nil {
		t.Fatal("expected non-allowlisted address to still be denied")
	}
}

func TestHost_NXDOMAIN(t *testing.T) {
	resolver := fakeResolver{answers: map[string][]net.IPAddr{}}
	if _, err := Host(context.Background(), resolver, "does-not-resolve.invalid", nil); err == nil {
		t.Fatal("expected rejection for a host that does not resolve")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSQLitePath_Valid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.sqlite"), "x")
	resolved, err := SQLitePath(dir, "app.sqlite")
	if err != nil {
		t.Fatalf("expected valid path, got %v", err)
	}
	if filepath.Base(resolved) != "app.sqlite" {
		t.Errorf("unexpected resolved path: %s", resolved)
	}
}

func TestSQLitePath_Traversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := SQLitePath(dir, "../../etc/passwd"); err == nil {
		t.Fatal("expected traversal to be rejected")
	} else if code := errCode(t, err); code != jsonio.CodeInvalidPath {
		t.Errorf("expected code %s, got %s", jsonio.CodeInvalidPath, code)
	}
}

func TestSQLitePath_AbsoluteEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.sqlite"), "x")
	if _, err := SQLitePath(dir, filepath.Join(outside, "secret.sqlite")); err == nil {
		t.Fatal("expected absolute path outside user_files to be rejected")
	}
}

func TestSQLitePath_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := SQLitePath(dir, "nope.sqlite"); err == nil {
		t.Fatal("expected missing file to be rejected")
	}
}

func TestSQLitePath_NotRegularFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := SQLitePath(dir, "adir"); err == nil {
		t.Fatal("expected a directory to be rejected as not a regular file")
	}
}

func TestSQLitePath_EscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically requires elevated privileges on Windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.sqlite"), "x")
	link := filepath.Join(dir, "escape.sqlite")
	if err := os.Symlink(filepath.Join(outside, "secret.sqlite"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := SQLitePath(dir, "escape.sqlite"); err == nil {
		t.Fatal("expected an escaping symlink to be rejected")
	} else if code := errCode(t, err); code != jsonio.CodeInvalidPath {
		t.Errorf("expected code %s, got %s", jsonio.CodeInvalidPath, code)
	}
}

func TestCredentialFlagName(t *testing.T) {
	yes := []string{
		"--password", "-p", "password", "--db-password", "--secret", "--api-token-secret", "PASSWORD", "--pwd",
		// Gaps found by adversarial review: realistic spellings a
		// developer or user might actually type.
		"--db-pass", "--dbpass", "--dbpwd", "--userpwd", "--auth-token",
		"--authtoken", "--api-key", "--apikey", "--pgpassword", "--mysql-pwd",
		"--client-secret",
	}
	for _, f := range yes {
		if !CredentialFlagName(f) {
			t.Errorf("CredentialFlagName(%q): expected true", f)
		}
	}
	no := []string{"--host", "--port", "--name", "--database", "--dev", "--path", "--schema", "--timeout", "--file", "-f"}
	for _, f := range no {
		if CredentialFlagName(f) {
			t.Errorf("CredentialFlagName(%q): expected false", f)
		}
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor("hunter2", "")
	got := r.Redact("connecting with password hunter2 to host")
	if got != "connecting with password *** to host" {
		t.Errorf("Redact: got %q", got)
	}
}
