# 5. The internal Go packages

This is where the real work happens. Read the packages in this order — each
depends on the ones before it.

```
jsonio      ← the error/response vocabulary everything else speaks
   ↑
validate    ← the security checks
   ↑
ddl         ← SQL generation (pure strings, executes nothing)
   ↑
hub         ← the only code that talks to ClickHouse
   ↑
sources     ← the pluggable source types
   ↑
config, health, preflight   ← supporting cast
```

---

## `internal/jsonio` — the contract with Python

[jsonio.go](../internal/jsonio/jsonio.go) defines how `fedctl` and the Python
shim communicate. It is 125 lines and it shapes every other package, because
almost every function in this codebase returns one of its errors.

### The envelope

Every `--json` command writes exactly one JSON object to stdout:

```json
{"ok": true,  "data": {"name": "sales", "type": "postgres"}}
{"ok": false, "error": {"code": "HOST_NOT_ALLOWED", "message": "...", "hint": "..."}}
```

```go
type Response struct {
    OK    bool       `json:"ok"`
    Data  any        `json:"data,omitempty"`
    Error *ErrorInfo `json:"error,omitempty"`
}
```

`omitempty` means the unused field disappears rather than appearing as
`null`. `any` is Go's name for "a value of any type" (it is an alias for
`interface{}`).

### Error codes

```go
const (
    CodeInvalidRequest    = "INVALID_REQUEST"
    CodeHostNotAllowed    = "HOST_NOT_ALLOWED"
    CodeInvalidPath       = "INVALID_PATH"
    CodeSourceExists      = "SOURCE_EXISTS"
    CodeSourceNotFound    = "SOURCE_NOT_FOUND"
    CodeConnectionFailed  = "CONNECTION_FAILED"
    CodeHubUnavailable    = "HUB_UNAVAILABLE"
    CodeInternal          = "INTERNAL_ERROR"
    ...
)
```

These strings are a **public interface between two languages**. The Python
shim has a matching dictionary that turns each into an HTTP status:

```python
ERROR_STATUS = {
    "HOST_NOT_ALLOWED": 400,
    "SOURCE_EXISTS":    409,
    "SOURCE_NOT_FOUND": 404,
    "CONNECTION_FAILED": 502,
    ...
}
```

Change a code string in Go without changing Python and the mapping silently
falls back to 500. The two files are joined at the hip; the comment in each
says so.

### Errors that carry a code

```go
type Error struct {
    Info ErrorInfo
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Info.Code, e.Info.Message) }
```

Defining an `Error() string` method is all it takes to satisfy Go's built-in
`error` interface. So a `*jsonio.Error` can be returned by any function that
returns `error`, travel up through ordinary Go error handling, and still be
recognised at the top and turned back into a structured response.

Each error has three parts, and the third is the humane one:

- `Code` — for machines.
- `Message` — what went wrong.
- `Hint` — what to do about it.

For example: *"host resolves to 10.1.2.3, which is in a private range"* with
the hint *"add the host or address to the source-host allowlist if this is
intentional"*. The user sees both, in the CLI and in the browser.

### `AsError` and the trust boundary

```go
func AsError(err error) *Error {
    var je *Error
    if ok := asError(err, &je); ok { return je }
    return NewError(CodeInternal, "internal error", "")
}
```

If the error is not one of ours, it is replaced with a generic message. That
is deliberate: an unrecognised error might be a raw database driver error,
and driver errors sometimes echo the connection string — password included.
Anything crossing into an API response gets sanitised.

As covered in [chapter 4](04-fedctl-cli.md), the plain CLI path deliberately
does **not** go through this, because there the reader is the operator and
the real message is what they need.

### Reading requests strictly

```go
if dec.More() {
    return NewError(CodeInvalidRequest, "trailing data after JSON request", "send exactly one JSON object")
}
```

Exactly one JSON object is accepted. A stream with two objects is an error,
not "read the first and ignore the rest". Strictness here means a malformed
request from the shim fails loudly instead of doing something half-intended.

---

## `internal/validate` — the security layer

[validate.go](../internal/validate/validate.go) is the most security-critical
file in the repository. Every function in it is unit-tested, by requirement.

### Source names

