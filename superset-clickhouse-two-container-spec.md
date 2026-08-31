# Build spec: Superset + ClickHouse hub, two containers, Go tooling

It is a specification. Implement every
section, then verify against the acceptance criteria.

---

## 1. The stack

Exactly two containers.

| Container | Contents |
|---|---|
| `superset` | Superset 6.1.x + `clickhouse-connect` + the `.supx` extension + the static `fedctl` binary |
| `clickhouse` | ClickHouse server (the federation hub) + `fedctl` for its healthcheck |

Three named volumes: `ch-data`, `ch-user-files`, `superset-home`.

Superset's metadata database is the **existing external PostgreSQL**, reached via
`SQLALCHEMY_DATABASE_URI`. Do not run a Postgres container, and do not fall back to SQLite for
metadata — that is a genuine reliability problem, not a style preference. Fail startup with a
clear message if the URI is unset or points at SQLite.

`compose.dev.yaml` may add sample source databases under a `dev` profile for local testing. That
overlay is explicitly not part of the production stack and must never be required by `fedctl up`.

---

## 2. What was removed, and what it costs

Document all of this in the README. These are accepted trade-offs, not oversights.

**Redis is gone.** Its three jobs disappear or move:

- *Cache* → `FileSystemCache` on `superset-home`, plus ClickHouse's own `use_query_cache` with a
  short TTL for repeated dashboard loads.
- *Async query results backend* → removed. SQL Lab runs synchronously.
- *Celery broker* → removed.

The cost, which must be stated plainly in the README: concurrent long queries hold gunicorn
workers, so enough of them will make the UI unresponsive for everyone. Mitigate by sizing the
worker pool generously and setting a firm `SQLLAB_TIMEOUT`. **Alerts and Reports, scheduled
reports, and thumbnails do not work without Celery.** If any of those become requirements, Redis
and a worker container come back; note this as the known re-expansion point.

**The separate Go controller service is gone.** The Python extension shim `exec`s the `fedctl`
binary that is already in the Superset image. This removes a container and removes the
shared-token authentication surface entirely.

**Nothing else was removed.** The Go policy, the no-shell policy, and the compatibility rules in
sections 3 and 4 are unchanged and still binding.

---

## 3. Language policy

| Layer | Language |
|---|---|
| CLI, provisioning, DDL generation, validation, health checks, seeding | **Go** |
| Superset extension backend | **Python**, thin shim only, target ~150 lines |
| Superset extension frontend | **TypeScript/React** |

Python and TypeScript are framework requirements: the extension system loads
`backend/src/{publisher}/{name}/entrypoint.py` via `apache-superset-core` and
`frontend/src/index.tsx` via Webpack Module Federation. Neither can be Go.

The Python shim contains **no DDL, no validation, no ClickHouse client**. It checks permissions,
resolves the Superset user, execs `fedctl`, parses JSON, and returns it. If you find yourself
writing logic there, move it to Go.

**No shell scripts anywhere.** No `.sh`, no `.bat`, no `entrypoint.sh`, no shell inside Makefile
recipes beyond invoking `fedctl`. Container entrypoints call the Go binary directly.

---

## 4. Compatibility rules

Targets: Linux (glibc and musl), macOS, Windows hosts, on amd64 and arm64.

### Go

- `CGO_ENABLED=0` always. Pure-Go drivers only:
  - ClickHouse: `github.com/ClickHouse/clickhouse-go/v2`
  - PostgreSQL: `github.com/jackc/pgx/v5`
  - MySQL: `github.com/go-sql-driver/mysql`
  - SQLite: `modernc.org/sqlite` — **not** `mattn/go-sqlite3`, which needs CGO
