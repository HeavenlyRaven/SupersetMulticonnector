# 4. `fedctl`, the Go CLI

Everything in [cmd/fedctl/](../cmd/fedctl/) is *plumbing*: parsing flags,
deciding whether output should be human-readable or JSON, and calling into
[internal/](../internal/) where the real logic lives. Keep that split in mind
— it is the organising principle of the whole codebase.

The commands, at a glance:

```
fedctl
├── install                     first-time setup: everything below, in order
├── preflight                   check the host before starting anything
├── up      [--dev]             start the stack, wait for health, configure Superset
├── down    [--volumes]         stop the stack
├── check   <clickhouse|superset>   health probe (run by Docker, not usually by you)
├── doctor                      probe every source, print a status table
├── version                     version, commit, build date, platform
├── seed sqlite --from <path>   copy a host file into the shared volume
├── apply  [-f sources.yaml]    reconcile sources from a config file
├── plan   [-f sources.yaml]    print the SQL that apply would run
└── source
    ├── types                   what source types exist, and their form fields
    ├── list                    what sources are attached
    ├── add                     attach a new source
    ├── remove <name>           detach a source
    ├── test                    try a connection without saving anything
    ├── tables <name>           what tables ClickHouse sees in a source
    ├── probe  <name>           is this source actually reachable right now?
    └── sqlite-files
        ├── list                what SQLite files are uploaded
        └── put --name <n>      write stdin's bytes to a file in the volume
```

---

## [main.go](../cmd/fedctl/main.go) — the entry point

Only about 120 lines, but four distinct jobs.

### 1. The panic guard

```go
defer func() {
    if r := recover(); r != nil {
        fmt.Fprintln(os.Stderr, "fedctl: internal error (recovered from a panic) — this is a bug, please report it")
        os.Exit(2)
    }
}()
```

A **panic** in Go is an unrecoverable-by-default crash. Normally Go prints the
panic message plus a full stack trace, and a stack trace can include the
*values of function arguments*. Several functions in this program hold a
database password in a local variable. So the panic is caught and
deliberately **nothing about it is printed** — better to lose debugging
information than to print a password to a log file.

That is a real trade-off, made consciously, and the comment says so.

### 2. Rejecting credential-shaped flags

```go
if err := guardCredentialFlags(os.Args[1:]); err != nil { ... }
```

This runs *before Cobra even parses the arguments*. It walks the raw argument
list and rejects anything that looks like `--password`, `--token`,
`--db-pass`, `-p`, and so on.

Why so aggressive? On Linux, `/proc/<pid>/cmdline` is world-readable. If you
run `fedctl source add --password hunter2`, every other user on that machine
can read the password while the command runs. So this program refuses the
flag outright rather than accepting it and hoping for the best.

Note what this means in practice: **no `fedctl` command has a password flag**.
Passwords arrive either by being prompted for on stdin, or inside the JSON
request on stdin. The check is a backstop against a future developer adding
one by accident. The rule-matching itself lives in
`validate.CredentialFlagName` — see [chapter 5](05-internal-packages.md).

### 3. Loading `.env`

```go
loadDotEnv(".env")
```

