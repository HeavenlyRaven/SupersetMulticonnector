# 10. Glossary

Every piece of jargon in this repository, defined. Alphabetical.

---

**Alpha / Admin / Gamma** — Superset's built-in roles. Admin can do
everything; Alpha can create content and access all data sources; Gamma is
limited. This extension's `can_manage` permission goes to Admin
automatically; Alpha needs it granted by hand.

**argv** — the array of command-line arguments a program receives. On Linux
it is readable by other users through `/proc/<pid>/cmdline`, which is why
this project forbids passing credentials as flags.

**Atomic** — all-or-nothing. `hub.Attach` is atomic in the sense that a
failure partway through rolls back what already succeeded. `os.Rename` is
atomic in the filesystem sense: a reader sees either the old file or the new
one, never a half-written one.

**bi_ro** — the read-only ClickHouse user Superset connects as. `readonly=2`,
tuned for federated joins. Defined in
[roles.xml](../clickhouse/users.d/roles.xml).

**Bind mount** — mapping a host directory into a container. Powerful and
problematic (ownership, SELinux). This project allows it only for read-only
configuration directories.

**buildx** — Docker's extended builder, needed for multi-architecture images.
Checked by `fedctl preflight`.

**Celery** — a Python distributed task queue. Superset uses it for background
work: async SQL Lab queries, alerts, scheduled reports, thumbnails. **Not
present here**, which is why those features are off.

**cgroups** — the Linux kernel feature that enforces container resource
limits. v2 is required for accurate memory accounting; `fedctl preflight`
warns on v1.

**CGO** — Go's mechanism for calling C. Disabling it (`CGO_ENABLED=0`)
produces a static binary that cross-compiles freely and runs anywhere. A
project-wide rule here.

**CIDR** — a notation for a range of IP addresses: `10.0.0.0/8` means the
first 8 bits are fixed, covering `10.0.0.0`–`10.255.255.255`.

**ClickHouse** — a column-oriented analytics database. Here it is the
*federation hub*: it attaches PostgreSQL, MySQL, and SQLite databases and
joins across them live.

**clickhouse-connect** — the Python driver and SQLAlchemy dialect Superset
uses to query ClickHouse over HTTP (port 8123).

**Cobra** — the Go library that builds `fedctl`'s command tree, flags, and
help text.

**Compose (v1 vs v2)** — v1 was a standalone `docker-compose` program; v2 is
a `docker compose` plugin. This project requires v2 and detects v1 to give a
useful error.

**Compose Specification** — the current, versionless Compose file format. If
a file starts with `version: "3.8"`, it predates it.

**Container** — a running instance of an image. Its writable layer is
discarded when it is removed, which is why persistent data lives in volumes.

**CSRF (Cross-Site Request Forgery)** — an attack where another site makes
your browser send an authenticated request to an application you are logged
into. Prevented by a token the server issues and the client must echo back.

**DDL / DML** — Data Definition Language (`CREATE`, `DROP` — structure) vs.
Data Manipulation Language (`INSERT`, `UPDATE` — data). This project is
almost entirely DDL.

**Defence in depth** — enforcing the same rule at multiple independent
layers, so one failure is not a breach. Examples here: `bi_ro` is read-only
in ClickHouse *and* Superset's connection sets `allow_dml: false`; source
names are regex-validated *and* backtick-quoted.

**Digest** — a cryptographic hash identifying exact image content
(`@sha256:...`). Unlike a tag, it cannot be changed by the publisher.

**DSN (Data Source Name)** — a connection string packing driver, credentials,
host, port, and database into one value.

**Engine (ClickHouse)** — what backs a table or database. `MergeTree` stores
data locally; `PostgreSQL`, `MySQL`, and `SQLite` proxy to a foreign system
live.

**Envelope** — the `{"ok": true, "data": ...}` / `{"ok": false, "error": ...}`
wrapper every `--json` command writes. Defined in
[jsonio.go](../internal/jsonio/jsonio.go).

**fed_admin** — the ClickHouse user `fedctl` connects as. Can create and drop
databases and named collections; deliberately **cannot read federated data**.

**Federation** — querying multiple independent databases as though they were
one, without copying data. The premise of this project.

**Flask-AppBuilder (FAB)** — the framework layer providing Superset's REST
APIs, authentication, and permission model. Source of `@expose`, `@protect`,
`@permission_name`.