```go
var sourceNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,40}$`)
```

Lowercase letter first, then lowercase letters, digits, and underscores, 3 to
41 characters total. No spaces, no punctuation, no uppercase, no Unicode.

Why so strict? Because a source name becomes a **ClickHouse identifier** — a
database name and a named-collection name. And identifiers are the one thing
in SQL that *cannot* be passed as a bound parameter. Values can:

```sql
CREATE NAMED COLLECTION x AS host = ?, port = ?    -- ? is safe, always
```

Identifiers cannot — there is no `CREATE DATABASE ?`. They must be
interpolated into the SQL string. So the regex is the entire defence against
identifier injection. It deliberately allows far less than ClickHouse itself
would, on the principle that a name you cannot express is better than a name
you cannot trust.

Second layer:

```go
func QuoteIdentifier(name string) string {
    return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
```

Wrap in backticks and double any embedded backtick. The comment is explicit
that this is *defence in depth, not a substitute* for the regex. Both are
applied, always.

This is the check that rejects the spec's example attack:
`foo; DROP DATABASE default`.

### Host validation — the SSRF defence

The heart of the file.

```go
var deniedCIDRs = mustParseCIDRs(
    "127.0.0.0/8",      // loopback — the server itself
    "::1/128",          // IPv6 loopback
    "169.254.0.0/16",   // link-local — includes cloud metadata (169.254.169.254)
    "10.0.0.0/8",       // private
    "172.16.0.0/12",    // private
    "192.168.0.0/16",   // private
    "fd00::/8",         // IPv6 private
)
```

A **CIDR** is a range of IP addresses written as `base/prefix-length`.
`10.0.0.0/8` means "the first 8 bits are fixed" — every address from
`10.0.0.0` to `10.255.255.255`.

`Host` then does this:

```go
addrs, err := resolver.LookupIPAddr(ctx, host)   // 1. resolve the name
if err != nil { return ... "does not resolve" }
if len(addrs) == 0 { return ... "resolved to no addresses" }

hostAllowed := allow.allowsHost(host)
for _, a := range addrs {                        // 2. check EVERY address
    if hostAllowed || allow.allowsIP(a.IP) { continue }
    if denied(a.IP) { return ... error }
}
```

Three properties worth naming:

1. **It resolves the hostname.** Blocking the literal string `127.0.0.1` is
   useless — an attacker registers `evil.com` pointing at `127.0.0.1`. Only
   resolution catches that.
2. **It checks every returned address, not just the first.** A hostname can
   resolve to several IPs. If *any* of them is private, the host is rejected.
   There is a test for exactly this: `TestHost_MultiAddressRejectsIfAnyDenied`.
3. **A name that does not resolve is rejected** rather than allowed through.
   Default-deny.

### The allowlist

Blanket denial of private addresses would make the system unusable in the
common case: your database is *supposed* to be on your internal network. So
there is an explicit escape hatch, configured in `.env`:

```
FEDCTL_ALLOWED_SOURCE_HOSTS=dev-postgres,dev-mysql,10.20.0.0/16
```

`NewAllowlist` accepts three forms per entry and sorts them out itself:

```go
if _, n, err := net.ParseCIDR(e); err == nil { al.nets = append(al.nets, n); continue }   // a CIDR
if ip := net.ParseIP(e); ip != nil { ...single-address net... ; continue }                // an IP
al.hosts[strings.ToLower(e)] = true                                                       // a hostname
```

The important design point: it is *default-deny with explicit exceptions*,
not default-allow. An operator must consciously write down each host they
intend to reach. When you run `fedctl up --dev` and the sample databases fail
to attach, this is why — and `.env.example` pre-fills those two names.

### The TOCTOU warning

```go
// Callers MUST call Host again immediately before opening the connection,
// using a fresh Resolver call (not the addresses returned here) — DNS can
// change between admin-time validation and connection time, and skipping
// the second check turns this from an SSRF guard into a TOCTOU bypass.
```

A comment that tells you a function is not safe on its own. Compare with
[sourceops.go](../cmd/fedctl/sourceops.go), which calls it twice, and the
comment there points back here.

### The testable resolver

```go
type Resolver interface {
    LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}
var DefaultResolver Resolver = net.DefaultResolver
```

Instead of calling Go's DNS resolver directly, `Host` accepts anything with
that one method. In production it gets the real resolver; in tests it gets a
fake one that returns whatever the test wants. That is how
[validate_test.go](../internal/validate/validate_test.go) can test
"hostname resolves to 169.254.169.254" without controlling DNS.

**This is dependency injection**, and it is the standard Go way to make I/O
testable: depend on a small interface, not a concrete implementation.

### SQLite path validation

The other classic vulnerability. A user supplies a filename; the server opens
it. If they supply `../../etc/passwd`, do you read it?

```go
base, err := filepath.Abs(filepath.Clean(userFilesDir))
candidate := filepath.Clean(filepath.Join(base, requested))
if err := withinDir(base, candidate); err != nil { return "", err }   // check 1

resolvedBase, err := filepath.EvalSymlinks(base)
resolved, err := filepath.EvalSymlinks(candidate)                      // follow symlinks
if err := withinDir(resolvedBase, resolved); err != nil { return "", err }  // check 2

info, err := os.Stat(resolved)
if !info.Mode().IsRegular() { return "", ...error }                    // check 3
```

Three distinct attacks, three checks:

1. **Traversal.** `filepath.Clean` collapses `..` segments, then `withinDir`
   verifies the result is still under the base directory. This kills
   `../../etc/passwd`.
2. **Symlink escape.** A file inside `user_files` could be a *symbolic link*
   pointing at `/etc/shadow`. Cleaning the path does not catch that, because
   the path really is inside the directory — it is the target that is not. So
   `EvalSymlinks` resolves the link and the containment check is repeated on
   the real destination.
3. **Not a regular file.** Directories, device nodes, and FIFOs are rejected.
   Opening `/dev/urandom` as a database would be a strange day.

And the containment check itself:

```go
func withinDir(base, target string) error {
    rel, err := filepath.Rel(base, target)
    if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
        return jsonio.NewError(jsonio.CodeInvalidPath, "path escapes the user_files directory", "")
    }
    return nil
}
```

Note it uses `filepath.Separator` rather than a hardcoded `/`, so it is
correct on Windows too. Compare `strings.HasPrefix(target, base)` — the naive
version — which would wrongly accept `/data/user_files_evil` as being inside
`/data/user_files`.

### Credential flag detection

```go
var credentialFlagNames = map[string]bool{
    "password": true, "pass": true, "pwd": true, "passwd": true,
    "secret": true, "token": true, "apikey": true, "credential": true,
    "credentials": true, "p": true,
}
var credentialSubstrings = []string{"password", "passwd", "pass", "pwd", "secret", "token", "apikey", "credential"}
```

Exact matches, plus substring matches after stripping hyphens and
underscores — so `--db-pass`, `--auth_token`, and `--clientSecret` are all
caught. `-p` is in the list because `mysql` and `psql` both use it for
passwords and muscle memory is real.

The comment is careful to describe this correctly:

> This is a defense-in-depth backstop, not the primary guarantee: no fedctl
> command registers a credential-named flag in the first place.

The real guarantee is that the flags do not exist. This catches the day
someone adds one.

### The redactor

```go
func (r *Redactor) Redact(s string) string {
    for _, secret := range r.secrets {
        s = strings.ReplaceAll(s, secret, "***")
    }
    return s
}
```

Given known secret values, scrub them from any text. Used in
[hub.go](../internal/hub/hub.go) before logging a driver error, because a
driver error might quote the connection details it failed with.

Again: this is a *last* line of defence. Passwords are supposed to never
reach a loggable string at all, because they travel as bound parameters. The
redactor exists for the case where that assumption fails.

---

## `internal/ddl` — generating SQL

[ddl.go](../internal/ddl/ddl.go) builds SQL statements. It never executes
one. That separation makes every statement a pure function of its inputs, and
therefore trivially testable — which is exactly what
[ddl_test.go](../internal/ddl/ddl_test.go) does with golden-file tests.

### Statements, args, and secrets

```go
type Arg struct {
    Value  any
    Secret bool
}