- Cross-compile: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`,
  with checksums.
- `filepath.Join` and `filepath.ToSlash`, never `/` concatenation. `os.UserConfigDir()`, never
  `$HOME`.
- No `exec.Command("sh", ...)`. The one permitted exec is the Python shim calling `fedctl`, with
  an argv array, never a shell string.

### Containers

- Both images multi-arch, built with `docker buildx`. Superset's release images are already
  multi-platform across `linux/amd64` and `linux/arm64`, as is ClickHouse's. Verify both
  manifests exist for each pinned tag before committing.
- Compose Specification format, **no `version:` key**. Require Compose v2; detect and reject v1
  in preflight.
- **No bind mounts except read-only config directories**, each with a `:z` suffix for SELinux
  hosts. SQLite files enter through `fedctl seed sqlite`, which copies into the `ch-user-files`
  named volume via a throwaway container. This removes UID-mapping and relabelling failures.
- Healthchecks invoke the baked-in binary: `["CMD", "/usr/local/bin/fedctl", "check", "<target>"]`.
  Never `curl`, `wget`, or `nc` — their presence varies by base image.
- Pin every image by tag **and** digest. No `container_name`.

### Repository

- `.gitattributes` with `* text=auto eol=lf`.
- No symlinks. All config paths are POSIX-style and interpreted inside containers.

---

## 5. Repository layout

```
superset-federation/
├── compose.yaml
├── compose.dev.yaml
├── .env.example
├── .gitattributes
├── .dockerignore
├── go.mod
├── cmd/fedctl/main.go
├── internal/
│   ├── config/          # sources.yaml parsing
│   ├── hub/             # ClickHouse client and DDL execution
│   ├── ddl/             # DDL generation per source type
│   ├── sources/         # postgres.go, mysql.go, sqlite.go, registry.go
│   ├── validate/        # identifier, host, path validation
│   ├── health/          # healthcheck targets
│   ├── preflight/       # host environment checks
│   └── jsonio/          # stdin request / stdout response contract
├── images/
│   ├── superset/Dockerfile
│   └── clickhouse/Dockerfile
├── clickhouse/
│   ├── config.d/federation.xml
│   └── users.d/roles.xml
├── superset/superset_config.py
├── extension/           # the .supx source
├── config/sources.yaml
└── README.md
```

---

## 6. `compose.yaml`

Two services on one internal network. Only Superset publishes a port.

- `clickhouse`: derived image, volumes `ch-data:/var/lib/clickhouse` and
  `ch-user-files:/var/lib/clickhouse/user_files`, config directories bind-mounted read-only with
  `:z`, `ulimits: nofile: {soft: 262144, hard: 262144}`, memory limit via
  `deploy.resources.limits.memory`. Not published.
- `superset`: derived image, volume `superset-home:/app/superset_home`, publishes `8088`,
  `depends_on: clickhouse: {condition: service_healthy}`, memory limit set.

No secrets in `compose.yaml`. Everything from `.env`; `.env.example` documents every key,
including the external PostgreSQL DSN for Superset metadata.

---

## 7. Images

### `images/superset/Dockerfile`

Superset's official images ship almost no database drivers by design, and deriving is the
documented production path. Multi-stage:

1. Go stage with `--platform=$BUILDPLATFORM` and `TARGETOS`/`TARGETARCH` build args, so
   cross-building does not emulate the compiler. Produces `fedctl`.
2. Derive from `apache/superset` (pinned tag + digest). Add `clickhouse-connect[sqlalchemy]`,
   `superset_config.py`, the built `.supx` (or `LOCAL_EXTENSIONS` wiring in dev), and `fedctl` at
   `/usr/local/bin/fedctl`.

### `images/clickhouse/Dockerfile`

Base ClickHouse (pinned tag + digest) plus `fedctl`. Nothing else.

---

## 8. ClickHouse configuration

`clickhouse/config.d/federation.xml`:

- `max_server_memory_usage_to_ram_ratio` at 0.8, so ClickHouse honours the container limit rather
  than reading host RAM.
- `user_files_path` at the default, where `ch-user-files` mounts.
- Bounded TTL on the query log so the volume cannot grow without limit.
- Query cache enabled at server level.

`clickhouse/users.d/roles.xml`:

- `fed_admin` — used only by `fedctl`. `CREATE DATABASE`, `DROP DATABASE`, named collection
  control.
- `bi_ro` — used by Superset. `readonly = 2`, and a profile tuned for federated joins:
  `join_algorithm = 'grace_hash,parallel_hash,hash'`, `max_bytes_in_join` and `max_memory_usage`
  from env, `max_execution_time = 300`, `join_use_nulls = 1`, `use_query_cache = 1` with a short
  `query_cache_ttl`.

All sources are **live**: `PostgreSQL`, `MySQL`, and `SQLite` database engines. No
`MaterializedPostgreSQL`, no `MaterializedMySQL`.

---

## 9. `fedctl`

Cobra-based. One binary, identical behaviour on all five targets. It is both the operator CLI and
the extension's backend implementation.

| Command | Behaviour |
|---|---|
| `preflight` | Compose v2 present, buildx present, both manifests exist for pinned images, disk, memory, cgroup version, SELinux presence, external Postgres reachable. Non-zero exit with actionable messages. |
| `up [--dev]` | Runs the stack, waits for healthchecks. |
| `down [--volumes]` | Tears down. |
| `seed sqlite --from <path> [--as <name>]` | Copies a host SQLite file into `ch-user-files`. |
| `source list \| add \| remove \| test \| tables \| probe` | Source management against the hub. |
| `apply [-f config/sources.yaml]` | Declarative reconcile. Idempotent. |
| `plan` | Prints DDL that `apply` would run, credentials redacted. |
| `check <target>` | Healthcheck probe: `clickhouse`, `superset`. |
| `doctor` | Connectivity probe per source, status table. |
| `version` | Version, commit, date, platform. |

`apply` and the `source` subcommands share one reconcile implementation. There must not be two.

### JSON mode — the contract with the Python shim

Every `source` subcommand accepts `--json`, which makes it read a JSON request from **stdin** and
write a JSON response to **stdout**, with logs on stderr and a meaningful exit code.

**Credentials arrive on stdin, never as arguments.** Argv is world-readable through `/proc` on
Linux, so a password in a flag is visible to every process on the host. Enforce this: if a
credential-shaped flag is passed, exit non-zero with an explanatory error.

Response envelope, always:

```json
{"ok": true, "data": {...}}
{"ok": false, "error": {"code": "HOST_NOT_ALLOWED", "message": "...", "hint": "..."}}
```

### Validation — security-critical, unit test every rule

1. **Source name** `^[a-z][a-z0-9_]{2,40}$`. Never interpolate identifiers; use a quoting helper
   and pass values as query parameters.
2. **Host** must resolve, and is rejected for `127.0.0.0/8`, `::1`, `169.254.0.0/16`,
   `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fd00::/8` unless an explicit allowlist
   permits it. Check every resolved address, and re-check immediately before connecting. Without
   this, an admin form becomes an SSRF primitive running inside your network.
3. **Port** 1–65535.
4. **SQLite path** must, after `filepath.Clean` and symlink resolution, be a regular file inside
   the `user_files` directory. Reject traversal and escaping symlinks.
5. **Credentials** go into ClickHouse named collections, never inline in DDL. Verify the
   parameter names against the pinned ClickHouse version before finalising; if a parameter is
   unsupported for an engine, fall back to positional arguments and ensure that statement is
   never logged.
6. Passwords scrubbed from every log line, error, and panic recovery.

### DDL

```sql
CREATE NAMED COLLECTION IF NOT EXISTS fed_sales_creds AS
    host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?;