**Golden test** — a test asserting output matches an exact expected value
character for character. Used for generated SQL.

**grace_hash** — a ClickHouse join algorithm that spills to disk instead of
failing when a join exceeds memory. Listed first in the `bi_readonly` profile
because federated join sizes are unpredictable.

**gunicorn** — the Python web server running Superset. Forks
`WEB_CONCURRENCY` worker processes. Without Celery, a running query holds one
for its whole duration.

**Health check** — a command Docker runs inside a container to decide whether
it is working. Here, always `fedctl check <target>`.

**Idempotent** — running twice has the same effect as running once. `fedctl
apply` and `fedctl up` both are.

**Image** — a frozen filesystem plus a default command; the template a
container is created from.

**information_schema** — the SQL-standard set of metadata views. Used to
count tables when testing a connection.

**init() (Go)** — a function that runs automatically when a package loads.
Used by each source type to register itself.

**internal/ (Go)** — a directory whose packages can only be imported within
the same module. Compiler-enforced privacy.

**JWT** — a signed token proving an authenticated session. `fedctl` obtains
one from Superset's login endpoint.

**Least privilege** — granting the minimum permissions needed. Produced the
two-role ClickHouse setup here.

**Live (as opposed to materialised)** — data read from the source at query
time rather than copied in advance. Everything here is live; the spec
explicitly forbids `MaterializedPostgreSQL` and `MaterializedMySQL`.

**Module Federation** — a Webpack feature letting a running application load
separately-built code at runtime. How the extension's panel gets into
Superset.

**Named collection (ClickHouse)** — a stored, named set of connection
parameters including secrets. Referenced by name in DDL, so the password
never appears in a statement. ClickHouse redacts *every* field when read
back — which is why this project stores the host in the database's `COMMENT`
instead.

**Named volume** — Docker-managed storage identified by name, surviving
container replacement. `ch-data`, `ch-user-files`, `superset-home`.

**OOM killer** — the Linux kernel component that kills processes when memory
runs out. `max_server_memory_usage_to_ram_ratio` exists to keep ClickHouse
from provoking it.

**pgx** — the pure-Go PostgreSQL driver used by `fedctl`. Its *fallback*
behaviour was the source of an SSRF near-miss; see
[chapter 5](05-internal-packages.md).

**Preflight** — checks run before starting the stack, so failures are
diagnosed early with actionable messages.

**Probe** — a check that a source is reachable *right now*, by actually
selecting from it. Distinct from "the database definition exists", because
ClickHouse connects lazily.

**Profile (ClickHouse)** — a named set of user settings. `bi_readonly`
carries the join and cache tuning.

**Profile (Compose)** — a label that keeps a service from starting unless
explicitly requested. Used by `compose.dev.yaml`.

**readonly = 2** — a ClickHouse setting: cannot modify data, but may change
settings within a query. What `bi_ro` uses.

**Redaction** — replacing secret values with `***` in text before logging.
Last-resort defence; secrets are supposed to never reach a loggable string.

**Registry (Go pattern)** — a map from name to implementation, populated by
each implementation's `init()`. Makes source types pluggable.

**SELinux** — a mandatory access control system on Red Hat-family Linux. The
`:z` suffix on a bind mount relabels it so the container may read it.

**Shim** — a thin adapter layer. Here, the Python code that translates HTTP
requests into `fedctl` subprocess calls.

**Slice (Go)** — a view over an array. `plan.Steps[:i]` is "everything before
index `i`", used to roll back completed steps.

**SQL Lab** — Superset's interactive SQL editor, and where the extension's
panel appears.

**SQLAlchemy** — the Python database toolkit Superset uses. Its URI format
(`postgresql+psycopg2://...`) is what `SQLALCHEMY_DATABASE_URI` holds.

**SQLite** — a database that is a single file with no server. Its file nature
drives all of this project's special handling: uploads, a shared volume, path
validation, and the Replace button.

**SSRF (Server-Side Request Forgery)** — tricking a server into making
network requests on your behalf, using its network position. The main
security concern in a "add a data source by hostname" feature.

**Static binary** — an executable with no external library dependencies.
Produced by `CGO_ENABLED=0`, which is why one `fedctl` runs in both images
and on Windows.