type Statement struct {
    Text string `json:"text"`
    Args []Arg  `json:"-"`
}
```

A statement is SQL text plus its bind values. `Text` contains only `?`
placeholders where values belong — which means:

> Text alone is always safe to log or return in `fedctl plan` output.

Marking an argument `Secret` lets `Statement.Secrets()` list exactly the
values needing redaction, so `host` and `user` can still appear in a
diagnostic message while the password cannot. Precision beats blanket
suppression.

Note the `json:"-"` tag on `Args`: it excludes the field from JSON output
entirely. Even if a `Statement` were serialised by accident, the values would
not go with it.

### Plans and rollback

```go
type Step struct {
    Do   Statement
    Undo *Statement    // nil if nothing to undo
}

type Plan struct {
    Steps []Step
}
```

Each step knows how to reverse itself. Attaching a PostgreSQL source is two
steps:

| Step | Do | Undo |
|---|---|---|
| 1 | `CREATE NAMED COLLECTION IF NOT EXISTS fed_x_creds AS host = ?, ...` | `DROP NAMED COLLECTION IF EXISTS fed_x_creds` |
| 2 | `CREATE DATABASE IF NOT EXISTS fed_x ENGINE = PostgreSQL(fed_x_creds) COMMENT ?` | `DROP DATABASE IF EXISTS fed_x` |

If step 2 fails, step 1's `Undo` runs. Without this you would accumulate
orphaned credential collections from every failed attempt — invisible,
because ClickHouse will not show you their contents.

ClickHouse has no transactions for DDL, so the rollback is built by hand.
That is what "atomic" means here: not a database transaction, but a plan that
cleans up after itself.

### Naming

```go
func CollectionName(sourceName string) string { return "fed_" + sourceName + "_creds" }
func DatabaseName(sourceName string) string   { return "fed_" + sourceName }
```

A source named `sales` becomes the ClickHouse database `fed_sales` with
credentials in `fed_sales_creds`. The prefix is what lets `hub.List` find
every federated database with one query (`WHERE name LIKE 'fed_%'`) without a
separate registry table. **ClickHouse's own catalog is the source of truth.**
There is no state file to get out of sync.

> **A wart to be aware of:** the prefix is added unconditionally, so naming a
> source `fed_sales` produces the database `fed_fed_sales`. The example file
> [config/sources.yaml](../config/sources.yaml) and some README examples do
> exactly that. It works and round-trips correctly, it just looks odd. Prefer
> plain names — `sales`, not `fed_sales`.

### The host comment trick

```go
Do: Statement{Text: fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s ENGINE = PostgreSQL(%s) COMMENT ?", db, coll),
              Args: []Arg{plain(hostComment(p["host"]))}},
