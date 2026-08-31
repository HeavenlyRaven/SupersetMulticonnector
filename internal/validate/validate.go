// Package validate implements the security-critical input checks from
// spec section 9: source names, hosts (SSRF), ports, and SQLite paths.
// Every exported function here must be unit tested per spec section 13.
package validate

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/acme/superset-federation/internal/jsonio"
)

var sourceNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,40}$`)

// SourceName enforces ^[a-z][a-z0-9_]{2,40}$. Source names are used as
// ClickHouse database and named-collection identifiers, so this regex is
// the sole gate against identifier injection — it is deliberately far
// stricter than what ClickHouse itself allows.
func SourceName(name string) error {
	if !sourceNameRe.MatchString(name) {
		return jsonio.NewError(jsonio.CodeInvalidName,
			fmt.Sprintf("invalid source name %q", name),
			"names must match ^[a-z][a-z0-9_]{2,40}$")
	}
	return nil
}

// QuoteIdentifier backtick-quotes a ClickHouse identifier. Callers must
// only pass identifiers that already passed SourceName; this is defense in
// depth, not a substitute for that validation — identifiers can never be
// bound as query parameters, so this plus SourceName's regex is the whole
// defense against identifier injection.
func QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Port enforces the 1-65535 range.
func Port(port int) error {
	if port < 1 || port > 65535 {
		return jsonio.NewError(jsonio.CodeInvalidPort,
			fmt.Sprintf("port %d out of range 1-65535", port), "")
	}
	return nil
}

var deniedCIDRs = mustParseCIDRs(
	"127.0.0.0/8",
	"::1/128",
	"169.254.0.0/16",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fd00::/8",
)

func mustParseCIDRs(specs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, len(specs))
	for i, s := range specs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("validate: bad denied CIDR " + s + ": " + err.Error())
		}
		nets[i] = n
	}
	return nets
}

func denied(ip net.IP) bool {
	for _, n := range deniedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolver is the subset of *net.Resolver that Host needs, so tests can
// fake DNS resolution without a network.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var DefaultResolver Resolver = net.DefaultResolver

// Allowlist holds host and address exceptions to the denied ranges.
// Entries may be exact hostnames, single IPs, or CIDRs; this lets an
// operator permit e.g. a docker-network hostname or subnet used by the
// compose.dev.yaml sample sources without weakening the default-deny rule
// for everyone else.
type Allowlist struct {
	hosts map[string]bool
	nets  []*net.IPNet
}

func NewAllowlist(entries []string) *Allowlist {
	al := &Allowlist{hosts: map[string]bool{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			al.nets = append(al.nets, n)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			mask := net.CIDRMask(bits, bits)
			al.nets = append(al.nets, &net.IPNet{IP: ip.Mask(mask), Mask: mask})
			continue
		}
		al.hosts[strings.ToLower(e)] = true
	}
	return al
}

func (al *Allowlist) allowsHost(host string) bool {
	if al == nil {
		return false
	}
	return al.hosts[strings.ToLower(host)]
}

func (al *Allowlist) allowsIP(ip net.IP) bool {
	if al == nil {
		return false
	}
	for _, n := range al.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Host resolves host and rejects it if host itself, or any resolved
// address, falls in a denied private/loopback/link-local range — unless
// the allowlist explicitly permits that hostname or address. It returns
// the resolved addresses on success.
//
// Callers MUST call Host again immediately before opening the connection,
// using a fresh Resolver call (not the addresses returned here) — DNS can
// change between admin-time validation and connection time, and skipping
// the second check turns this from an SSRF guard into a TOCTOU bypass.
func Host(ctx context.Context, resolver Resolver, host string, allow *Allowlist) ([]net.IP, error) {
	if host == "" {
		return nil, jsonio.NewError(jsonio.CodeHostNotAllowed, "host must not be empty", "")
	}
	if resolver == nil {
		resolver = DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, jsonio.NewError(jsonio.CodeHostNotAllowed,
			fmt.Sprintf("host %q does not resolve", host), err.Error())
	}
	if len(addrs) == 0 {
		return nil, jsonio.NewError(jsonio.CodeHostNotAllowed,
			fmt.Sprintf("host %q resolved to no addresses", host), "")
	}
	hostAllowed := allow.allowsHost(host)
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
		if hostAllowed || allow.allowsIP(a.IP) {
			continue
		}
		if denied(a.IP) {
			return nil, jsonio.NewError(jsonio.CodeHostNotAllowed,
				fmt.Sprintf("host %q resolves to %s, which is in a private/link-local/loopback range", host, a.IP),
				"add the host or address to the source-host allowlist if this is intentional")
		}
	}
	return ips, nil
}

// SQLitePath validates that requested, once cleaned and joined against
// userFilesDir and with all symlinks resolved, names a regular file inside
// userFilesDir. It returns the resolved absolute path on success. This
// rejects both directory traversal ("../../etc/passwd") and a symlink
// planted inside userFilesDir that escapes it.
func SQLitePath(userFilesDir, requested string) (string, error) {
	if requested == "" {
		return "", jsonio.NewError(jsonio.CodeInvalidPath, "sqlite path must not be empty", "")
	}
	if strings.ContainsRune(requested, 0) {
		return "", jsonio.NewError(jsonio.CodeInvalidPath, "sqlite path contains a NUL byte", "")
	}
	base, err := filepath.Abs(filepath.Clean(userFilesDir))
	if err != nil {
		return "", jsonio.NewError(jsonio.CodeInternal, "cannot resolve user_files directory", "")
	}
	candidate := filepath.Clean(filepath.Join(base, requested))
	if err := withinDir(base, candidate); err != nil {
		return "", err
	}

	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", jsonio.NewError(jsonio.CodeInternal, "cannot resolve user_files directory", err.Error())
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", jsonio.NewError(jsonio.CodeInvalidPath,
			fmt.Sprintf("sqlite path %q does not exist", requested),
			"seed the file first with `fedctl seed sqlite`")
	}
	if err := withinDir(resolvedBase, resolved); err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", jsonio.NewError(jsonio.CodeInvalidPath, fmt.Sprintf("sqlite path %q does not exist", requested), "")
	}
	if !info.Mode().IsRegular() {
		return "", jsonio.NewError(jsonio.CodeInvalidPath, fmt.Sprintf("sqlite path %q is not a regular file", requested), "")
	}
	return resolved, nil
}

func withinDir(base, target string) error {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return jsonio.NewError(jsonio.CodeInvalidPath, "path escapes the user_files directory", "")
	}
	return nil
}

// credentialFlagNames catches exact, whole-flag spellings — including
// single-letter conventions borrowed from mysql/psql (-p for password).
var credentialFlagNames = map[string]bool{
	"password": true, "pass": true, "pwd": true, "passwd": true,
	"secret": true, "token": true, "apikey": true, "credential": true,
	"credentials": true, "p": true,
}

// credentialSubstrings catches compound flag names a developer might
// plausibly invent (--db-pass, --auth-token, --userpwd, --client-secret)
// by matching after stripping hyphens, so hyphen placement can't dodge
// the check.
var credentialSubstrings = []string{"password", "passwd", "pass", "pwd", "secret", "token", "apikey", "credential"}

// CredentialFlagName reports whether a CLI flag name looks like it is
// meant to carry a secret. Section 9 requires fedctl to reject any
// credential-shaped flag outright, since argv is world-readable via /proc.
// This is a defense-in-depth backstop, not the primary guarantee: no
// fedctl command registers a credential-named flag in the first place —
// passwords travel only via stdin/JSON (see cmd/fedctl/source.go).
func CredentialFlagName(flagName string) bool {
	f := strings.ToLower(strings.TrimLeft(flagName, "-"))
	f = strings.ReplaceAll(f, "_", "-")
	if credentialFlagNames[f] {
		return true
	}
	stripped := strings.ReplaceAll(f, "-", "")
	for _, sub := range credentialSubstrings {
		if strings.Contains(stripped, sub) {
			return true
		}
	}
	return false
}

// Redactor scrubs known secret values from arbitrary text. Secrets are
// designed to never reach a loggable string in the first place — they
// travel as bind parameters, never interpolated into DDL text — but this
// is defense in depth for panic recovery and unexpected error paths.
type Redactor struct {
	secrets []string
}

func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if s != "" {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

func (r *Redactor) Redact(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}
