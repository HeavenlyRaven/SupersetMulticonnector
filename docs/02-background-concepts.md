# 2. Background concepts

This chapter is a reference, not a novel. Skim it once so you know what is in
it, then come back whenever a term in the code stops you.

---

## Part A — Docker

### Image vs. container

An **image** is a frozen filesystem plus a default command. Think of it as an
installer that has already been run: it contains the operating system files,
the installed program, and its dependencies, all as one immutable blob.

A **container** is one running instance of an image. The image is the class;
the container is the object. You can start ten containers from one image, and
each gets its own writable layer on top, discarded when the container is
removed.

**This matters constantly:** anything a container writes that is not in a
volume disappears when the container is replaced. That is why Superset's
metadata cannot live inside the container.

### Dockerfile and layers

A `Dockerfile` is a recipe for building an image. Each instruction produces a
layer, and layers are cached — which is why you see this ordering pattern in
[images/superset/Dockerfile](../images/superset/Dockerfile):

```dockerfile
COPY go.mod go.sum ./
RUN go mod download        # ← cached unless dependencies change
COPY cmd/ cmd/
COPY internal/ internal/
RUN go build ...           # ← only this re-runs when you edit code
```

Dependencies are copied and downloaded *before* the source code, so editing a
`.go` file does not invalidate the (slow) dependency download step.

### Multi-stage builds

Both Dockerfiles here start with **two** `FROM` lines. The first stage builds
the Go binary; the second stage is the actual final image and copies just the
compiled binary out of the first:

```dockerfile
FROM golang:1.25-bookworm AS fedctl-build   # stage 1: has the Go compiler
RUN go build -o /out/fedctl ./cmd/fedctl

FROM apache/superset:6.1.0                  # stage 2: the real image
COPY --from=fedctl-build /out/fedctl /usr/local/bin/fedctl
```

The final image never contains the Go compiler — only the ~10 MB binary it
produced. Smaller image, smaller attack surface.

### Tags and digests

`apache/superset:6.1.0` is a **tag**. Tags are mutable: whoever owns that
repository can republish a different image under the same tag tomorrow.
`@sha256:59cd4af6...` is a **digest** — a cryptographic hash of the exact
image content, which cannot change.

This repo pins both:

```dockerfile
FROM apache/superset:6.1.0@sha256:59cd4af66006fe4cc98906eda42a771dbefdacb432f9ab083e02cdc6ff01f29d
```

The tag is for humans, the digest is the guarantee. Same idea as a lockfile
in a package manager.

### Volumes: named volumes vs. bind mounts

Containers need somewhere durable to put data. Two options:

**Bind mount** — map a directory from the host machine into the container:
`-v /home/me/data:/var/lib/clickhouse`. Simple, but it drags in host-specific
problems: file ownership mismatches (the container's user ID may not match
yours), and SELinux relabelling on Red Hat systems.

**Named volume** — ask Docker to manage a storage area by name:
`-v ch-data:/var/lib/clickhouse`. Docker owns it, it survives container
replacement, and it has no host path to get wrong.

This project uses named volumes for all data, and allows bind mounts *only*
for read-only configuration directories. There are three named volumes:

| Volume | Mounted at | Holds |
|---|---|---|
| `ch-data` | `/var/lib/clickhouse` | ClickHouse's own storage: system tables, the query log, any native tables |
| `ch-user-files` | `/var/lib/clickhouse/user_files` | Uploaded SQLite files — the only place ClickHouse will read local files from |
| `superset-home` | `/app/superset_home` | Superset's file cache |

Note that `ch-user-files` is mounted into **both** containers. That is
unusual enough that [compose.yaml](../compose.yaml) has a long comment
explaining why: the Superset container needs to write uploaded SQLite files
there, and the ClickHouse container needs to read them.

The `:z` suffix you see on the config mounts (`:ro,z`) tells Docker to
relabel the directory for SELinux so the container is permitted to read it.
Without it, these mounts fail on Fedora/RHEL hosts with a confusing
permission error.