```

Why store `host=db.example.com` in the database's comment? Because of the
discovery documented at length in both this file and `hub.go`:

> ClickHouse redacts EVERY field in a named collection as `[HIDDEN]` when
> read back via `system.named_collections`, including non-secret ones like
> host.

So there is no way to ask ClickHouse "what host does this source point at?"
once it is stored. The UI needs that for its Host column, and the audit log
needs it on removal. The workaround is to write the host — which is not
secret — into the `COMMENT` field, and parse it back out with a regex in
`hub.databaseHost`.

`hostComment` and the regex in `hub.go` are two halves of one format. The
comment on `hostComment` says so: *"the exact, parseable format
hub.SourceHost expects."*

### The three attach functions

`AttachPostgres` — named collection with six parameters (including `schema`,
defaulting to `public`), then the database.

`AttachMySQL` — identical minus `schema`, which MySQL does not have as a
separate concept.

`AttachSQLite` — **one** step, no named collection:

```go
Do: Statement{Text: fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s ENGINE = SQLite(?)", db),
              Args: []Arg{plain(resolvedPath)}},
```

There are no credentials for a file. The path arrives already validated and
resolved to an absolute path by `validate.SQLitePath` — and the doc comment
states the contract explicitly: *"this function does not touch the
filesystem."* Each layer does its own job and trusts the layer before it,
which is only safe because that contract is written down.

`Detach` builds the reverse: drop the database, then the collection if there
is one. Every statement is `IF EXISTS`, so nothing to roll back — a partial
failure just leaves work for the next run.

---

## `internal/hub` — talking to ClickHouse

[hub.go](../internal/hub/hub.go) is the only file in the project that opens a
ClickHouse connection. Concentrating that in one package means credential
handling, redaction, and error wrapping happen in exactly one place.

### Opening a connection

```go
db := clickhouse.OpenDB(&clickhouse.Options{
    Addr: []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)},
    Auth: clickhouse.Auth{Database: cfg.Database, Username: cfg.User, Password: cfg.Password},
    DialTimeout: 5 * time.Second,
    Settings: clickhouse.Settings{"max_execution_time": 60},
})
db.SetMaxOpenConns(8)
db.SetMaxIdleConns(4)
db.SetConnMaxLifetime(time.Hour)
```

`*sql.DB` in Go is not a single connection — it is a **connection pool**, safe
for concurrent use, which opens and closes real connections as needed. The
three `Set...` calls bound that pool.

Port 9000 is ClickHouse's **native protocol**. Superset uses port 8123, the
HTTP protocol, because its Python driver speaks that. The two are independent
paths to the same server, which is why the comment on `DialConfig` points it
out.

`Open` also pings before returning, so a bad password fails immediately with
a clear `HUB_UNAVAILABLE` rather than at the first query.

### Attach, with rollback

```go
func (c *Conn) Attach(ctx context.Context, plan ddl.Plan) error {
    for i, step := range plan.Steps {
        if _, err := c.db.ExecContext(ctx, step.Do.Text, step.Do.Values()...); err != nil {
            c.rollback(ctx, plan, plan.Steps[:i])
            return jsonio.NewError(jsonio.CodeInternal, "attach failed: "+redact(plan, err.Error()), "")
        }
    }
    return nil
}
```

Read the failure path carefully:

- `plan.Steps[:i]` is a **slice** of the steps that already succeeded — Go
  slice syntax, "everything before index `i`".
- `rollback` walks that backwards, running each `Undo`.
- `redact(plan, err.Error())` scrubs the plan's secrets out of the driver's
  error message before it becomes part of the returned error.

And the rollback's own error handling:

```go
if _, err := c.db.ExecContext(ctx, step.Undo.Text, ...); err != nil {
    c.log("rollback step failed (%s): %s", step.Undo.Text, redact(plan, err.Error()))
}
```

A failed rollback is *logged*, not returned. Otherwise the rollback's error
would replace the original error — and the original is what the user needs to
see. The comment states this: *"Rollback failures are logged but do not
shadow the original error."*

### Listing sources

```go
rows, err := c.db.QueryContext(ctx,
    "SELECT name, engine FROM system.databases WHERE name LIKE ? ORDER BY name", databasePrefix+"%")
