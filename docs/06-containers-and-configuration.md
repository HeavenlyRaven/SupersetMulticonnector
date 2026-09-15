# 6. Containers and configuration

This chapter covers everything that shapes the two running containers: the
Dockerfiles that build them, the Compose files that run them, and the
configuration files inside them.

---

## [compose.yaml](../compose.yaml) — the production stack

The whole system, in about 120 lines. Read it top to bottom.

### No `version:` key

The first thing you will notice is what is missing. Older Compose files start
with `version: "3.8"`. The current Compose Specification dropped it — the
format is now versionless, and `docker compose` infers what it needs.

### A pinned project name

```yaml
name: superset-federation
```

Compose derives a project name from the directory you are in, and uses it to
prefix container and volume names. Pinning it means the names stay stable
regardless of what your checkout directory is called.

The comment then makes an important admission:

> fedctl doesn't actually rely on this string being exact anywhere — `fedctl
> seed sqlite` resolves the real ch-user-files volume via Compose's own
> `com.docker.compose.volume` label rather than guessing a prefixed name.

The name is a convenience, not a contract. That distinction was learned from
the bug described in [chapter 4](04-fedctl-cli.md#resolvecomposevolume--a-real-bug-preserved-in-a-comment).

### Networks and volumes

```yaml
networks:
  federation:

volumes:
  ch-data:
  ch-user-files:
  superset-home:
```

One private network, three named volumes. Empty values mean "use the
defaults" — Docker manages them.

### The ClickHouse service

```yaml
clickhouse:
  build:
    context: .
    dockerfile: images/clickhouse/Dockerfile
  image: superset-federation/clickhouse:local
  networks: [federation]
  volumes:
    - ch-data:/var/lib/clickhouse
    - ch-user-files:/var/lib/clickhouse/user_files
    - ./clickhouse/config.d:/etc/clickhouse-server/config.d:ro,z
    - ./clickhouse/users.d:/etc/clickhouse-server/users.d:ro,z
```

Two named volumes for data, and two bind mounts for configuration —
`:ro` read-only, `:z` for SELinux relabelling. These are the *only* bind
mounts allowed anywhere in the project, and only because they are read-only
configuration.

```yaml
  environment:
    CLICKHOUSE_FED_ADMIN_PASSWORD: ${FEDCTL_ADMIN_PASSWORD:?FEDCTL_ADMIN_PASSWORD must be set in .env}
```

That `${VAR:?message}` syntax means **"fail with this message if the variable
is unset"**. Compose refuses to start rather than launching a ClickHouse with
an empty admin password. Notice also that no secret is written in this file —
it only forwards values from `.env`.

```yaml
  ulimits:
    nofile: {soft: 262144, hard: 262144}
```

The maximum number of open file descriptors. ClickHouse keeps a great many
files open and will complain loudly at the default limit.

```yaml
  deploy:
    resources:
      limits:
        memory: ${CLICKHOUSE_MEMORY_LIMIT:-4g}
```

A container memory cap, defaulting to 4 GB (`:-` means "use this if unset",
as opposed to `:?` which fails). This pairs with a ClickHouse setting you
will meet below — without that setting, ClickHouse reads the *host's* total
RAM and plans as if it owned the machine.

```yaml
  healthcheck:
    test: ["CMD", "/usr/local/bin/fedctl", "check", "clickhouse"]
    interval: 15s
    timeout: 5s
    retries: 5
    start_period: 30s
```

`start_period` is a grace window: failures during the first 30 seconds do not
count against `retries`, because a starting server is expected to be
unavailable briefly.

### The Superset service

```yaml
  depends_on:
    clickhouse:
      condition: service_healthy
  ports:
    - "${SUPERSET_PORT:-8088}:8088"
```

Superset waits for ClickHouse to be *healthy*, not merely started — the
distinction matters, since a started ClickHouse may still be loading. And
this is the only published port in the stack.

```yaml
  group_add:
    - "101"