### Networks

Containers on the same Docker network can reach each other **by service
name**. In [compose.yaml](../compose.yaml) both services join the network
called `federation`, so inside the Superset container the hostname
`clickhouse` resolves to the ClickHouse container. That name only exists
inside Docker — from your laptop, `clickhouse` means nothing.

This causes a subtle bug class that this codebase hit and documented: code
running *inside* a container and code running *on your host* need different
addresses for the same service. See the comments in
[health.go](../internal/health/health.go) and
[config.go](../internal/config/config.go) about why the health check dials
`localhost:8088` but the host-side registration code dials
`localhost:${SUPERSET_PORT}`.

### Ports

`ports: - "8088:8088"` means "publish container port 8088 as host port 8088",
making it reachable from your browser. **Only the Superset service publishes
a port here.** ClickHouse deliberately publishes none — it is reachable only
from inside the Docker network, which is a meaningful security property: the
federation hub is not exposed to your LAN at all.

### Docker Compose

Compose describes a multi-container application in one YAML file, so
`docker compose up` starts everything with the right networks, volumes, and
ordering. Two things about the version:

- **Compose v1** was a separate Python program invoked as `docker-compose`
  (with a hyphen). It is obsolete.
- **Compose v2** is a plugin to the Docker CLI invoked as `docker compose`
  (with a space).

This project requires v2 and detects v1 to give you a useful error — see
`checkComposeV2` in [preflight.go](../internal/preflight/preflight.go).

Modern Compose files do **not** have a `version:` key at the top. If you see
`version: "3.8"` in a tutorial, that tutorial predates the current spec.

### Health checks

A container can declare how to test whether it is actually working:

```yaml
healthcheck:
  test: ["CMD", "/usr/local/bin/fedctl", "check", "clickhouse"]
  interval: 15s
  start_period: 30s
  retries: 5
```

Docker runs that command inside the container periodically and marks the
container `starting`, `healthy`, or `unhealthy`. Other services can then wait
for it:

```yaml
depends_on:
  clickhouse:
    condition: service_healthy
```

Superset will not start until ClickHouse reports healthy. Note the test is
the project's own Go binary, not `curl` — the comment in the spec explains
why: `curl`, `wget`, and `nc` are present in some base images and absent from
others, so depending on them makes health checks fragile. A binary you ship
yourself is always there.

### Profiles

`compose.dev.yaml` marks its sample databases with `profiles: ["dev"]`.
Services with a profile do **not** start unless the profile is explicitly
requested. This is how the sample PostgreSQL and MySQL databases can live in
the repo without ever being part of a production deployment.

---

## Part B — Databases

### DDL vs. DML

- **DDL** (Data Definition Language): statements that change *structure* —
  `CREATE DATABASE`, `DROP TABLE`, `CREATE NAMED COLLECTION`.
- **DML** (Data Manipulation Language): statements that change *data* —
  `INSERT`, `UPDATE`, `DELETE`.

This project generates a lot of DDL and essentially no DML. The package
[internal/ddl/](../internal/ddl/) is named after this distinction.

### The four database systems involved

| System | Role here | Why it is here |
|---|---|---|
| **ClickHouse** | The federation hub | A column-oriented analytics database, very fast at large aggregations and joins, and — crucially — it can attach foreign databases as engines |
| **PostgreSQL** | Attachable source, *and* Superset's metadata store | The most common serious open-source relational database |
| **MySQL** | Attachable source | The other most common one |
| **SQLite** | Attachable source | A database that is just a single file, with no server at all |

SQLite being a *file* rather than a *service* is the reason it needs special
handling all over this codebase: you cannot connect to it over the network,
so the file itself must physically be present in a volume both containers can
see.

### ClickHouse-specific concepts you will meet

**Database engine.** Explained in [chapter 1](01-what-this-project-is.md).
`ENGINE = PostgreSQL(...)` makes a ClickHouse database a live proxy for a
remote one.