Explained under [dotenv.go](#dotenvgo--reading-env-yourself) below.

### 4. Building the command tree

```go
func newRootCmd() *cobra.Command {
    root := &cobra.Command{
        Use:           "fedctl",
        SilenceUsage:  true,
        SilenceErrors: true,
    }
    root.AddCommand(newInstallCmd())
    root.AddCommand(newPreflightCmd())
    ...
}
```

`SilenceErrors: true` tells Cobra not to print errors itself, because this
program prints them its own way — via `printCLIError`, the last function in
the file.

`printCLIError` has an interesting comment worth reading in full. There are
two error-printing paths in this codebase with **different trust
boundaries**:

- In `--json` mode, an unrecognised error is replaced with a generic
  `"internal error"` string, because the caller might be a web request and a
  raw driver error could leak internals.
- In normal CLI mode, the person reading the message *is* the operator
  looking at their own terminal, so they get the real message. Replacing
  "docker compose up failed: exit status 1" with "internal error" would be
  useless.

There is a regression test for this in
[main_test.go](../cmd/fedctl/main_test.go) — an earlier version got it wrong.

---

## [dotenv.go](../cmd/fedctl/dotenv.go) — reading `.env` yourself

Docker Compose reads `.env` and passes the values into containers. But
`fedctl` also runs as a plain program on your laptop (`fedctl up`,
`fedctl preflight`), and in that situation nothing has put `.env` into its
environment. Without this file, `.env` would be "effectively decorative for
half of what this tool does" — the comment's phrasing.

The parser is deliberately minimal: skip blanks and `#` comments, split on
the first `=`, strip surrounding quotes, and then:

```go
if _, alreadySet := os.LookupEnv(key); alreadySet {
    continue
}
```

**A variable already exported in your shell wins over `.env`.** That is the
same precedence Compose uses, and it is what lets you do a one-off override,
or let CI inject secrets directly, without editing the file.

---

## [common.go](../cmd/fedctl/common.go) — the shared helpers

Small file, high traffic. Six helpers that most commands use.

`loadApp(jsonMode bool)` loads configuration and, on failure, reports it in
whichever format the caller is using — a JSON envelope on stdout, or a plain
message on stderr.

`openHub` and `openBIReadOnly` both open a ClickHouse connection, but as
**different users**, and the difference is the interesting part:

```go
// openHub  → connects as fed_admin  (can CREATE/DROP databases)
// openBIReadOnly → connects as bi_ro (can SELECT actual data)
```

`fed_admin` deliberately has **no permission to read federated data**. So
when `fedctl probe` wants to prove a source really works by running a
`SELECT`, it cannot use `fed_admin` — that query would fail with "Not enough
privileges". It uses `bi_ro`, which is the same role Superset itself uses, so
the probe proves exactly what Superset would experience. That is why
`fedctl probe` and `fedctl doctor` open *two* connections.

`stderrLogger` writes diagnostics to stderr — never stdout. In `--json` mode,
stdout carries exactly one JSON envelope and nothing else, so a stray log line
on stdout would corrupt the response the Python shim is trying to parse. This
rule holds throughout the codebase.

`writeJSONResult` and `printHuman` are the two output paths. Notice that both
render the *same* data structure; JSON mode just wraps it in the envelope.

---

## [images.go](../cmd/fedctl/images.go) — two constants

```go
const (
    SupersetBaseImage   = "apache/superset:6.1.0@sha256:59cd4af6..."
    ClickHouseBaseImage = "clickhouse/clickhouse-server:26.8.1.2041@sha256:9f8fa0d5..."
)
```

The same pinned references that appear in the Dockerfiles, duplicated here so
`fedctl preflight` can verify they still exist and still publish both CPU
architectures — without parsing YAML or Dockerfiles. The comment reminds you
to update both places, and to re-verify old pins because tags can be
republished.

`seed.go` also uses `ClickHouseBaseImage` as the throwaway container for
copying files.

---

## [version.go](../cmd/fedctl/version.go) — build metadata

```go
fmt.Printf("fedctl %s (commit %s, built %s) %s/%s\n",
    version, commit, date, runtime.GOOS, runtime.GOARCH)
```

Those three variables are declared in `main.go` with placeholder values and
overwritten **at build time** by the linker. Look at the
[Makefile](../Makefile):

```make
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
```

`-X main.version=1.2.3` sets the value of the variable `version` in package
`main`. `-s -w` strip debug information to shrink the binary. This is the
standard Go way to stamp a build without generating source files.

---

## [check.go](../cmd/fedctl/check.go) — the health check

Fifteen lines of Cobra wrapper around `health.Check`. What matters is who
calls it: Docker does, every fifteen seconds, inside each container, as
declared in [compose.yaml](../compose.yaml) and both Dockerfiles.

`cobra.ExactArgs(1)` enforces exactly one argument; `cmd.ValidArgs` powers
shell tab-completion. The logic is in
[internal/health/](../internal/health/health.go) — see chapter 5.

---

## [preflight.go](../cmd/fedctl/preflight.go) — checking the host

Two things: it defines which images to check (`pinnedImages()`), and it
renders the results.

```go
w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
fmt.Fprintln(w, "CHECK\tSTATUS\tMESSAGE")
```

`text/tabwriter` is a standard-library package that aligns tab-separated
columns — you write `\t` between fields and it computes the widths. That is
how the output table lines up.

`printPreflightResults` is exported to the package, not just this file,
because `install` reuses it for its own preflight step.

---

## [compose.go](../cmd/fedctl/compose.go) — talking to Docker safely

Four small functions and one important one.

`runComposeArgs` builds the argument list:

```go
args := []string{"compose", "-f", "compose.yaml"}
if dev {
    args = append(args, "-f", "compose.dev.yaml", "--profile", "dev")
}
```

`runDockerCompose` executes it:

```go
cmd := exec.CommandContext(ctx, "docker", args...)
```

Notice what is *absent*: there is no shell. `exec.Command` takes the program
name and an array of arguments, and passes them to the operating system
directly. Nothing is ever parsed by `sh`, so quoting bugs and shell injection
are structurally impossible. This is a project-wide rule ("no shell scripts
anywhere"), and this is the mechanism that upholds it.

### `resolveComposeVolume` — a real bug, preserved in a comment

This function exists because of a failure that is worth studying:

Compose prefixes volume names with the project name. A volume declared as
`ch-user-files` actually exists in Docker as
`superset-federation_ch-user-files`. An earlier version of `fedctl seed
sqlite` ran:

```
docker run -v ch-user-files:/dest ...
```

Docker happily **created a brand-new, empty volume** literally called
`ch-user-files` and copied the file into it. The command printed success. The
file was nowhere the running stack could see it.

The fix is to stop guessing and ask Docker, using the labels Compose itself
attaches to every volume it creates:

```go
cmd := exec.CommandContext(ctx, "docker", "volume", "ls",
    "--filter", "label=com.docker.compose.project="+project,
    "--filter", "label=com.docker.compose.volume="+logicalName,
    "--format", "{{.Name}}")
```

And the project name itself is obtained by asking Compose (`docker compose
config --format json`) rather than assuming, because `COMPOSE_PROJECT_NAME`
can override what is written in the YAML.

**The lesson:** when another tool owns a naming convention, ask that tool for
the answer instead of reimplementing its rules.

---

## [up.go](../cmd/fedctl/up.go) — starting everything

`runUp` is the interesting function, and it runs five steps in order:

**1. Fail fast on bad configuration.**

```go
if err := config.ValidateMetadataURI(preApp.SupersetMetadataURI); err != nil {
    return err
}
```

Before starting a single container, verify that Superset's metadata database
is configured and is PostgreSQL. Catching this now saves you from a stack
that starts, appears fine, and then crash-loops.

**2. `docker compose up -d --build`.** `-d` detaches, `--build` rebuilds
images if their inputs changed.

**3. Wait for health.** `waitHealthy` polls `docker compose ps --format json`
every two seconds until every service reports `healthy` (or `running` for
services with no health check), or the timeout expires.

Why poll Compose rather than checking directly? Because Compose reports what
the *container's own health check* found — a check that runs **inside** the
container, where the service hostnames actually resolve. `fedctl` on your
laptop cannot reach `clickhouse:9000` at all; the Docker network is not
visible from the host.

The JSON parsing here is defensive, and the comment tells you why: some
Compose versions print one JSON object per line, others print a single JSON
array. The code tries both.

**4. Bootstrap Superset.**

```go
runComposeArgs(dev, "exec", "-T", "superset", "superset", "db", "upgrade")
runComposeArgs(dev, "exec", "-T", "superset", "superset", "fab", "create-admin", ...)
runComposeArgs(dev, "exec", "-T", "superset", "superset", "init")
```

Three standard Superset commands: create/migrate the metadata schema, create
the admin user, install default roles and permissions. In a bigger deployment
these would run in a dedicated init container — but the spec says exactly two
containers, so they run here via `docker compose exec`.

Creating the admin is deliberately non-fatal:

```go
_ = runDockerCompose(ctx, createArgs)
```

The `_ =` explicitly discards the error, because on any run after the first,
the user already exists and the command fails. That is expected, not a
problem.

Note the honest comment about a rule being broken here: `superset fab
create-admin` has no way to accept a password on stdin, so the password
*does* travel as a command-line argument in this one place. The comment
explains the scope (visible only inside the Superset container, for the
duration of one command) and why it is different from `fedctl`'s own rule.
That is the right way to handle a compromise — document it where it happens,
do not pretend it is not there.

**5. Register the ClickHouse connection in Superset** — see
[superset.go](#supersetgo--talking-to-supersets-rest-api).

---

## [superset.go](../cmd/fedctl/superset.go) — talking to Superset's REST API

A minimal HTTP client with exactly three operations.

**Log in** — POST to `/api/v1/security/login` with the admin credentials,
receive a JWT access token.

**Fetch a CSRF token** — GET `/api/v1/security/csrf_token/`, keeping both the
token and the session cookie. Superset requires both on write requests.

**Create the database connection** — POST to `/api/v1/database/`:

```go
uri := fmt.Sprintf("clickhousedb://%s:%s@%s:8123/default", ...)
payload := createDatabasePayload{
    DatabaseName:  "ClickHouse Federation Hub",
    SQLAlchemyURI: uri,
    Expose:        true,   // show it in SQL Lab
    AllowDML:      false,  // no INSERT/UPDATE/DELETE
    AllowCTAS:     false,  // no CREATE TABLE AS SELECT
    AllowCVAS:     false,  // no CREATE VIEW AS SELECT
}
```

Two details to notice.

`url.QueryEscape` is applied to the username and password before they are
embedded in the URI — a password containing `@` or `/` would otherwise
corrupt the URL's structure.

The write-blocking flags are **belt and braces**: `bi_ro` is already
`readonly=2` on the ClickHouse side, so writes are impossible regardless.
Setting them here too means the UI does not even offer the option. Two
independent layers enforcing the same rule is a deliberate pattern in this
codebase.

Finally, idempotence, since Superset has no "create or update" endpoint:

```go
if resp.StatusCode == http.StatusUnprocessableEntity &&
   strings.Contains(strings.ToLower(string(respBody)), "already exists") {
    return nil // idempotent: already registered by a prior `fedctl up`
}
```

A duplicate-name error is treated as success. This is what makes `fedctl up`
safe to run repeatedly.

---

## [down.go](../cmd/fedctl/down.go) — stopping

Twenty-five lines. `docker compose down`, plus `--volumes` if you pass
`--volumes`.

Be careful with that flag: it deletes `ch-data`, `ch-user-files`, and
`superset-home`. Every uploaded SQLite file and every attached source
definition goes with it. Your *source* databases are untouched — they are
external — but everything the hub knew about them is gone.

---

## [install.go](../cmd/fedctl/install.go) — the friendly front door

The largest file in `cmd/`, and the one a new user meets first. It exists so
that setup is one command instead of a page of instructions.

The flow:

1. Confirm you are in a checkout of this repo (`compose.yaml` and
   `.env.example` are present).
2. Confirm `docker` is on the PATH; if not, print an OS-specific install link.
3. Write `.env` — unless one exists and you did not pass `--force`.
4. Re-load `.env` into this process (the file may have just been created).
5. Run the preflight checks.
6. Run the same code `fedctl up` runs.

### How `.env` gets written

This is the clever part, and worth copying in your own projects. The
generation is driven by a table:

```go
var envFields = []envField{
    {Key: "SQLALCHEMY_DATABASE_URI", Prompt: "External PostgreSQL URI ...", Validate: config.ValidateMetadataURI},
    {Key: "SUPERSET_SECRET_KEY", Generate: true},
    {Key: "SUPERSET_ADMIN_PASSWORD", Prompt: "...(leave blank to auto-generate)", Secret: true},
    {Key: "FEDCTL_ADMIN_PASSWORD", Generate: true},
    {Key: "SUPERSET_PORT", Prompt: "Port to publish Superset on", Default: "8088"},
    ...
}
```

Each entry says how that key is resolved:

- `Generate: true` — a random secret, never shown, never asked about. All the
  internal passwords (`FEDCTL_ADMIN_PASSWORD`, `SUPERSET_CH_PASSWORD`,
  `SUPERSET_SECRET_KEY`) are like this. **You are never asked to invent a
  password that only two containers will ever use.**
- `Secret: true` — prompt with hidden input; if you leave it blank, generate
  one and print it exactly once.
- `Validate` — prompt and re-prompt until the answer passes the check.
- `Default` — use it silently.

Then the file is written by **rewriting `.env.example` line by line**:

```go
lines := strings.Split(string(exampleBytes), "\n")
for i, line := range lines {
    m := envKeyLineRe.FindStringSubmatch(line)
    if m == nil { continue }              // comment or blank: keep as-is
    if v, ok := values[m[1]]; ok {
        lines[i] = m[1] + "=" + v         // replace the value only
    }
}
```

Every comment in `.env.example` survives into your `.env`, so your real
config file is self-documenting, and `.env.example` stays the single source
of truth for documentation. Any key that gets added there later passes
through untouched.

And the file mode:

```go
os.WriteFile(".env", ..., 0o600)   // owner read/write only
```

`0o600` is octal Unix permissions: this file holds every credential in the
stack, so nobody but its owner may read it.

### Secret generation

```go
func generateSecret(nBytes int) (string, error) {
    b := make([]byte, nBytes)
    if _, err := rand.Read(b); err != nil { ... }
    return hex.EncodeToString(b), nil
}
```

The import is `crypto/rand`, **not** `math/rand`. That distinction matters
enormously: `math/rand` is a predictable pseudo-random generator, fine for
shuffling a list and catastrophic for generating a password. If you take one
security habit from this file, take this one.

### The hand-written `readLine`

```go
// readLine reads one line directly from os.Stdin, one byte at a time,
// deliberately without a buffered reader...
```

Reading input byte by byte looks like a beginner mistake. It is not. The
password prompt uses `term.ReadPassword`, which reads from the same file
descriptor in raw mode. A `bufio.Reader` reads *ahead* — grabbing bytes into
a buffer that `term.ReadPassword` then never sees, silently eating part of
the password. Reading exactly one byte at a time keeps the two compatible.

This is the kind of thing you only learn by hitting it.

---

## [seed.go](../cmd/fedctl/seed.go) — getting a SQLite file into the volume

The problem: SQLite is a file, and it must end up in the `ch-user-files`
volume where ClickHouse can read it. But the project forbids bind mounts for
data. So how does a file on your laptop get into a Docker-managed volume?

The answer:

```go
dockerArgs := []string{
    "run", "--rm",
    "-v", hostDir + ":/src:ro,z",     // your file's directory, read-only
    "-v", volume + ":/dest",          // the real Compose volume
    ClickHouseBaseImage,
    "cp", "/src/" + base, "/dest/" + dest,
}
```

Start a throwaway container that mounts both, run `cp`, and let the container
delete itself (`--rm`). The running stack still has no bind mount; the
temporary container did the crossing.

Validation before that:

```go
var seedNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
```

The destination name may contain no path separators and no `..`, so it cannot
escape the volume. The source must be an existing *regular* file — not a
directory, not a device node.

Note `filepath.ToSlash` and `filepath.Abs`. Windows paths use backslashes;
Docker wants forward slashes. Using the `path/filepath` package rather than
string concatenation is a project rule, and it is why this works identically
on Windows, macOS, and Linux.

---

## [source.go](../cmd/fedctl/source.go) — the source command tree

Eight subcommands, all following the same shape. Take `add`:

```go
if jsonMode {
    if err := readJSONRequest(&req); err != nil { ... }   // whole request from stdin
} else {
    // build the request from flags...
    if ty, ok := sources.Get(srcType); ok && ty.HasCredentials() {
        pw, err := readSecret(fmt.Sprintf("password for %s %q: ", srcType, name))
        params["password"] = pw
    }
}
```

**Two input modes, one implementation.** In `--json` mode the entire request
including the password arrives as one JSON object on stdin; that is the
Python shim's path. In normal CLI mode you supply flags, and if the source
type needs credentials, the password is *prompted for* on stdin. Either way,
the password never touches a command-line argument.

`ty.HasCredentials()` asks the source type itself, so SQLite (which has no
credentials) is never prompted. Nothing here hardcodes a list of types.

At the bottom, `finish` is the shared tail of every subcommand:

```go
func finish(jsonMode bool, result any, err error) error {
    if jsonMode { return writeJSONResult(result, err) }
    if err != nil { printCLIError(err); return err }
    return printHuman(result)
}
```

One envelope on stdout in JSON mode; a readable message or indented JSON
otherwise. Every subcommand ends by calling it, so the output contract cannot
drift between commands.

---

## [sourceops.go](../cmd/fedctl/sourceops.go) — where it all comes together

Small file, but it is the heart of the CLI. Read `attachSource` line by line:

```go
func attachSource(ctx context.Context, conn *hub.Conn, app config.App, name, typeName string, rawParams map[string]string) error {
    // 1. Validate the parameters for this source type.
    ty, params, err := sources.Validate(ctx, typeName, app.UserFilesDir, rawParams)
    if err != nil { return err }

    // 2. SSRF check.
    if host, ok := params["host"]; ok {
        if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil { return err }
    }

    // 3. Build the SQL (nothing has been executed yet).
    plan, err := buildAttachPlan(ty, name, params)
    if err != nil { return err }

    // 4. SSRF check AGAIN, immediately before ClickHouse dials the host.
    if host, ok := params["host"]; ok {
        if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil { return err }
    }

    // 5. Execute, with rollback on failure.
    return conn.Attach(ctx, plan)
}
```

Steps 2 and 4 look like a copy-paste mistake. They are not — that is the
TOCTOU defence. Between validating the host and actually connecting to it,
DNS could change. A hostname that resolved to a public IP during validation
could resolve to `127.0.0.1` by the time ClickHouse dials it. Checking again
right before the connection closes that window. The comment in
[validate.go](../internal/validate/validate.go) states this as a requirement
on callers: *"Callers MUST call Host again immediately before opening the
connection, using a fresh Resolver call (not the addresses returned here)."*

The doc comment above the function also flags something structural:

> This is the single reconcile implementation spec section 9 requires:
> `source add` and `apply` both call this.

There is exactly one function that attaches a source. The interactive path
and the declarative path share it. If they had separate implementations, they
would eventually disagree — usually about a security check.

### The other operations

`doAddSource` wraps `attachSource` with an existence check, so adding a
duplicate gives a clean `SOURCE_EXISTS` error instead of a confusing
`IF NOT EXISTS` no-op. Note that `apply` deliberately *skips* this check —
re-running it must be a silent no-op, not an error.

`doRemoveSource` has one subtlety:

```go
sourceType := hub.EngineToType(engine)
host := ""
if hasCollection {
    host = conn.SourceHost(ctx, name)
}
plan, err := ddl.Detach(name, hasCollection)
```

The host and type are read **before** anything is dropped, because the audit
log needs them and they will not exist afterwards. Order matters when you are
destroying the thing you want to describe.

`doTestSource` is deliberately independent of ClickHouse:

> doTestSource dials the source directly with its own pure-Go driver and
> never touches ClickHouse at all: no named collection, no database, nothing
> to clean up.

This is why the "Test connection" button in the UI is fast and leaves no mess
— it is a direct connection from the Superset container to your database
using Go's own PostgreSQL/MySQL/SQLite drivers.

Note the comment about why it only checks the host once, unlike
`attachSource`: here there is no gap between checking and connecting, because
this call *is* the connection.

---

## [sqlitefiles.go](../cmd/fedctl/sqlitefiles.go) — receiving browser uploads

`seed.go` copies a file from *your host*. This file handles a file arriving
from *a browser*, inside the Superset container, where there is no Docker
socket and no host filesystem to reach.

`putSQLiteFile` is a good example of careful file handling:

```go
tmp, err := os.CreateTemp(dir, ".upload-*")     // 1. write to a temp file
tmpPath := tmp.Name()
defer os.Remove(tmpPath)                        // 2. clean up if we fail

n, copyErr := io.Copy(tmp, io.LimitReader(r, maxSQLiteUploadBytes+1))  // 3. bounded copy
...
if n > maxSQLiteUploadBytes { return ...error }  // 4. enforce the limit
os.Chmod(tmpPath, 0o644)
os.Rename(tmpPath, dest)                         // 5. atomic move into place
```

Four separate protections in fifteen lines:

- **`io.LimitReader`** caps the read at 200 MiB + 1 byte. Without it, a
  browser could fill the volume. Reading one byte *past* the limit is how the
  code detects an oversized upload rather than silently truncating it.
- **Temp file + rename.** `os.Rename` within one filesystem is atomic — it
  either happens completely or not at all. So a client that disconnects
  halfway never leaves a corrupt file at the real name, and ClickHouse never
  reads a half-written database.
- **`defer os.Remove`** cleans up the temp file on every failure path. After
  a successful rename it is a harmless no-op, since the file has moved.
- **Refusing to overwrite** unless `--replace` was passed.

That last one is a product decision expressed in code. SQLite has no live
connection to re-read, so refreshing a source's data means replacing the
file's bytes. That is what the UI's **Replace** button does. But a plain
upload that happens to reuse a name must *not* silently overwrite a file some
attached source is reading. So the two intentions get two different commands.

Filename validation reuses `seedNameRe` from `seed.go` — one filename policy
regardless of how the bytes arrived.

---

## [apply.go](../cmd/fedctl/apply.go) — the declarative path

`fedctl apply` reads [config/sources.yaml](../config/sources.yaml) and
attaches everything in it. `fedctl plan` prints what `apply` *would* do
without doing it. If you have used Terraform, this is the same plan/apply
idea.

The loop is careful about failures:

```go
for _, spec := range sf.Sources {
    params, err := spec.ResolveParams()
    if err != nil {
        results = append(results, applyResult{Name: spec.Name, OK: false, Note: err.Error()})
        failed = true
        continue          // ← keep going
    }
    ...
}
```

One broken entry does not stop the others. You get a full report of what
succeeded and what did not, then a non-zero exit code. Compare that to
failing on the first error, which would make you fix problems one run at a
time.

Idempotence comes free: `attachSource` generates `CREATE ... IF NOT EXISTS`,
so re-running is a no-op. There is no separate "does this already exist?"
branch to get wrong.

And the safety of `plan`:

```go
// Password is never resolved into DDL text — it is always bound as a "?"
// placeholder — so printing the plan is safe...
```

`fedctl plan` prints real SQL, and that SQL contains `?` where the password
belongs. Printing it cannot leak anything, structurally, rather than because
someone remembered to redact.

---

## [doctor.go](../cmd/fedctl/doctor.go) — is everything actually working?

Lists every source and probes each one:

```go
for _, s := range list {
    status := "ok"
    if err := conn.Probe(ctx, reader, ddl.DatabaseName(s.Name)); err != nil {
        status = "FAILED: " + err.Error()
        anyFailed = true
    }
    fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, s.Type, s.TableCount, status)
}
```

Note the two connections again — `conn` is `fed_admin` for catalog queries,
`reader` is `bi_ro` for the actual data-touching query. And note that it
probes everything before returning a non-zero exit code, so one dead source
does not hide a second one.

"Probe" here means something specific: not "does the database definition
exist" but "can ClickHouse open the upstream connection *right now*". Chapter
5 covers how `hub.Probe` forces that to be true.

---

Next: [The internal Go packages](05-internal-packages.md) — where the actual
logic lives.