CREATE DATABASE IF NOT EXISTS fed_sales ENGINE = PostgreSQL(fed_sales_creds);
```

MySQL takes the same shape with `ENGINE = MySQL(...)`. SQLite:

```sql
CREATE DATABASE IF NOT EXISTS fed_ref
  ENGINE = SQLite('/var/lib/clickhouse/user_files/app.sqlite');
```

Attach is atomic: a failure after the collection is created must roll it back.

---

## 10. Python shim

Scaffold with `superset-extensions init`. Publisher `acme`, name `ch-federation`.

Read these and report drift before coding — the framework is pre-1.0 at `0.0.x`:

- https://superset.apache.org/developer-docs/extensions/overview
- https://superset.apache.org/developer-docs/extensions/quick-start
- https://superset.apache.org/developer-docs/extensions/contribution-types
- https://superset.apache.org/developer-docs/extensions/extension-points/sqllab
- https://superset.apache.org/developer-docs/extensions/development
- https://superset.apache.org/developer-docs/extensions/deployment
- https://superset.apache.org/developer-docs/extensions/security

```python
from flask_appbuilder.api import expose, protect
from superset_core.rest_api.api import RestApi
from superset_core.rest_api.decorators import api

@api(id="ch_federation_api", name="ClickHouse Federation API",
     description="Manage live federated sources on the ClickHouse hub")
class ChFederationAPI(RestApi):
    ...