**Named collection.** ClickHouse's built-in store for connection secrets:

```sql
CREATE NAMED COLLECTION fed_sales_creds AS
    host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?;
CREATE DATABASE fed_sales ENGINE = PostgreSQL(fed_sales_creds);
```

The credentials go in once, by name, and the database definition just
references the name. The password never appears in the `CREATE DATABASE`
statement — which means the statement is safe to print, log, and show in
`fedctl plan` output.

There is a wrinkle this codebase discovered the hard way and documented in
[hub.go](../internal/hub/hub.go): ClickHouse redacts **every** field of a
named collection as `[HIDDEN]` when you read it back, including harmless
ones like `host`. So to be able to display "this source points at
db.example.com" in the UI later, the code stores the host separately in the
database's `COMMENT`. That is what `hostComment` and `SourceHost` are for.

**System tables.** ClickHouse exposes its own metadata as queryable tables:
`system.databases`, `system.tables`, `system.one`. Nearly all of this
project's introspection is ordinary `SELECT`s against those.

**`readonly = 2`.** A ClickHouse user setting. `readonly=1` means "cannot
change anything, including settings"; `readonly=2` means "cannot change data,
but may adjust settings within a query". Superset needs the latter because it
sets per-query options.

### `information_schema`

PostgreSQL and MySQL both expose a standard set of metadata views called
`information_schema`. This project uses it to count tables when testing a
connection:

```sql
SELECT count(*) FROM information_schema.tables WHERE table_schema = $1
```

That count is what the UI shows as "Reachable — 3 table(s) visible."

### Connection strings / DSNs

A **DSN** (Data Source Name) packs connection details into one string:

```
postgresql+psycopg2://superset:secret@postgres.example.internal:5432/superset
└────────┬────────┘   └───┬───┘ └─┬──┘ └───────────┬──────────────┘ └┬─┘ └──┬──┘
      driver           user   password           host              port  database
```

The `+psycopg2` part names the specific Python driver SQLAlchemy should use.
You will see DSNs for Superset's metadata database, for the ClickHouse
connection Superset registers (`clickhousedb://...`), and inside `fedctl`'s
own connection code.

---

## Part C — Apache Superset

**Superset** is an open-source business intelligence web application: you
connect it to databases, explore data, build charts, and assemble dashboards.
It is written in Python (Flask) with a React frontend.

Concepts you need:

**Metadata database.** Superset's own storage for users, dashboards, saved
queries, and permissions. Configured with `SQLALCHEMY_DATABASE_URI`. Distinct
from the databases you *analyse* — see [chapter 1](01-what-this-project-is.md).

**Database connection.** A registered data source that appears in the UI.
This project registers exactly one automatically: the ClickHouse hub, added
by `fedctl up` through Superset's REST API. See
[superset.go](../cmd/fedctl/superset.go).

**SQL Lab.** The interactive SQL editor inside Superset. This is where the
federated joins get typed, and where the extension's panel appears.

**`superset_config.py`.** A Python file Superset imports at startup to
configure itself. Because it is real Python, it can read environment
variables and even exit the process — which this project's
[superset_config.py](../superset/superset_config.py) does if the metadata
URI is missing.

**gunicorn workers.** Superset runs behind gunicorn, a Python web server that
forks a pool of worker processes. `WEB_CONCURRENCY` sets the pool size. With
no Celery, a running SQL Lab query occupies one worker until it finishes —
which is exactly why this stack sets a generous worker count and a firm
`SQLLAB_TIMEOUT`.

**Flask-AppBuilder (FAB).** The framework layer Superset uses for its REST
APIs and its permission system. The decorators in the extension's Python file
(`@expose`, `@protect`, `@permission_name`) come from FAB.

**Permissions.** FAB permissions are pairs of "action on resource", like
`can_manage on ChFederationAPI`. Roles (Admin, Alpha, Gamma) hold sets of
these. The extension declares the permissions it needs in
[extension.json](../extension/extension.json).

