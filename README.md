# Superset + ClickHouse federation hub

## Architecture

Two containers, nothing else: `superset` (Apache Superset with the
`clickhouse-connect` driver and this repo's `ch-federation` extension) and
`clickhouse` (the federation hub, plus the `fedctl` binary baked into both
images). ClickHouse attaches live `PostgreSQL`, `MySQL`, and `SQLite`
database engines as needed and joins across them directly — there is no
replication, no `MaterializedPostgreSQL`/`MaterializedMySQL`, and no
separate metadata store beyond the external PostgreSQL Superset already
needs. `fedctl`, a single static Go binary, is both the operator CLI
(`fedctl up`, `fedctl source add`, ...) and the backend the extension's
Python shim execs in `--json` mode, so all DDL generation, validation, and
ClickHouse access live in exactly one implementation. Every mutating
operation is validated and SSRF-checked before ClickHouse ever touches a
new host, and every credential travels on stdin or as a ClickHouse bind
parameter — never as a CLI argument, never inline in logged SQL. Superset's
own extensions framework (pre-1.0) loads a thin Python/React extension
that gives SQL Lab a source-management panel backed entirely by `fedctl`.

## What was removed, and what it costs

These are accepted trade-offs made explicitly to get to a two-container
stack — not oversights.

**Redis is gone.** Its three jobs disappear or move:

- *Cache* — `FileSystemCache` on the `superset-home` volume
  (`superset/superset_config.py`), plus ClickHouse's own `use_query_cache`
  with a short TTL (`clickhouse/users.d/roles.xml`'s `bi_readonly`
  profile) for repeated dashboard loads.
- *Async query results backend* — removed. SQL Lab runs synchronously.
- *Celery broker* — removed entirely.

**The cost, stated plainly:** with no async backend, a long-running SQL
Lab query holds a gunicorn worker for its entire duration. Enough
concurrent long queries will make the UI unresponsive for everyone. This
is mitigated, not eliminated, by sizing `WEB_CONCURRENCY` generously and
setting a firm `SQLLAB_TIMEOUT` (both in `.env.example`) — there is no way
around the fundamental trade-off without bringing Celery back.

**Alerts & Reports, scheduled reports, and thumbnails do not work without
Celery**, and are explicitly disabled (`FEATURE_FLAGS` in
`superset_config.py`) rather than left silently broken. If any of these
become a real requirement, the known re-expansion path is: add a Redis
container, a Celery worker container, and the corresponding
`CELERY_CONFIG`/`RESULTS_BACKEND` — that turns this back into a
three-or-four container stack.

**The separate Go controller service is gone.** The Python extension shim
`exec`s the `fedctl` binary that ships inside the Superset image, instead
of talking to a separate controller container over the network. This
removes both a container and the shared-token authentication surface a
network-facing controller would have needed.

**Nothing else was removed.** The Go/no-shell/compatibility rules below
are unchanged and binding on every later design choice.

## Language policy, and why Python is unavoidable

| Layer | Language | Why |
|---|---|---|
| CLI, provisioning, DDL generation, validation, health checks, seeding | **Go** | one static, cross-platform, CGO-free binary; safe to run as both an operator CLI and inside a container HEALTHCHECK |
| Superset extension backend | **Python**, thin shim, ~150–200 lines | Superset's extension system loads `backend/src/{publisher}/{name}/entrypoint.py` via `apache-superset-core` — this loader is Python, full stop, so *some* Python is unavoidable if the extension system is used at all |
| Superset extension frontend | **TypeScript/React** | loaded via Webpack Module Federation as `frontend/src/index.tsx`; same reasoning as above, for the frontend loader |

The Python shim (`extension/backend/src/acme/ch_federation/entrypoint.py`)
deliberately contains **no DDL, no validation, no ClickHouse client**. Each
handler does exactly five things: `@protect()`, an explicit
`can_manage`/`can_read` check, `exec fedctl <cmd> --json` with an argv list
and the request body on stdin, parse the JSON response, write an audit log
line. If a future change adds logic beyond that, it belongs in Go instead.

**No shell scripts anywhere.** No `.sh`, `.bat`, or `entrypoint.sh` in this
repo; container entrypoints are the base images' own official entrypoints
(gunicorn, clickhouse-server), never a wrapper script we wrote. The one
place fedctl invokes another process by name is `docker`/`docker compose`
itself (preflight checks, `up`/`down`, `seed sqlite`'s throwaway
container) — always via an argv array, never `sh -c "..."`.

## Quickstart

All commands are identical across hosts because `fedctl` is a single
static binary — the only per-OS difference is how you get it onto `PATH`.
There is no separate installer script: `fedctl install` **is** the
installer, and it's the same binary you use afterward to operate the
stack. Distributing this repo (as a git checkout, or a release archive
containing the platform binaries from `make cross-build` /
`dist/checksums.txt`) is distributing the installer.

1. Install Docker with Compose v2 and buildx (Docker Desktop on
   macOS/Windows bundles both; on Linux install `docker-compose-plugin`
   and `docker-buildx-plugin`). `fedctl install` checks for this and
   tells you exactly what's missing if it isn't there yet — you don't
   need to get this right before starting.
2. Get `fedctl` onto your machine: build it from a checkout, or use a
   released binary for your platform (`linux/amd64`, `linux/arm64`,
   `darwin/amd64`, `darwin/arm64`, `windows/amd64`):
   ```
   go build -o fedctl ./cmd/fedctl        # macOS/Linux
   go build -o fedctl.exe ./cmd/fedctl    # Windows (PowerShell/cmd)
   ```
3. From inside the checkout, run the installer:
   ```
   ./fedctl install
   ```
   This walks you through everything else in one pass: checks Docker,
   writes `.env` (prompting only for what it can't know on its own — the
   external PostgreSQL URI, the Superset admin username/password, the
   port to publish; every internal secret — ClickHouse role passwords,
   Superset's Flask secret key — is generated for you and never needs to
   be typed), runs the same checks as `fedctl preflight`, and then the
   same thing `fedctl up` does: builds both images, waits for both
   containers to become healthy, runs Superset's one-time bootstrap, and
   registers the ClickHouse connection. It prints the generated admin
   password once if you left that prompt blank — save it.

   For unattended installs (CI, provisioning scripts), export
   `SQLALCHEMY_DATABASE_URI` (and `SUPERSET_ADMIN_USER`/
   `SUPERSET_ADMIN_PASSWORD` if you want to choose them instead of a
   generated admin password) and run `fedctl install --yes` — every
   prompt is skipped, and no credential is ever accepted as a flag (see
   the SSRF/credential rules below); `install --yes` only ever reads
   secrets from the environment, the same as everything else in fedctl.
   `fedctl install --dev` also starts `compose.dev.yaml`'s sample
   PostgreSQL/MySQL databases; `--skip-up` stops after `.env` and
   preflight without starting anything; re-running `install` is safe — an
   existing `.env` is left alone unless you pass `--force`.
4. Open `http://localhost:8088` (or whatever port you chose) and log in.
5. `./fedctl down` (add `--volumes` to also delete `ch-data`,
   `ch-user-files`, and `superset-home`).

Already have `.env` set up, or prefer to drive each step yourself?
`fedctl preflight`, `fedctl up`, and `fedctl down` remain available
individually — `install` is a convenience wrapper around exactly those,
not a separate code path.

Windows note: `fedctl` itself needs no WSL — it is a native Windows
binary — but Docker Desktop's WSL2 backend is still what actually runs
the containers, same as any other Docker Desktop workload.

## Pinned versions

| Component | Version | Notes |
|---|---|---|
| `apache/superset` | `6.1.0` @ `sha256:59cd4af66006fe4cc98906eda42a771dbefdacb432f9ab083e02cdc6ff01f29d` | verified `linux/amd64` + `linux/arm64` on 2026-08-31 |
| `clickhouse/clickhouse-server` | `26.8.1.2041` (26.8 LTS) @ `sha256:9f8fa0d51f86611fda226c4085cdafdddce35312a60c934de2e4450d61fcb99a` | verified `linux/amd64` + `linux/arm64` on 2026-08-31 |
| `golang` (build stage only) | `1.23-bookworm` @ `sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db` | never shipped in the runtime images |
| `clickhouse-connect[sqlalchemy]` | `1.7.2` | Superset's ClickHouse SQLAlchemy dialect (`clickhousedb://`) |
| `github.com/ClickHouse/clickhouse-go/v2` | `v2.48.0` | pure Go, `CGO_ENABLED=0` |
| `github.com/jackc/pgx/v5` | `v5.10.0` | pure Go |
| `github.com/go-sql-driver/mysql` | `v1.10.0` | pure Go |
| `modernc.org/sqlite` | `v1.57.0` | pure Go port of SQLite — **not** `mattn/go-sqlite3`, which needs CGO |
| `github.com/spf13/cobra` | `v1.10.2` | CLI framework |

Image tags can be rebuilt under the same version string (observed for the
`6.1.0` tag during this build — same tag, `last_pushed` days after the
GitHub release). Re-verify with
`docker buildx imagetools inspect <ref>` (or `fedctl preflight`, which
does this automatically) before trusting an old pin long-term; digests
are the only truly immutable identifier.

## Adding a source (operator walkthrough)

Two ways to attach a federated source, both idempotent:

**From SQL Lab** — open the "Federated sources" panel (registered via the
`ch-federation` extension), click **Add source**, pick a type, fill in the
fields (PostgreSQL/MySQL defaults to their standard ports and, for
PostgreSQL, schema `public`), click **Test connection**, and once it
reports a table count, **Add** enables. **Remove** requires typing the
source's exact name to confirm.

**From the CLI** — for a PostgreSQL source, for example:

```
fedctl source add --name fed_sales --type postgres \
    --host pg.example.internal --port 5432 --user svc --database salesdb
# prompts for the password on stdin — never pass it as a flag
```

Or declaratively, editing `config/sources.yaml` (a real example is already
checked in) and running:

```
fedctl plan -f config/sources.yaml    # preview the DDL, credentials redacted
fedctl apply -f config/sources.yaml   # idempotent: safe to re-run
```

A SQLite source must be seeded into the `ch-user-files` volume first:
`fedctl seed sqlite --from ./app.sqlite --as app.sqlite`, then reference
just the filename (`app.sqlite`) as its path — not a host path.

If the source's host resolves to a private/loopback/link-local address
(e.g. the `compose.dev.yaml` sample databases, which live on the internal
docker network), add it to `FEDCTL_ALLOWED_SOURCE_HOSTS` in `.env` first —
otherwise `fedctl` rejects it as a possible SSRF target.

## Adding a source type (developer walkthrough)

Adding a new federated source engine is one Go file and zero frontend
changes:

1. Implement `sources.Type` in a new file under `internal/sources/`
   (`postgres.go`, `mysql.go`, and `sqlite.go` are the existing examples —
   `Name()`, `Engine()`, `HasCredentials()`, `Fields()`, `Validate()`).
2. Register it with `sources.Register(...)` in that file's `init()`.
3. Add the matching `ddl.AttachX`/DDL-generation function in
   `internal/ddl/ddl.go`, and wire it into
   `cmd/fedctl/sourceops.go`'s `buildAttachPlan` switch.

The admin panel's Add Source form renders its fields entirely from
`GET /source-types` (`fedctl source types`), which is generated from
`Fields()` — no TypeScript changes are needed for a new field set.

## Join-tuning knobs

All live in `clickhouse/users.d/roles.xml`'s `bi_readonly` profile
(Superset's `bi_ro` connection) and `.env`:

- `join_algorithm = grace_hash,parallel_hash,hash` — lets ClickHouse pick
  a memory-appropriate join strategy per query instead of always using
  the default.
- `max_bytes_in_join` / `max_memory_usage` — from `CLICKHOUSE_MAX_BYTES_IN_JOIN`
  / `CLICKHOUSE_MAX_MEMORY_USAGE` in `.env`; raise these if large
  federated joins hit memory limits, mind the container's own memory cap
  (`CLICKHOUSE_MEMORY_LIMIT`).
- `max_execution_time = 300` — paired with Superset's `SQLLAB_TIMEOUT`;
  change both together.
- `join_use_nulls = 1` — SQL-standard NULL handling for outer joins across
  heterogeneous sources.
- `use_query_cache = 1`, `query_cache_ttl = 60` — short-TTL caching for
  repeated dashboard loads, since there's no Redis-backed cache layer
  anymore.

## Backup and restore of `ch-data`

There is no built-in backup command; `ch-data` is a regular named Docker
volume, so standard volume backup applies:

```
# Backup (stack can stay up; ClickHouse handles concurrent reads fine
# for a snapshot, though pausing writes gives a more consistent copy):
docker run --rm -v ch-data:/data -v "$PWD":/backup alpine \
    tar czf /backup/ch-data-$(date +%Y%m%d).tar.gz -C /data .

# Restore into a fresh volume:
docker volume create ch-data
docker run --rm -v ch-data:/data -v "$PWD":/backup alpine \
    tar xzf /backup/ch-data-YYYYMMDD.tar.gz -C /data
```

Named collections and source database DDL are cheap to regenerate from
`config/sources.yaml` via `fedctl apply` — you generally do not need to
back up ClickHouse's metadata for *that*, only the actual table data if
you materialize anything locally (which this design otherwise avoids —
everything is live).

## Ownership

**ClickHouse container owner:** _Data Platform / Infra on-call —
replace this with your team's actual name before shipping._ This
placeholder exists because the spec this repo implements didn't name a
real owner; don't leave it unfilled in production.

## Known limitations

- **The Superset extensions framework is pre-1.0** (its own docs are
  labeled "Next"/unreleased as of this build). Decorator names, the
  `views.registerView`/`commands.registerCommand` API, the
  `/extensions/{publisher}/{name}/` REST path prefix, and the exact
  `extension.json` `permissions` field semantics were the best
  interpretation available from current framework docs at build time —
  re-verify all of these against your actual Superset version before
  relying on them, especially if extension endpoints 404 or the panel
  doesn't load.
- **No SQL Lab-adjacent contribution point exists for a full admin panel
  today** — only `sqllab.leftSidebar`, `sqllab.editor`,
  `sqllab.rightSidebar`, and `sqllab.panels`. This extension uses
  `sqllab.panels`, the closest fit for a source-management table; there is
  currently no dedicated Superset-wide "Settings" extension point.
- **Alerts & Reports, scheduled reports, and thumbnails do not work**
  (no Celery) — see "What was removed" above for the re-expansion path.
- **`fedctl seed sqlite` and the one-time Superset bootstrap
  (`docker compose exec superset superset fab create-admin ...`) both
  pass values via argv to processes fedctl doesn't control** — Docker's
  own `docker run`/`docker compose exec` argv, and Flask-AppBuilder's
  `create-admin` CLI, which has no stdin-based credential input. This is
  distinct from fedctl's own strict "no credential-shaped flags" rule,
  which governs fedctl's *own* argv only; both of these run once, locally,
  on the host executing `fedctl up`.
- **The exact GRANT syntax for `NAMED COLLECTION CONTROL`** in
  `clickhouse/users.d/roles.xml` should be re-verified against the pinned
  ClickHouse 26.8 release before production use (spec's own instruction:
  verify parameter/privilege names against the pinned version).
- **This build's TypeScript could not be compiled or type-checked** —
  no Node.js/npm was available in the environment that produced it. The
  code is believed correct (React/TS conventions, brace/paren-balance
  checked by hand) but `npm install && npm run typecheck && npm run
  build` in `extension/frontend/` has not actually been run. Do this
  before deploying.
- **The integration test (`test/integration`, `-tags integration`) has
  not been run** — no Docker daemon was available in the environment that
  produced this build. It is structurally complete (builds cleanly under
  `-tags integration`) and documents the intended end-to-end flow, but
  needs a real run against Docker before being trusted.
- **"Admin and Alpha get `can_manage` by default"** (spec section 10) is
  only half-wired: Superset/FAB grants every permission-view pair to Admin
  automatically, so that half holds with zero extra code. Alpha does not
  get a newly-declared extension permission automatically in stock
  Superset — `extension.json`'s `permissions` field declares that
  `can_manage on ChFederationAPI` should exist, but nothing here grants it
  to Alpha specifically. Confirm the extensions framework's own
  provisioning handles this, or grant it manually (Superset admin UI:
  Settings → List Roles → Alpha → add the permission), before relying on
  Alpha-role access.
- **This build went through an adversarial multi-agent verification pass**
  against every rule in spec sections 9–15 (validation, DDL, the Python
  shim, the frontend, the compose/Dockerfile setup, and this README). It
  found and this build fixed one real security bug (a pgx/v5
  `TestConnection` code path that could dial the ClickHouse container's
  own loopback interface, bypassing the SSRF host check —
  `internal/sources/postgres.go`), one functional bug (the Add Source
  modal's displayed field defaults weren't written into the actual submit
  payload), and a handful of smaller gaps (a weak credential-flag-name
  heuristic, no panic recovery, an incomplete audit-log call on source
  removal, and the missing "last checked" column) — all now fixed and
  covered by tests where practical. What that pass could not check —
  actually running `docker compose up`, the four-way join, or `npm run
  build` — remains genuinely unverified, per the items above.