```

Each handler does exactly five things: `@protect()`, check `can_manage on ChFederation` for
mutating calls, `exec` `fedctl <cmd> --json` with an argv array and the request body on stdin,
parse the JSON response, write an audit log entry. Nothing else.

Rules:

- `subprocess.run` with a list, `shell=False`, an explicit timeout, and `capture_output=True`.
- Credentials go to stdin. Never into argv, never into an environment variable.
- Never return a password in a response, not even masked.
- Map `error.code` to an HTTP status; pass `message` and `hint` through unchanged.
- Audit every mutation to Superset's action log: user, source name, type, host, timestamp. Never
  credentials.
- Permissions: Admin and Alpha get `can_manage` by default; `GET /sources` may be broader.

Endpoints: `GET /source-types`, `GET /sources`, `POST /sources/test`, `POST /sources`,
`DELETE /sources/{name}`, `GET /sources/{name}/tables`, `POST /sources/{name}/probe`.

---

## 11. Frontend

Register a SQL Lab panel:

```tsx
views.registerView(
  { id: 'acme.ch-federation.panel', name: 'Federated sources' },
  'sqllab.panels',
  () => <FederationPanel />,
);
```

Confirm whether a contribution point exists outside SQL Lab; if not, build there and record the
limitation in the README.

- Source table: name, type, host, table count, health dot, last checked, actions (probe, remove).
- Add-source modal renders its fields from `GET /source-types`, so a new source type in Go needs
  no frontend change. PostgreSQL defaults to 5432 and schema `public`; MySQL to 3306; SQLite
  shows a path field with helper text that the file must be seeded into the volume first.
- Test-connection must succeed before Add enables. Show table count on success, `message` plus
  `hint` on failure.
- Remove requires typed confirmation.
- Passwords `type="password"`, never persisted client-side, never logged. CSRF token via
  `authentication.getCSRFToken()` on every mutation.
- Users without `can_manage` get a read-only list; the server enforces this independently.
- `@apache-superset/core/components` only.

---

## 12. Superset configuration

`superset/superset_config.py`:

- `SQLALCHEMY_DATABASE_URI` from env, pointing at the external PostgreSQL. Refuse to start if
  unset or SQLite.
- `CACHE_CONFIG` and `DATA_CACHE_CONFIG` using `FileSystemCache` on `superset_home`, with a
  bounded threshold.
- No Celery config. Async query execution off.
- `SQLLAB_TIMEOUT` and `SUPERSET_WEBSERVER_TIMEOUT` set to firm values, documented alongside the
  gunicorn worker count.
- Generous `WEB_CONCURRENCY`, since long queries now hold workers.

`fedctl up` registers the ClickHouse connection idempotently through Superset's REST API:
`clickhousedb://bi_ro:***@clickhouse:8123/default`, with `expose_in_sqllab: true`,
`allow_dml: false`, `allow_ctas: false`, `allow_cvas: false`.

---

## 13. Tests

- Go unit tests for every rule in section 9. The SSRF and SQLite traversal cases matter most.
- Golden-file DDL tests per source type; assert no credential appears in any generated statement
  or log record.
- A test asserting `fedctl` rejects credentials passed as flags.
- `apply` idempotence: twice in a row is a no-op.
- Atomicity: a forced failure mid-attach leaves no orphaned collection or database.
- Python shim: a user without `can_manage` gets 403 before `fedctl` is invoked; the subprocess is
  called with a list and `shell=False`.
- Integration (`-tags integration`): `fedctl up --dev`, add all three source types, run the
  four-way join, assert rows.
- CI compatibility gate: build all five targets, run integration on `linux/amd64` and
  `linux/arm64`. A build that does not cross-compile is a failed build.

---

## 14. Acceptance criteria

- [ ] The running stack is exactly two containers.
- [ ] No Redis, no Celery, no Postgres container anywhere in `compose.yaml`.
- [ ] Superset metadata lives in the external PostgreSQL; startup fails clearly if unset or SQLite.
- [ ] `fedctl preflight` gives actionable errors on a host missing Compose v2 or buildx.
- [ ] `fedctl up` reaches all-healthy from a clean checkout with no shell script executed.
- [ ] The same commands work on Linux, macOS, and Windows hosts.
- [ ] All binaries build with `CGO_ENABLED=0` for all five targets; checksums emitted.
- [ ] Both pinned images resolve on amd64 and arm64.
- [ ] A search for `.sh`, `/bin/sh`, or `/bin/bash` finds nothing outside documentation.
- [ ] No bind mount except read-only config directories.
- [ ] `fedctl apply` twice is a no-op; `fedctl plan` redacts credentials.
- [ ] The four-way live join returns correct rows from SQL Lab.
- [ ] A row changed on a source appears in Superset within seconds.
- [ ] `fedctl seed sqlite` populates the volume with no bind mount.
- [ ] A password passed as a flag is rejected; passwords appear in no log, response, or SQL.
- [ ] SQLite path `../../etc/passwd` rejected; host `169.254.169.254` rejected; source name
      `foo; DROP DATABASE default` rejected.
- [ ] A non-privileged Superset user cannot attach or remove, enforced server-side.
- [ ] The Python backend is under 200 lines and contains no DDL or validation logic.
- [ ] Adding a source type is one Go file and zero frontend changes.

---

## 15. Deliverables

Working stack plus a README covering: architecture in five sentences, the removals from section 2
and their costs stated plainly, the language policy and why Python is unavoidable, quickstart per
host OS, the pinned version table, how to add a source, the join-tuning knobs from section 8,
backup and restore of `ch-data`, a named **owner** for the ClickHouse container, and a
known-limitations section covering the pre-1.0 extensions API and the absence of Alerts and
Reports.

Work section by section. After each, run what exists and report status before continuing.
Sections 3 and 4 constrain every later section — if a later choice would introduce a shell
script, a CGO dependency, a bind mount, or logic in Python, stop and report rather than accepting
it.