**Extensions.** A newer Superset feature that lets you add backend endpoints
and frontend views without forking Superset. A built extension is a `.supx`
bundle. This framework is **pre-1.0 and unstable**, which the code and README
both flag repeatedly — if the panel ever fails to load after a Superset
upgrade, that is the first thing to suspect.

**CSRF token.** A protection against Cross-Site Request Forgery: a token the
server issues and the browser must echo back on every state-changing request,
proving the request came from your actual page and not from a malicious site
you happened to also have open. Superset requires it; the extension's
frontend fetches one for every mutation in [api.ts](../extension/frontend/src/api.ts).

---

## Part D — Go, as used in this repository

You do not need to be a Go expert. You need these specific things.

### Modules and packages

[go.mod](../go.mod) declares the module — its name and its dependencies:

```go
module github.com/acme/superset-federation
go 1.25.0
require github.com/spf13/cobra v1.10.2
```

The module name is the import prefix, which is why files import
`github.com/acme/superset-federation/internal/validate`. It does not have to
exist on GitHub; it is just a name.

[go.sum](../go.sum) records cryptographic hashes of every dependency version,
so a tampered dependency is detected at build time. Do not edit it by hand.

A **package** is a directory of `.go` files sharing a namespace. Files in the
same package can call each other's functions without imports.

### `cmd/` and `internal/`

Two conventions you will see in almost every Go project:

- **`cmd/<name>/`** contains a `main` package — a program that can be built
  into an executable. Here, `cmd/fedctl` becomes the `fedctl` binary.
- **`internal/`** is special to the Go toolchain: packages under it can only
  be imported by code in the same module. It is a compiler-enforced "this is
  our private implementation" boundary.

### Exported vs. unexported

Capitalisation *is* the access modifier. `func Host(...)` is exported and
usable from other packages; `func denied(...)` is private to its package.
When you see a lowercase function, it is deliberately internal.

### Interfaces

An interface is a list of method signatures. Any type with those methods
satisfies it — no `implements` keyword, no inheritance:

```go
type Type interface {
    Name() string
    Engine() string
    HasCredentials() bool
    Fields() []Field
    Validate(userFilesDir string, params Params) (Params, error)
    TestConnection(ctx context.Context, userFilesDir string, params Params) (int, error)
}
```

That is [registry.go](../internal/sources/registry.go). `postgresType`,
`mysqlType`, and `sqliteType` each implement those six methods, so each can
be stored and used as a `Type`. This is the mechanism that makes "adding a
new database engine is one Go file" true.

### `init()` and registration

A function named `init()` runs automatically when its package is loaded:

```go
func init() { Register(postgresType{}) }
```

Each source file registers itself. Nothing central has to maintain a list —
adding `mssql.go` with its own `init()` is enough to make it appear
everywhere, including the web form.

### Errors are values

Go has no exceptions. Functions return an error alongside their result, and
you check it:

```go
plan, err := ddl.AttachPostgres(name, params)
if err != nil {
    return err
}
```

The repetitive `if err != nil` is normal Go. This project layers on a custom
error type carrying a machine-readable code — see
[jsonio.go](../internal/jsonio/jsonio.go) — so the same error can become both
a friendly CLI message and an HTTP status code.

`%w` in `fmt.Errorf("...: %w", err)` **wraps** an error, preserving the
original for later inspection with `errors.As`.

### `defer`

`defer` schedules a call to run when the surrounding function returns, no
matter how it returns:

```go
conn, err := openHub(ctx, app)
if err != nil { return err }
defer conn.Close()      // guaranteed to run
```

This is Go's answer to `try/finally`, and you will see it on every connection
and file handle in the codebase.

### `context.Context`

A `context.Context` carries cancellation and deadlines through a call chain.
When you see:

```go
ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()
```

it means "everything below this gets ten seconds, then gets cancelled".
Passing `ctx` as the first argument to anything that does I/O is the
convention, and it is what makes a hung database connection eventually give
up instead of hanging forever.