```

That is the whole registry. Every federated source is a ClickHouse database
named `fed_*`, so listing them is one query against ClickHouse's own catalog.
For each, the code counts tables and reads the host comment.

```go
// Health and exact table counts are computed live on every call — there is
// no cache to go stale, matching the rest of this system's design.
```

The same philosophy as the data itself: do not store a copy, ask the source
of truth.

### `Probe` — proving a source really works

```go
func (c *Conn) Probe(ctx context.Context, reader *Conn, database string) error {
    // 1. Is the hub itself alive?
    row := c.db.QueryRowContext(ctx, "SELECT 1 FROM system.one")
    // 2. Does the federated database show any tables?
    tables, err := c.Tables(ctx, database)
    if len(tables) == 0 { return ...error }
    // 3. Actually read from one, as bi_ro.
    q := fmt.Sprintf("SELECT count() FROM %s.%s", validate.QuoteIdentifier(database), validate.QuoteIdentifier(tables[0].Name))
    if _, err := reader.db.ExecContext(ctx, q); err != nil { return ...error }
    return nil
}
```

Step 3 is the whole point. Creating a federated database succeeds *even if
the remote server is unreachable* — ClickHouse connects lazily, so the DDL
only records the intent. Listing tables might come from catalog information.
Only actually selecting rows forces ClickHouse to open the upstream
connection.

And it must run as `reader` (`bi_ro`), not `c` (`fed_admin`), because
`fed_admin` has no `SELECT` privilege on federated data by design. The
comment records that this was verified live: a `fed_admin` probe fails with
"Not enough privileges". Since `bi_ro` is the role Superset itself uses, a
passing probe proves what Superset would actually experience.

Note `QuoteIdentifier` applied to the table name, which came from ClickHouse
rather than from a user. Defence in depth again: the value is trusted, but it
is quoted anyway.

### [databases.go](../internal/hub/databases.go)

One method, but it demonstrates an important Go pattern:

```go
err := c.db.QueryRowContext(...).Scan(&engine)
if err == sql.ErrNoRows {
    return "", jsonio.NewError(jsonio.CodeSourceNotFound, "database "+name+" does not exist", "")
}
if err != nil {
    return "", jsonio.NewError(jsonio.CodeInternal, "looking up database engine: "+err.Error(), "")
}
```

`sql.ErrNoRows` is not really an error — it is "no result". Distinguishing it
from a genuine failure is what turns a generic 500 into an accurate 404.

---

## `internal/sources` — pluggable database types

### The interface

[registry.go](../internal/sources/registry.go) defines what a source type
must be able to do:

```go
type Type interface {
    Name() string               // "postgres"
    Engine() string             // "PostgreSQL" — the ClickHouse ENGINE name
    HasCredentials() bool       // false only for sqlite
    Fields() []Field            // what the UI should render
    Validate(userFilesDir string, params Params) (Params, error)
    TestConnection(ctx context.Context, userFilesDir string, params Params) (int, error)
}
```

### Self-describing forms

`Fields()` is the interesting one:

```go
func (postgresType) Fields() []Field {
    return []Field{
        {Name: "host",     Label: "Host",     Type: "text",     Required: true},
        {Name: "port",     Label: "Port",     Type: "number",   Required: true, Default: "5432"},
        {Name: "user",     Label: "User",     Type: "text",     Required: true},
        {Name: "password", Label: "Password", Type: "password", Required: true},
        {Name: "database", Label: "Database", Type: "text",     Required: true},
        {Name: "schema",   Label: "Schema",   Type: "text",     Required: false, Default: "public"},
    }
}
```

That list is served over HTTP by `GET /source-types`, and the React form
**renders itself from it**. There is no PostgreSQL-specific code in the
frontend at all. Add a new source type in Go with its own `Fields()`, and the
Add Source dialog grows the right inputs with no TypeScript changes.

This is the concrete payoff of the interface design, and the README states it
as a promise: *"Supporting a new database engine is one Go file and no
frontend changes."*

### The registry

```go
var registry = map[string]Type{}
func Register(t Type) { registry[t.Name()] = t }
```

and in each type's file:

```go
func init() { Register(postgresType{}) }
```

`init()` runs at package load, so simply *existing* in the package is enough
to be registered. `All()` returns them sorted by name, so output is stable
run to run.

### [postgres.go](../internal/sources/postgres.go) and a real SSRF near-miss

Most of this file is unremarkable — validate required fields, default the
schema to `public`, check the port. Then there is `postgresConnConfig`, which
carries the longest comment in the package, and it is worth understanding
fully.

The natural way to build a pgx connection config is:

```go
cfg, _ := pgx.ParseConfig("")   // empty DSN, then fill in the fields
cfg.Host = p["host"]
cfg.Port = port
```

That is a security hole. Parsing an empty DSN makes pgx populate
**fallbacks** — libpq-compatible defaults like `127.0.0.1` and `::1`. Those
fallbacks survive overwriting `Host` and `Port`, and pgx will try them if the
primary connection fails. Those addresses never went through `validate.Host`.
An SSRF guard that can be bypassed by supplying a host that fails to connect
is not a guard.

The fix is to build an explicit connection URL, so no fallbacks are ever
populated:

```go
u := &url.URL{
    Scheme: "postgres",
    User:   url.UserPassword(p["user"], p["password"]),
    Host:   fmt.Sprintf("%s:%d", p["host"], atoiOrZero(p["port"])),
    Path:   "/" + p["database"],
}
cfg, err := pgx.ParseConfig(u.String())
```

Then verify the parser did what was asked:

```go
if cfg.Host != p["host"] || int(cfg.Port) != atoiOrZero(p["port"]) {
    return nil, jsonio.NewError(..., "postgres connection config did not match the requested host/port", "")
}
```

And then the subtlest part — the fallback filter:

```go
kept := cfg.Fallbacks[:0]
for _, fb := range cfg.Fallbacks {
    if fb.Host == cfg.Host && fb.Port == cfg.Port {
        kept = append(kept, fb)
    }
}
cfg.Fallbacks = kept
```

Fallbacks pointing at the *same* host and port are kept; any pointing
elsewhere are dropped. Why keep any at all? Because pgx implements
`sslmode=prefer` — try TLS, fall back to plaintext on the same connection —
*using* the fallback mechanism. An earlier version of this code dropped all
fallbacks and broke against any PostgreSQL without TLS configured, which is
the normal case for a local or development server. The comment records both
the bug and the reasoning:

> Live-verified dropping every fallback unconditionally (the previous
> behavior here) broke that retry entirely... Any fallback pointing at a
> DIFFERENT host/port (the actual SSRF-relevant case) is still stripped.

That is what a good security fix looks like: precisely scoped, not a blunt
instrument, and explained where the next person will read it.

(`cfg.Fallbacks[:0]` reusing the same backing array to filter in place is a
common Go idiom for filtering a slice without allocating.)

### [mysql.go](../internal/sources/mysql.go)

The same shape, simpler. It uses the driver's own config builder
(`mysql.NewConfig()`), which does not have the fallback problem, then opens
via `database/sql`, pings, and counts tables in `information_schema`.

### [sqlite.go](../internal/sources/sqlite.go)

Different from the other two, in the ways a file is different from a server.

```go
func (sqliteType) Validate(userFilesDir string, params Params) (Params, error) {
    resolved, err := validate.SQLitePath(userFilesDir, params["path"])
    if err != nil { return nil, err }
    return Params{"path": resolved}, nil
}
```

It returns a *new* map containing only `path` — the resolved absolute path.
Anything else the caller sent is discarded.

And a nice defensive detail in `TestConnection`:

```go
// It checks the file exists first: SQLite drivers (this one included)
// silently create an empty database on open if the path doesn't exist,
// which would otherwise make "test connection" against a mistyped path
// report a false success with zero tables instead of a clear error.
if _, err := os.Stat(p["path"]); err != nil { return 0, ...error }
```

Without that `Stat`, typing a filename wrong would *create* an empty database
and report success. Knowing that SQLite creates files on open is the kind of
detail that turns a confusing bug into a clear error message.

### [util.go](../internal/sources/util.go)

Five lines:

```go
func atoiOrZero(s string) int {
    n, err := strconv.Atoi(s)
    if err != nil { return 0 }
    return n
}
```

Non-numeric input becomes `0`, which `validate.Port` then rejects — so
"not a number" and "out of range" produce one consistent `INVALID_PORT`
error instead of two different ones. A tiny function with a documented
rationale.

---

## `internal/config` — configuration and the sources file

[config.go](../internal/config/config.go) does two unrelated jobs.

### Environment configuration

```go
func LoadApp() (App, error) {
    a := App{
        ClickHouseHost:   getenvDefault("CLICKHOUSE_HOST", "clickhouse"),
        FedAdminUser:     getenvDefault("FEDCTL_ADMIN_USER", "fed_admin"),
        FedAdminPassword: os.Getenv("FEDCTL_ADMIN_PASSWORD"),
        ...
    }
}
```

Note what it does *not* do: it does not require anything. The comment
explains why:

> It is deliberately permissive — it does not require FedAdminPassword, the
> metadata URI, or any other single field, because different fedctl commands
> need different subsets.

`fedctl check clickhouse` runs inside the ClickHouse container and has no
business needing Superset's metadata URI. So validation is pushed to whoever
actually needs a value: `openHub` checks `FedAdminPassword`, `up` and
`preflight` call `ValidateMetadataURI`. **Validate at the point of use, not
in the loader.**

The comment on `SupersetBaseURL` documents a real trap:

> SupersetBaseURL is used only by fedctl processes that run on the HOST — NOT
> by the in-container healthcheck, which always dials the fixed internal
> localhost:8088.

Inside the container Superset always listens on 8088. On your host it appears
on whatever `SUPERSET_PORT` says. Same service, two different addresses,
depending on which side of the container boundary you are standing.

### Metadata URI validation

```go
func ValidateMetadataURI(uri string) error {
    if strings.TrimSpace(uri) == "" { return fmt.Errorf("SQLALCHEMY_DATABASE_URI is unset: ...") }
    if strings.HasPrefix(lower, "sqlite") { return fmt.Errorf("... this is a reliability problem, not a style preference ...") }
    if !strings.HasPrefix(lower, "postgresql") { return fmt.Errorf("... must be a postgresql:// URI ...") }
    return nil
}
```

Three rules, three specific error messages. Notice the messages explain *why*
rather than just saying "invalid". An error message is documentation that
appears exactly when it is needed.

### The sources file

```go
type SourceSpec struct {
    Name        string `yaml:"name"`
    Host        string `yaml:"host,omitempty"`
    PasswordEnv string `yaml:"password_env,omitempty"`
    ...
}
```

The critical field is `PasswordEnv`. The YAML file never contains a password
— it contains the *name of an environment variable* that holds one:

```yaml
- name: fed_sales
  type: postgres
  host: pg.example.internal
  user: svc_readonly
  password_env: FED_SALES_PASSWORD
