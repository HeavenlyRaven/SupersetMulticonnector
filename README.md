# Superset + ClickHouse federation hub

Query PostgreSQL, MySQL, and SQLite together from Superset's SQL Lab.
ClickHouse does the live cross-database joins; a two-container stack does
the hosting — no ETL, no replication, no separate controller service.

New to the codebase? [`docs/`](docs/README.md) is a guided tour that
explains every file in the repository, and the Docker, SQL, Superset, and Go
concepts behind them, from the ground up.

## Architecture

Two containers: `superset` (Apache Superset, with a source-management panel
added via a Superset extension) and `clickhouse` (the federation hub).
ClickHouse attaches PostgreSQL, MySQL, and SQLite databases directly
through its own engines and joins across them live — queries always hit
the real source, nothing is replicated or materialized. SQLite is the one
exception by necessity (it's a local file, not a network service): its
database file is uploaded into a shared volume both containers can read.

`fedctl`, a single Go binary baked into both images, does the actual work.
It's the CLI you can run yourself (`fedctl up`, `fedctl source add`, ...),
and it's also what the extension's Superset-side panel execs on every
request — so there's exactly one implementation of validation, DDL
generation, and ClickHouse access. Every new source host is checked
against a private/loopback allowlist before ClickHouse ever dials it, and
credentials are never passed as command-line arguments or written to logs.

### Notes on scope

This stack leaves out Redis and Celery to stay at two containers. Two
practical effects: SQL Lab queries run synchronously (a long-running query
holds a web worker for its duration — size `WEB_CONCURRENCY` and
`SQLLAB_TIMEOUT` in `.env` accordingly), and Alerts & Reports, scheduled
reports, and thumbnails are disabled, since they require Celery. If you
need those, add a Redis container and a Celery worker back in — nothing
else here assumes their absence.

## Quickstart

**On Windows**, there's an installer — download it from the
[releases page](https://github.com/HeavenlyRaven/SupersetMulticonnector/releases)
and follow [docs/INSTALL.md](docs/INSTALL.md), which also covers the two
prerequisites (Docker Desktop, and an external PostgreSQL for Superset's
metadata). The rest of this section is the build-from-source path, which
works on every platform.

Requires Docker with Compose v2 and buildx (Docker Desktop bundles both;
on Linux, install `docker-compose-plugin` and `docker-buildx-plugin`).

1. Build or download `fedctl`:
   ```
   go build -o fedctl ./cmd/fedctl        # macOS/Linux
   go build -o fedctl.exe ./cmd/fedctl    # Windows
   ```

2. From the repo root, run the installer:
   ```
   ./fedctl install
   ```
   This checks Docker, writes `.env` (prompting only for what it can't
   infer — your external PostgreSQL URI, an admin username/password, and
   the port to publish; every internal secret is generated for you), builds
   both images, waits for them to become healthy, and registers the
   ClickHouse connection in Superset. Leave the admin password prompt
   blank to get one generated and printed once — save it.

   For unattended installs, set `SQLALCHEMY_DATABASE_URI` (and optionally
   `SUPERSET_ADMIN_USER`/`SUPERSET_ADMIN_PASSWORD`) in the environment and
   run `fedctl install --yes`. Add `--dev` to also start the bundled
   sample PostgreSQL/MySQL databases, useful for trying things out locally.

3. Open `http://localhost:8088` (or your chosen port) and log in.

4. `./fedctl down` stops the stack (add `--volumes` to also delete stored
   data).

Already have `.env`? `fedctl preflight`, `fedctl up`, and `fedctl down`
work individually — `install` is just a convenience wrapper around them.

**Windows:** `fedctl` is a native binary and needs no WSL itself, but
Docker Desktop's WSL2 backend still runs the containers, same as any other
Docker Desktop workload.

## Adding a source

**From SQL Lab** — open the "Federated sources" panel, click **Add
source**, pick a type, fill in the fields, click **Test connection**, and
once it reports a table count, **Add** enables. **Remove** requires typing
the source's exact name to confirm.

For a SQLite source, upload the file directly from the picker — no
separate step needed. Since SQLite has no live connection to re-read, use
**Replace** to refresh a source's data with a newer copy of the same file.

**From the CLI** — for example, a PostgreSQL source:

```
fedctl source add --name fed_sales --type postgres \
    --host pg.example.internal --port 5432 --user svc --database salesdb
# prompts for the password on stdin — never pass it as a flag
```

Or declaratively, editing `config/sources.yaml` and running:

```
fedctl plan -f config/sources.yaml    # preview the DDL, credentials redacted
fedctl apply -f config/sources.yaml   # idempotent: safe to re-run
```

If a source's host resolves to a private/loopback/link-local address (for
example, a database running on your own machine, reachable via
`host.docker.internal`), add it to `FEDCTL_ALLOWED_SOURCE_HOSTS` in `.env`
first — otherwise `fedctl` rejects it as a possible SSRF target.

## Extending: adding a source type

Supporting a new database engine is one Go file and no frontend changes:

1. Implement `sources.Type` in a new file under `internal/sources/`
   (`postgres.go`, `mysql.go`, `sqlite.go` are the existing examples).
2. Register it with `sources.Register(...)` in that file's `init()`.
3. Add the matching `ddl.AttachX` function in `internal/ddl/ddl.go`, and
   wire it into `cmd/fedctl/sourceops.go`'s `buildAttachPlan` switch.

The Add Source form renders its fields entirely from `GET /source-types`
(`fedctl source types`), generated from your type's `Fields()` — no
TypeScript changes needed for a new field set.

## Releasing

`make version-set VERSION=x.y.z`, commit, tag, push — the tag triggers a
workflow that publishes container images, the Windows installer, `.deb`/
`.rpm`/`.apk` packages, and binaries for all five targets. Full runbook:
[RELEASING.md](RELEASING.md).

## Join-tuning knobs

All live in `clickhouse/users.d/roles.xml`'s `bi_readonly` profile and
`.env`:

- `join_algorithm` — lets ClickHouse pick a memory-appropriate join
  strategy per query.
- `max_bytes_in_join` / `max_memory_usage` (`.env`: `CLICKHOUSE_MAX_BYTES_IN_JOIN` /
  `CLICKHOUSE_MAX_MEMORY_USAGE`) — raise if large federated joins hit
  memory limits; keep in mind the container's own memory cap
  (`CLICKHOUSE_MEMORY_LIMIT`).
- `max_execution_time` — change together with Superset's `SQLLAB_TIMEOUT`.
- `join_use_nulls` — SQL-standard NULL handling for outer joins across
  heterogeneous sources.
- `use_query_cache` / `query_cache_ttl` — short-lived result caching for
  repeated dashboard loads.

## Backup and restore

`ch-data` is a regular named Docker volume — standard volume backup
applies:

```
# Backup
docker run --rm -v ch-data:/data -v "$PWD":/backup alpine \
    tar czf /backup/ch-data-$(date +%Y%m%d).tar.gz -C /data .

# Restore into a fresh volume
docker volume create ch-data
docker run --rm -v ch-data:/data -v "$PWD":/backup alpine \
    tar xzf /backup/ch-data-YYYYMMDD.tar.gz -C /data
```

Named collections and source definitions are cheap to regenerate from
`config/sources.yaml` via `fedctl apply`, so you generally only need to
back up actual table data if you materialize something locally — this
design otherwise keeps everything live.

## Pinned versions

| Component | Version |
|---|---|
| `apache/superset` | `6.1.0` (digest-pinned) |
| `clickhouse/clickhouse-server` | `26.8.1.2041` (26.8 LTS, digest-pinned) |
| `clickhouse-connect[sqlalchemy]` | `1.7.2` |
| `psycopg2-binary` | `2.9.13` |
| `github.com/ClickHouse/clickhouse-go/v2` | `v2.48.0` |
| `github.com/jackc/pgx/v5` | `v5.10.0` |
| `github.com/go-sql-driver/mysql` | `v1.10.0` |
| `modernc.org/sqlite` | `v1.57.0` |
| `github.com/spf13/cobra` | `v1.10.2` |

Exact digests are pinned in the Dockerfiles and `go.mod`/`go.sum`. Image
tags can be republished under the same version string, so re-verify a
pin's digest before trusting it long-term: `docker buildx imagetools
inspect <ref>`, or `fedctl preflight`, which checks this automatically.

## Known limitations

- Superset's extensions framework is still pre-1.0 (marked "Next" /
  unreleased in its own docs as of this writing). The REST path prefix,
  decorator names, and `extension.json` permission semantics may shift in
  a future Superset release — re-check against your Superset version if
  extension endpoints 404 or the panel doesn't load.
- Alerts & Reports, scheduled reports, and thumbnails require Celery,
  which this stack doesn't run (see "Notes on scope" above).
- Superset's Admin role gets the extension's `can_manage` permission
  automatically; Alpha does not, by default. Grant it manually (Settings →
  List Roles → Alpha) if you want Alpha-level users to manage sources.