### Struct tags

The backtick strings on struct fields control encoding:

```go
type Response struct {
    OK    bool       `json:"ok"`
    Data  any        `json:"data,omitempty"`
    Error *ErrorInfo `json:"error,omitempty"`
}
```

`json:"ok"` names the JSON field; `omitempty` drops it when empty. The same
mechanism with `yaml:"..."` drives the parsing of
[config/sources.yaml](../config/sources.yaml).

### Build tags

A comment at the very top of a file can restrict which platforms compile it:

```go
//go:build windows
```

That is how [internal/preflight/](../internal/preflight/) has three files for
the same job — `resources_windows.go`, `resources_linux.go`,
`resources_darwin.go` — each using its own operating system's API to read
free disk space and RAM. Exactly one is compiled into any given build.

### `CGO_ENABLED=0`

Go can call C libraries, but doing so ties the resulting binary to the C
library on the build machine — breaking cross-compilation and failing at
runtime on Alpine Linux (which uses musl instead of glibc). Setting
`CGO_ENABLED=0` forbids it, producing a **fully static binary** that runs on
any Linux with no dependencies at all.

This is why the project's SQLite driver is `modernc.org/sqlite` (a pure-Go
reimplementation) rather than the more popular `mattn/go-sqlite3` (a C
binding). It is a deliberate constraint, listed in the spec, and it is why
one `fedctl` binary can be copied into both container images and also run
natively on Windows.

### Cobra

[Cobra](https://github.com/spf13/cobra) is the library that turns Go
functions into a CLI with subcommands, flags, and help text. Its shape:

```go
cmd := &cobra.Command{
    Use:   "check <target>",
    Short: "Run a healthcheck probe",
    Args:  cobra.ExactArgs(1),
    RunE:  func(cmd *cobra.Command, args []string) error { ... },
}
cmd.Flags().BoolVar(&jsonMode, "json", false, "write a jsonio envelope")
```

`RunE` is the handler (the `E` means it returns an error). Commands are
assembled into a tree in `newRootCmd()` in [main.go](../cmd/fedctl/main.go).

---

## Part E — Security vocabulary

**SSRF (Server-Side Request Forgery).** You trick a server into making a
network request on your behalf. Dangerous because the server is *inside* the
network — it can reach things you cannot, like `169.254.169.254`, the cloud
metadata endpoint that hands out credentials. Any feature where a user
supplies a hostname the server then connects to is a potential SSRF, which is
exactly what "add a data source" is.

**TOCTOU (Time Of Check, Time Of Use).** A bug where you validate something,
and then by the time you *use* it, it has changed. Here: you resolve
`db.example.com`, see a public IP, approve it — and by the time ClickHouse
connects, DNS has been changed to point at `127.0.0.1`. The fix is to check
again immediately before use, which
[sourceops.go](../cmd/fedctl/sourceops.go) does explicitly.

**argv leakage.** On Linux, `/proc/<pid>/cmdline` is readable by other users,
so a password passed as `--password hunter2` is visible to everyone on the
machine for as long as the process lives. Hence this project's rule:
credentials on stdin, never in arguments — enforced by `guardCredentialFlags`
in [main.go](../cmd/fedctl/main.go).

**Least privilege.** Give each identity the smallest set of permissions that
lets it do its job. Here it produced two ClickHouse users: `fed_admin` can
create and drop databases but **cannot read any federated data**, and `bi_ro`
can read data but cannot change anything. See
[roles.xml](../clickhouse/users.d/roles.xml).

**Idempotent.** Running it twice has the same effect as running it once.
`fedctl apply` is idempotent because every statement it generates says
`IF NOT EXISTS`.

**Atomic.** All-or-nothing. When attaching a source, if the second statement
fails, the first is undone, so you never end up with a stray named collection
and no database. See `Attach` and `rollback` in
[hub.go](../internal/hub/hub.go).

---

Next: [Repository map](03-repository-map.md) — every file, one line each.