**Superset (Apache Superset)** — the open-source BI web application this
project builds around.

**superset_config.py** — the Python file Superset imports at startup to
configure itself. Here it also validates and can refuse to start.

**.supx** — a packaged Superset extension bundle.

**Table-driven test** — a Go idiom: a slice of test cases looped over, so
adding a case is one line.

**TOCTOU (Time Of Check, Time Of Use)** — a bug where something changes
between validation and use. Here: DNS changing between validating a host and
connecting to it. Defended by checking twice.

**Type guard (TypeScript)** — a function returning `x is T`, narrowing a
type for the compiler. `isApiError` is one.

**user_files** — the directory ClickHouse permits local file access in.
Backed by the `ch-user-files` volume; the only place a SQLite source may
live.

**Volume** — see *named volume*.

---

## Where to go next

You have read the whole system. Some suggestions, roughly in order of
usefulness.

### Run it

Nothing else teaches as fast. From the repo root:

```bash
go build -o fedctl.exe ./cmd/fedctl     # Windows
./fedctl.exe install
```

You need Docker Desktop running and a PostgreSQL somewhere for Superset's
metadata (a free hosted one is fine). Then add `--dev` to get sample
databases and try attaching them:

```bash
./fedctl.exe up --dev
./fedctl.exe source add --name devpg --type postgres \
    --host dev-postgres --port 5432 --user sample --database sampledb
./fedctl.exe source list
./fedctl.exe doctor
```

(Remember `FEDCTL_ALLOWED_SOURCE_HOSTS=dev-postgres,dev-mysql` in `.env` —
otherwise your own SSRF guard blocks them, which is itself a good lesson.)

### Break it on purpose

The fastest way to understand a safety mechanism is to trip it.

- Add a source named `foo; DROP DATABASE default`.
- Add one with host `169.254.169.254`.
- Add a SQLite source with path `../../etc/passwd`.
- Try `fedctl source add --password hunter2`.
- Run `fedctl apply` twice and watch nothing happen the second time.
- Stop the dev PostgreSQL container and run `fedctl doctor`.

Each produces a specific, deliberate error. Find the code that produced it.

### Add a source type

The best exercise in the repo, because the design promises it is easy. Add a
new file `internal/sources/mssql.go`:

1. Implement the six `Type` methods.
2. `func init() { Register(mssqlType{}) }`.
3. Add `ddl.AttachMSSQL` in [ddl.go](../internal/ddl/ddl.go).
4. Add a case in `buildAttachPlan` in
   [sourceops.go](../cmd/fedctl/sourceops.go).
5. Add a `TABLE ENGINE` grant in
   [roles.xml](../clickhouse/users.d/roles.xml).

Write no TypeScript. The Add Source form will render your fields because it
reads them from `GET /source-types`. If it does, you have understood the
architecture.

### Read the spec

[superset-clickhouse-two-container-spec.md](../superset-clickhouse-two-container-spec.md)
is the requirements document the whole project was built from. Having read
the code, you can now read it as a checklist and verify each item yourself.
Section 14's acceptance criteria are especially worth walking through.

### Learn the underlying pieces properly

| Topic | Suggestion |
|---|---|
| **Go** | [A Tour of Go](https://go.dev/tour/) then [Effective Go](https://go.dev/doc/effective_go). The interface and error chapters are the ones that matter for this codebase. |
| **Docker** | Docker's own "Get Started" guide, then the Compose Specification. Focus on volumes and networks — they cause most real problems. |
| **SQL** | Write joins by hand until they are boring. Then read about query planning. |
| **ClickHouse** | The official docs' "Database Engines" section covers exactly the feature this project is built on. |
| **Superset** | Run it standalone once, without this project, to see what the extension is adding. |
| **Security** | The OWASP pages on SSRF and Path Traversal. You have now seen both defended in real code, which makes the reading much more concrete. |

### Study the comments

Search the repository for `Live-verified`:

```bash
grep -rn "Live-verified" --include="*.go" --include="*.xml" --include="*.yaml" --include="*.ts" --include="*.tsx" .
```

Every hit is a bug someone hit against a real system, diagnosed, and wrote
down where the next person would find it. Reading those in one sitting is a
compressed education in how software actually fails — and a good model for
how to leave a codebase better than you found it.

---

[Back to the index](README.md)