```

`ResolveParams` reads it at apply time, and errors clearly if it is unset.
That is what makes [config/sources.yaml](../config/sources.yaml) safe to
commit to git — a pattern worth reusing anywhere you have configuration with
secrets in it.

`LoadSourcesFile` also rejects duplicate names, catching a copy-paste mistake
before it half-applies.

---

## `internal/health` — the health checks

[health.go](../internal/health/health.go) implements the two targets Docker
probes.

`checkClickHouse` opens a hub connection as `fed_admin` and pings it. A
successful ping means the server is up, the network works, the credentials
are right, and `roles.xml` was loaded — one probe covering four failure
modes.

`checkSuperset` requests `http://localhost:8088/health`. The comment answers
the obvious question:

> checkSuperset always dials localhost:8088, never cfg.SupersetBaseURL: this
> only ever runs from inside the superset container itself, where gunicorn
> always listens on its fixed internal port 8088 regardless of what host port
> compose.yaml publishes.

Both use `context.WithTimeout(ctx, 5*time.Second)`, matching the `timeout:
5s` in the Compose health check. A probe that hangs is a probe that never
reports.

Also note `io.LimitReader(resp.Body, 4096)` when reading the response body
for an error message — bounding how much of a remote response you will read
into memory is a habit worth acquiring.