```

This one needs explanation, and the file gives a good one. The
`ch-user-files` volume is owned by user and group 101 inside the ClickHouse
image (`clickhouse:clickhouse`). The Superset container runs as a completely
different user. When the extension uploads a SQLite file, it writes into that
shared volume — and would get "permission denied", even though the mount is
read-write.

`group_add: ["101"]` adds group 101 as a *supplementary group* to the
Superset container's user, so the group-write bit on the directory applies.
The matching half is in
[images/clickhouse/Dockerfile](../images/clickhouse/Dockerfile), which
pre-creates the directory as `0775` owned by `101:101`.

This is the classic Linux file-permission problem, solved properly: not by
running as root, and not by `chmod 777`.

### Why `ch-user-files` is mounted into both containers

The longest comment in the file explains this, and it is worth following.

The Python shim runs `fedctl` **inside the Superset container**. So when
someone adds a SQLite source, the path validation
(`validate.SQLitePath`) runs in *that* container. If `ch-user-files` were not
mounted there, validation would always fail with "cannot resolve user_files
directory" — the directory would not exist on that side.

The comment also answers the obvious objection:

> A named volume mounted into two services is a normal, encouraged Docker
> pattern — unlike a bind mount, it isn't what spec section 4's "no bind
> mounts" rule is about.

And why read-write rather than read-only: the browser upload path writes
directly here, because inside that container there is no Docker socket to
launch a helper container with.

---

## [compose.dev.yaml](../compose.dev.yaml) — sample databases

Two extra services under `profiles: ["dev"]`, so they never start unless
`--profile dev` is passed (which `fedctl up --dev` does).

```yaml
dev-postgres:
  image: postgres:16-alpine@sha256:cf78e766...
  profiles: ["dev"]
  environment:
    POSTGRES_USER: sample
    POSTGRES_PASSWORD: sample
    POSTGRES_DB: sampledb
  volumes:
    - ./dev/postgres-init:/docker-entrypoint-initdb.d:ro,z
```

Both official images run any `.sql` file found in
`/docker-entrypoint-initdb.d` the first time they initialise a fresh data
directory. That is how [dev/postgres-init/001-sample.sql](../dev/postgres-init/001-sample.sql)
and [dev/mysql-init/001-sample.sql](../dev/mysql-init/001-sample.sql) get
loaded — a standard mechanism, using `.sql` files rather than shell scripts.

The credentials being `sample`/`sample` is fine: these containers are not
reachable from outside the Docker network and exist only for local testing.

The header comment flags a gotcha you will otherwise hit within five minutes:

> Their hostnames resolve to addresses in the docker-network's private range,
> which validate.Host denies by default — add them to
> FEDCTL_ALLOWED_SOURCE_HOSTS in .env to attach them as sources locally.

Which is why [.env.example](../.env.example) ships with
`FEDCTL_ALLOWED_SOURCE_HOSTS=dev-postgres,dev-mysql`. Your own SSRF
protection blocks your own test databases — working as designed.

---

## [images/superset/Dockerfile](../images/superset/Dockerfile)

### Stage 1: build the Go binary

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm@sha256:3b4a1151... AS fedctl-build
ARG TARGETOS
ARG TARGETARCH
...
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/fedctl ./cmd/fedctl
```

Several things at once:

- **`--platform=$BUILDPLATFORM`** pins this stage to the *builder's* native
  architecture. Without it, building an ARM image on an x86 machine would run
  the entire Go compiler under emulation — minutes instead of seconds.
- **`$TARGETOS` / `$TARGETARCH`** are supplied by buildx and passed to Go's
  own cross-compilation variables. So the compiler runs natively while
  producing a binary for the target. Go cross-compiles trivially; this
  exploits that.
- **`--mount=type=cache`** keeps Go's build cache between builds without
  storing it in the image.
- **`CGO_ENABLED=0`** produces a fully static binary — see
  [chapter 2](02-background-concepts.md).
- **`-trimpath`** removes local filesystem paths from the binary, making
  builds reproducible.

### Stage 2: derive from Superset

```dockerfile
FROM apache/superset:6.1.0@sha256:59cd4af6...
USER root
RUN uv pip install --python /app/.venv/bin/python \
    "clickhouse-connect[sqlalchemy]==1.7.2" \
    "psycopg2-binary==2.9.13"
```

Superset's official images ship almost no database drivers, on purpose —
deriving an image and adding the ones you need is the documented production
path.

Two drivers are needed:

- `clickhouse-connect[sqlalchemy]` — so Superset can query the hub.
- `psycopg2-binary` — so Superset can reach **its own metadata database**.
  The comment records that this was discovered live: without it Superset dies
  at startup with `ModuleNotFoundError: No module named 'psycopg2'`, before
  ClickHouse is ever involved.