---

## `internal/preflight` — checking the host

[preflight.go](../internal/preflight/preflight.go) runs the checks that must
pass before starting the stack. Its structure is a small pattern worth
copying:

```go
type Result struct {
    Name    string
    OK      bool
    Message string
    Hint    string
}

func Run(ctx context.Context, opts Options) ([]Result, bool) {
    var results []Result
    results = append(results, checkComposeV2(ctx))
    results = append(results, checkBuildx(ctx))
    ...
    ok := true
    for _, r := range results { if !r.OK { ok = false } }
    return results, ok
}
```

**Every check runs, always.** Nothing stops at the first failure. If you are
missing both Compose v2 and buildx, you learn both now instead of
discovering the second after fixing the first. The comment states the
intent: *"Every check is independent and all are run, so one failure doesn't
hide the next."*

`checkComposeV2` is a nice piece of error-message design:

```go
out, err := runCmd(ctx, "docker", "compose", "version")
if err == nil { return Result{OK: true, ...} }
// v2 failed — is there a v1 binary? Then say so specifically.
if legacyOut, legacyErr := runCmd(ctx, "docker-compose", "--version"); legacyErr == nil {
    return Result{OK: false,
        Message: "found Compose v1 (`docker-compose`): " + ...,
        Hint:    "this stack requires Compose v2 (`docker compose`, no hyphen) — upgrade Docker Desktop or install the compose-plugin package"}
}
```