And a genuinely surprising discovery about *how* to install them:

> Superset runs from a dedicated venv at `/app/.venv` whose `bin/python` is a
> symlink back to the system interpreter, so plain `pip --python
> /app/.venv/bin/python` resolves that symlink and silently installs into
> system site-packages instead — same failure as installing nothing. `uv pip
> install --python /app/.venv/bin/python` correctly recognizes and targets
> the venv.

A command that reports success and installs into the wrong place is the worst
kind of bug. This one is now documented in the file where it happened.

```dockerfile
COPY --from=fedctl-build /out/fedctl /usr/local/bin/fedctl
COPY superset/superset_config.py /app/pythonpath/superset_config.py
RUN mkdir -p /app/extensions
ENV EXTENSIONS_PATH=/app/extensions
USER superset
HEALTHCHECK ... CMD ["/usr/local/bin/fedctl", "check", "superset"]
```

The binary from stage 1 is copied in — that is how `fedctl` comes to exist
inside the container. `USER superset` drops back from root before the image
finishes, so the container does not run privileged.

---

## [images/clickhouse/Dockerfile](../images/clickhouse/Dockerfile)

The same Go build stage, then:

```dockerfile
FROM clickhouse/clickhouse-server:26.8.1.2041@sha256:9f8fa0d5...
COPY --from=fedctl-build /out/fedctl /usr/local/bin/fedctl
RUN chmod 0755 /usr/local/bin/fedctl
RUN mkdir -p /var/lib/clickhouse/user_files \
    && chown 101:101 /var/lib/clickhouse/user_files \
    && chmod 0775 /var/lib/clickhouse/user_files
```

Base image plus `fedctl` plus one carefully-permissioned directory. Nothing
else.

Why pre-create `user_files` when ClickHouse would create it anyway? Because
of a specific Docker behaviour: when an **empty named volume** is mounted at
a path that already has content in the image, Docker seeds the volume with
that content — *including its ownership and permissions*. Creating it here as
`0775` owned by `101:101` means every fresh deployment gets a group-writable
directory from the start. If ClickHouse created it at runtime, it would be
`0755` and the Superset container could not write uploads into it.

This and `group_add: ["101"]` in compose.yaml are two halves of one solution.
Neither works alone.

---

## [clickhouse/config.d/federation.xml](../clickhouse/config.d/federation.xml)

ClickHouse reads its main `config.xml`, then merges every file in
`config.d/`. That is why this file only contains overrides.

### Listening on all interfaces

```xml
<listen_host>0.0.0.0</listen_host>
```

The comment explains both halves of this decision:

> without this, the server binds its HTTP/native ports to 127.0.0.1 only —
> reachable from nothing else on the docker network, including Superset's own
> connection and this container's own HEALTHCHECK.

Is listening on all interfaces a security problem? Not here, and the comment
says why: the port is never published to the host, and the network is
internal-only. **The container boundary is the access boundary.** Adding
`127.0.0.1` on top would only break things.

And a second discovery:

> IPv4 only, deliberately: a `::` (IPv6-any) listen_host fails hard on a
> docker bridge network without IPv6 enabled ("Address family for hostname
> not supported") — which is the common case.

### Honouring the container's memory limit

```xml
<max_server_memory_usage_to_ram_ratio>0.8</max_server_memory_usage_to_ram_ratio>
```

Tells ClickHouse to use at most 80% of the memory *it is allowed*, rather
than 80% of the host's RAM. Without it, a ClickHouse capped at 4 GB on a
64 GB machine would plan queries as though it had 51 GB, then get killed by
the kernel's OOM killer.

### Bounded query log

```xml
<query_log>
    <ttl>event_date + INTERVAL 14 DAY DELETE</ttl>
</query_log>
```

ClickHouse logs every query it runs into a table inside itself. Without a
retention rule, that table grows forever and eventually fills `ch-data`.

The comment records a failed first attempt: specifying a custom `<engine>`
clause conflicted with the `<partition_by>` inherited from the base config
(XML configs merge per-element, so the inherited setting survived even though
this file never mentions it) and ClickHouse refused to start. Using the
dedicated `<ttl>` key avoids the conflict entirely.

### Query cache limits

```xml
<query_cache>
    <max_size_in_bytes>1073741824</max_size_in_bytes>
    <max_entries>1024</max_entries>
    <max_entry_size_in_bytes>1048576</max_entry_size_in_bytes>
</query_cache>
```

Server-wide bounds. Whether a query *uses* the cache is decided per-user, in
the next file.

---

## [clickhouse/users.d/roles.xml](../clickhouse/users.d/roles.xml)

The security model of the whole hub, in one file. Every line of it was
verified against a running ClickHouse, and the comments record what failed
before it worked.

### Removing the default user

```xml
<default remove="remove"></default>
```

ClickHouse ships with a user called `default` that has **no password and no
network restriction**. It is deleted here.

That interacts with the official image's entrypoint script, which tries to
write its own `users.d/default-user.xml` when it cannot tell the default user
has been handled — and fails hard, because `users.d` is mounted read-only.
Hence `CLICKHOUSE_SKIP_USER_SETUP: "1"` in
[compose.yaml](../compose.yaml). Two settings in two files that only work
together, with a comment in each pointing at the other.

### `fed_admin` — the DDL role

```xml
<fed_admin>
    <password from_env="CLICKHOUSE_FED_ADMIN_PASSWORD" />
    <networks><ip>::/0</ip></networks>
    <profile>default</profile>
    <grants>
        <query>GRANT CREATE DATABASE, DROP DATABASE ON *.*</query>
        <query>GRANT SELECT ON system.*</query>
        <query>GRANT NAMED COLLECTION CONTROL ON *</query>
        <query>GRANT TABLE ENGINE ON PostgreSQL</query>
        <query>GRANT TABLE ENGINE ON MySQL</query>
        <query>GRANT TABLE ENGINE ON SQLite</query>
        <query>GRANT SHOW TABLES ON *.*</query>
    </grants>
</fed_admin>
```

`from_env` reads the password from an environment variable, so no plaintext
secret is in this file.

Now look at what is granted, and — more importantly — what is not. **There is
no `GRANT SELECT` on federated data.** `fed_admin` can create a database that
reads your sales system, and cannot read a single row from it. That is least
privilege applied seriously: the account that automation uses is the account
with no access to data.

Three of these grants were added only after live failures, and the comments
say so:

- **`TABLE ENGINE ON PostgreSQL`** etc. — without them, every
  `CREATE DATABASE ... ENGINE = PostgreSQL(...)` fails with "Not enough
  privileges... necessary to have the grant TABLE ENGINE ON PostgreSQL". This
  is a separate privilege class governing which storage engines a user may
  instantiate at all, independent of `CREATE DATABASE`.
- **`SHOW TABLES ON *.*`** — `GRANT SELECT ON system.*` is *not* enough to
  make `system.tables` list a federated database's tables, because ClickHouse
  filters `system.tables` row by row according to your real access to each
  listed table. `SHOW TABLES` is the correct narrower privilege for "list
  what exists" without granting the ability to read rows.
- The comment also records that `<access_management>` cannot be combined with
  `<grants>` — and that `fed_admin` does not need it anyway, since it never
  creates users or roles.

### `bi_ro` — the query role

```xml
<bi_ro>
    <password from_env="CLICKHOUSE_BI_RO_PASSWORD" />
    <profile>bi_readonly</profile>
</bi_ro>
```

This is the account Superset connects as. Its behaviour comes from the
profile:

```xml
<bi_readonly>
    <readonly>2</readonly>
    <join_algorithm>grace_hash,parallel_hash,hash</join_algorithm>
    <max_bytes_in_join from_env="CLICKHOUSE_MAX_BYTES_IN_JOIN" />
    <max_memory_usage from_env="CLICKHOUSE_MAX_MEMORY_USAGE" />
    <max_execution_time>300</max_execution_time>
    <join_use_nulls>1</join_use_nulls>
    <use_query_cache>1</use_query_cache>
    <query_cache_ttl>60</query_cache_ttl>
    ...
</bi_readonly>
```

Each setting, in plain terms:

| Setting | What it does |
|---|---|
| `readonly = 2` | Cannot modify data, but may adjust settings within a query. Superset needs the latter. |
| `join_algorithm` | Offers ClickHouse three strategies; it picks per query. `grace_hash` spills to disk instead of failing when a join is too big for memory — important for federated joins whose size cannot be predicted. |
| `max_bytes_in_join` | Cap on join memory. Raise it for big joins; keep it under the container's limit. |
| `max_memory_usage` | Cap on total query memory. |
| `max_execution_time = 300` | Kill a query after 5 minutes. Should be changed together with Superset's `SQLLAB_TIMEOUT`. |
| `join_use_nulls = 1` | SQL-standard NULL behaviour for outer joins. ClickHouse's default fills unmatched rows with zero values instead of NULL, which surprises everyone. |
| `use_query_cache`, `query_cache_ttl = 60` | Cache results for 60 seconds — good for a dashboard whose ten charts all hit the same data. |

The last two settings in the profile are pure field experience:

```xml
<query_cache_nondeterministic_function_handling>ignore</query_cache_nondeterministic_function_handling>
<query_cache_system_table_handling>ignore</query_cache_system_table_handling>
```

ClickHouse's query cache refuses, by default, to run any query that calls a
non-deterministic function (`now()`, `version()`) or reads a system table.
Superset's connection test does both. So without these two lines, **adding
the ClickHouse connection in Superset fails outright** before a single
dashboard query runs. `ignore` means "run it normally, just do not cache it"
— as opposed to `save`, which would cache a `now()` result that is stale one
second later.

---

## [superset/superset_config.py](../superset/superset_config.py)

Superset imports this Python file at startup. Because it is real Python, it
can validate and refuse to start.

### Refusing to start

```python
def _require_env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        print(f"superset_config: {name} is not set. ...", file=sys.stderr)
        sys.exit(1)
    return value

SQLALCHEMY_DATABASE_URI = _require_env("SQLALCHEMY_DATABASE_URI")
if SQLALCHEMY_DATABASE_URI.lower().startswith("sqlite"):
    print("superset_config: SQLALCHEMY_DATABASE_URI points at SQLite. ...", file=sys.stderr)
    sys.exit(1)
```

`sys.exit(1)` inside a config file is unusual and deliberate. Superset's
default is to silently fall back to a SQLite file inside the container — and
that file dies with the container, taking every dashboard, user, and saved
query with it. **Failing to start is better than starting wrongly.**

Note this is the third place the same rule is enforced: `fedctl up` checks it
before starting containers, `fedctl preflight` checks it and also tests
connectivity, and this file checks it inside the container. Three layers,
because the failure mode is data loss.

### Cache without Redis

```python
CACHE_CONFIG = {
    "CACHE_TYPE": "FileSystemCache",
    "CACHE_DIR": os.path.join(_CACHE_DIR, "superset"),
    "CACHE_DEFAULT_TIMEOUT": 300,
    "CACHE_THRESHOLD": 5000,
}
```

Files on the `superset-home` volume instead of Redis. `CACHE_THRESHOLD` caps
the number of cached items so the volume cannot fill.

### Feature flags

```python
FEATURE_FLAGS = {
    "ALERT_REPORTS": False,
    "THUMBNAILS": False,
    "ENABLE_EXTENSIONS": True,
}
```

The first two need Celery, which is not here, so they are switched off
explicitly rather than left to fail confusingly. The third is what makes the
extension load at all.

### Timeouts

```python
SQLLAB_TIMEOUT = int(os.environ.get("SQLLAB_TIMEOUT", "300"))
SUPERSET_WEBSERVER_TIMEOUT = int(os.environ.get("SUPERSET_WEBSERVER_TIMEOUT", "300"))
WEB_CONCURRENCY = int(os.environ.get("WEB_CONCURRENCY", "8"))
```

These three must be read together, which is why the comment keeps them
together even though `WEB_CONCURRENCY` is actually consumed by the base
image's gunicorn launcher rather than by Superset's Python config.

The logic: no Celery means a running query holds a gunicorn worker. Eight
workers means eight simultaneous long queries make the UI unresponsive for
everyone. So you want a generous worker count *and* a firm timeout. Raising
the timeout without raising the worker count makes the problem worse.

---

## [.env.example](../.env.example) — the configuration reference

Every configuration key, grouped and commented. `fedctl install` turns it
into your `.env` by rewriting the values and keeping the comments, so this
file is both the template and the documentation.

| Group | Keys | Notes |
|---|---|---|
| Metadata database | `SQLALCHEMY_DATABASE_URI` | The one value you *must* supply yourself. |
| Flask | `SUPERSET_SECRET_KEY` | Signs sessions and CSRF tokens. Auto-generated. |
| Superset admin | `SUPERSET_ADMIN_USER`, `SUPERSET_ADMIN_PASSWORD` | Used once to bootstrap the admin user. |
| ClickHouse roles | `FEDCTL_ADMIN_USER/PASSWORD`, `SUPERSET_CH_USER/PASSWORD` | Both passwords auto-generated; you never type them. |
| Join tuning | `CLICKHOUSE_MAX_BYTES_IN_JOIN`, `CLICKHOUSE_MAX_MEMORY_USAGE` | Feed the `bi_readonly` profile. |
| Resource limits | `CLICKHOUSE_MEMORY_LIMIT`, `SUPERSET_MEMORY_LIMIT` | Container caps. |
| Timeouts | `SQLLAB_TIMEOUT`, `SUPERSET_WEBSERVER_TIMEOUT`, `WEB_CONCURRENCY` | Read together, per above. |
| Networking | `SUPERSET_PORT` | The only published port. |
| Security | `FEDCTL_ALLOWED_SOURCE_HOSTS` | The SSRF allowlist. |
| Dev | `LOCAL_EXTENSIONS` | Points at extension source for development. Leave empty in production. |

---

## [config/sources.yaml](../config/sources.yaml)

The declarative alternative to clicking buttons:

```yaml
sources:
  - name: fed_sales
    type: postgres
    host: pg.example.internal
    port: 5432
    user: svc_readonly
    password_env: FED_SALES_PASSWORD
    database: salesdb
    schema: public
```

`password_env` names an environment variable rather than holding a password,
which is what makes this file safe to commit. Run `fedctl plan -f
config/sources.yaml` to preview the SQL and `fedctl apply` to make it so;
`apply` is idempotent.

(As noted in [chapter 5](05-internal-packages.md#naming), the `fed_` in these
example names is redundant — it produces `fed_fed_sales` as the ClickHouse
database. Harmless, but prefer plain names in your own file.)

---

## [Makefile](../Makefile)

```make
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o fedctl ./cmd/fedctl

test:
	go test ./...

fmt:
	gofmt -l .

vet:
	go vet ./...

cross-build:
	... five go build invocations ...
	cd dist && sha256sum fedctl-* > checksums.txt
```

Points worth noting:

- `go test ./...` — the `./...` pattern means "this directory and everything
  below it". The integration test is excluded automatically by its build tag.
- `gofmt -l .` **lists** files that are not correctly formatted rather than
  fixing them, which is what you want in CI.
- `go vet` catches suspicious-but-legal code, like a `Printf` whose arguments
  do not match its format string.
- `cross-build` produces all five targets — Linux, macOS, and Windows on both
  CPU architectures — from one machine, then writes SHA-256 checksums so
  downloaders can verify what they got. This only works because of
  `CGO_ENABLED=0`.
- The comment notes that every recipe invokes only `go`, `docker`, or
  `sha256sum` directly, honouring the no-shell-scripts rule even here.

`LDFLAGS` is where `version`, `commit`, and `date` get injected — see
[version.go](../cmd/fedctl/version.go) in chapter 4.

---

## The dotfiles

**[.dockerignore](../.dockerignore)** — excludes files from the build
context. Two entries matter for correctness:

```
.env
.env.*
!.env.example
```

Your real `.env` never enters a Docker build context, so it cannot end up
baked into an image layer. The `!` re-includes the example file. `dist/` and
`*.exe` are excluded to keep the context small.

**[.gitignore](../.gitignore)** — keeps secrets and build output out of git:
`.env`, `dist/`, the built binaries, `*.supx`, `node_modules/`,
`__pycache__/`.

**[.gitattributes](../.gitattributes)** — one line:

```
* text=auto eol=lf
```

Force Unix line endings on checkout. On Windows, git otherwise converts files
to CRLF — and a config file with CRLF line endings can break a Linux program
reading it inside a container. One line, prevents a genuinely baffling class
of bug.

---

Next: [The Superset extension](07-the-superset-extension.md).