Rather than "docker compose not found", it diagnoses *why* and tells you what
to do. Writing the second lookup costs five lines and saves a support ticket.

`checkImageManifests` shells out to `docker buildx imagetools inspect` and
looks for both `linux/amd64` and `linux/arm64` in the output. The comment
pre-empts the obvious objection:

> reimplementing OCI registry auth in Go would be a lot of surface for no
> benefit here. This is not a shell script: argv is passed as a slice, never
> through a shell interpreter.

`checkPostgresReachable` actually connects to Superset's metadata database
using `pgx`, so a typo in the URI is caught here rather than by a container
crash-looping later.

### The three `resources_*.go` files

The same two checks — free disk space and total RAM — implemented three
times, once per operating system, selected by build tag.

- [resources_linux.go](../internal/preflight/resources_linux.go) uses
  `unix.Statfs` and `unix.Sysinfo`. It can also check the real things: cgroup
  v2 (needed for accurate container memory limits) and whether SELinux is
  present.
- [resources_darwin.go](../internal/preflight/resources_darwin.go) uses
  `unix.Statfs` and `unix.SysctlUint64("hw.memsize")`. cgroups and SELinux
  return "not applicable" — on macOS, Docker runs in a Linux VM this process
  cannot see into.
- [resources_windows.go](../internal/preflight/resources_windows.go) uses
  `windows.GetDiskFreeSpaceEx`, and for memory calls into `kernel32.dll`
  directly because Go's Windows library does not wrap
  `GlobalMemoryStatusEx`:

  ```go
  var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
  var procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
  ```

  The `memoryStatusEx` struct mirrors the Win32 `MEMORYSTATUSEX` layout field
  for field, and `m.Length = uint32(unsafe.Sizeof(m))` is the Win32
  convention of telling the API which version of the struct you are passing.
  This is as low-level as this codebase gets.

Each file's "not applicable" result is still `OK: true` with an explanatory
message — a check that cannot run is not a failure, but it should say so
rather than silently vanish.

---

Next: [Containers and configuration](06-containers-and-configuration.md).
