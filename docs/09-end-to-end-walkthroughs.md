# 9. End-to-end walkthroughs

Chapters 4 to 7 went file by file. This one goes *event by event*: pick one
thing a user does, and follow it through every layer. This is where the
pieces connect.

---

## Walkthrough 1: `fedctl install` on a fresh machine

You have cloned the repo and built the binary. You run `./fedctl install`.

### Step 0 — before any command runs

[main.go](../cmd/fedctl/main.go) does three things before Cobra sees
anything:

1. Installs the panic guard.
2. Scans `os.Args` for credential-shaped flags. `install` has none, so this
   passes.
3. Calls `loadDotEnv(".env")` — which finds no file on a fresh machine and
   returns silently.

### Step 1 — sanity checks

[install.go](../cmd/fedctl/install.go), `runInstall`:

```go
checkRepoCheckout()   // is compose.yaml here? is .env.example here?
dockerAvailable(ctx)  // is `docker` on PATH?
```

If Docker is missing you get an OS-specific installation link, not just
"command not found".

### Step 2 — writing `.env`

`.env` does not exist, so `writeEnvFile(opts.Yes)` runs. It walks the
`envFields` table:

- `SQLALCHEMY_DATABASE_URI` → you are prompted, and the answer is validated
  by `config.ValidateMetadataURI`. Type a SQLite URI and it re-prompts with
  an explanation. This is the **one value nobody can guess for you**.
- `SUPERSET_SECRET_KEY`, `FEDCTL_ADMIN_PASSWORD`, `SUPERSET_CH_PASSWORD` →
  generated with `crypto/rand`, never shown, never asked about.
- `SUPERSET_ADMIN_PASSWORD` → prompted with hidden input; blank generates one
  and prints it once.
- `SUPERSET_PORT` → prompted with default `8088`.
- Everything else → its default.

Then `.env.example` is read, each `KEY=` line has its value swapped, and the
result is written with mode `0600`. Every comment survives.

At this point you have typed at most four answers and have a complete,
documented configuration file with three cryptographically random secrets in
it.

### Step 3 — reload

```go
loadDotEnv(".env")
```

Called again, because the file that did not exist at Step 0 exists now, and
everything downstream reads configuration from the environment.

### Step 4 — preflight

`preflight.Run` executes every check:

| Check | What it catches |
|---|---|
| `compose-v2` | Compose v1 or missing Compose, with a specific fix |
| `buildx` | Missing buildx plugin |
| `image-manifest:superset-base` | The pinned image no longer exists, or lacks an architecture |
| `image-manifest:clickhouse-base` | Same |
| `cgroup-version` | cgroup v1 on Linux (inaccurate memory limits) |
| `selinux` | Whether SELinux is present |
| `disk-space` | Less than 5 GiB free |
| `memory` | Less than 2 GiB RAM |
| `metadata-uri` | Wrong URI shape |
| `postgres-reachable` | The external PostgreSQL is not reachable **right now** |

All of them run; results print as a table. Any failure stops the install.

The last check earns its keep: without it, a typo in the metadata URI would
be discovered by a Superset container crash-looping five minutes later.

### Step 5 — bringing it up

`runUp(ctx, opts.Dev, 5*time.Minute)` — the same function `fedctl up` calls.
No shelling out to itself.

**5a.** Validate the metadata URI again (cheap, and catches an environment
that changed since preflight).

**5b.** `docker compose -f compose.yaml up -d --build`. Both images build:
each runs the Go build stage, then derives from its base image. First run
takes minutes.

**5c.** `waitHealthy` polls `docker compose ps --format json` every two
seconds.

Inside the ClickHouse container, Docker is running
`/usr/local/bin/fedctl check clickhouse` every 15 seconds. That process:

- loads config from the environment (`CLICKHOUSE_HOST=clickhouse`,
  `FEDCTL_ADMIN_PASSWORD` from `.env` via Compose's `env_file`),
- opens a native-protocol connection to `clickhouse:9000` as `fed_admin`,
- pings.

A successful ping proves the server started, the network works, `roles.xml`
was parsed, and the password matches. Once it passes, Compose marks the
container healthy, which releases Superset's `depends_on` condition and it
starts.

Superset's own health check hits `http://localhost:8088/health` from inside
its container.

**5d.** Bootstrap Superset — three `docker compose exec` calls:

```
superset db upgrade            # create/migrate the metadata schema
superset fab create-admin ...  # create the admin user (fails harmlessly if it exists)
superset init                  # install default roles and permissions
```

**5e.** Register the ClickHouse connection.
[superset.go](../cmd/fedctl/superset.go) logs in over HTTP to
`http://localhost:8088` (the *published* port, since this code runs on your
host), gets a JWT and a CSRF token, and POSTs a database definition:

```
clickhousedb://bi_ro:<generated-password>@clickhouse:8123/default
```

Note the host in that URI is `clickhouse` — because Superset will use it from
*inside* the network. And note the port is 8123, ClickHouse's HTTP port,
because Superset's Python driver speaks HTTP while `fedctl` speaks the native
protocol on 9000.

If the connection already exists, the 422 "already exists" response is
treated as success.

### Result

```
Done. Superset is running at http://localhost:8088
```

Two containers, three volumes, one registered data source, an admin user, and
a `.env` you can read. You answered a handful of questions.

---

## Walkthrough 2: adding a PostgreSQL source from the browser

The most instructive path in the system — it touches every layer.

### 1. Opening the panel

You open SQL Lab. Superset loads the extension's federated module and calls
the function registered by
[index.tsx](../extension/frontend/src/index.tsx), which renders
`<FederationPanel />`.

Its `useEffect` fires:

```tsx
const [list, types] = await Promise.all([api.listSources(), api.sourceTypes()]);
```

Two GET requests. Follow `sourceTypes()`:

```
GET /extensions/acme/ch-federation/source-types
        ↓
entrypoint.py: source_types()  →  @protect(), permission "read"
        ↓
subprocess.run(["/usr/local/bin/fedctl", "source", "types", "--json"], input=b"{}")
        ↓
doSourceTypes() iterates sources.All(), calling Fields() on each
        ↓
stdout: {"ok":true,"data":[{"name":"postgres","engine":"PostgreSQL","hasCredentials":true,"fields":[...]}, ...]}
        ↓
entrypoint.py: return self.response(200, result=data)
        ↓
Browser: sourceTypes state
```

The form's fields have now been described by Go code and shipped to React.

### 2. Filling in the form

You click **Add source**.
[AddSourceModal.tsx](../extension/frontend/src/AddSourceModal.tsx) renders a
Name input, a Type dropdown, and then one input per field in
`activeType.fields` — Host, Port (pre-filled `5432`), User, Password
(`type="password"`), Database, Schema (pre-filled `public`).

Nothing in that rendering knows what PostgreSQL is.

**Add is disabled.** `canAdd` requires a successful test whose recorded
signature equals the current form state.

### 3. Test connection

```
POST /extensions/acme/ch-federation/sources/test
     X-CSRFToken: <fresh token>
     {"name":"sales","type":"postgres","params":{"host":"pg.internal","port":"5432",...,"password":"hunter2"}}
```

In the shim: `@protect()`, then `_can_manage()` — a viewer without manage
access gets 403 here, before any subprocess starts.

Then:

```python
_run_fedctl(["source", "test"], request.json)
```

which becomes:

```python
subprocess.run(["/usr/local/bin/fedctl", "source", "test", "--json"],
               input=b'{"name":"sales",...,"password":"hunter2"}',
               shell=False, capture_output=True, timeout=30)
```

**The password is in `input`, never in the list.** Anyone running `ps` on
that machine sees only `fedctl source test --json`.

Inside `fedctl`, `doTestSource` in
[sourceops.go](../cmd/fedctl/sourceops.go):

```go
ty, params, err := sources.Validate(ctx, req.Type, app.UserFilesDir, req.Params)
// → postgresType.Validate: required fields present? port in range? schema defaulted?

if host, ok := params["host"]; ok {
    if _, err := validate.Host(ctx, nil, host, allowlist(app)); err != nil { return nil, err }
}
// → resolve pg.internal; reject if ANY address is private, unless allowlisted

tableCount, err := ty.TestConnection(ctx, app.UserFilesDir, params)
// → build a pgx config with no stray fallbacks, connect, count information_schema.tables
```

Note what does **not** happen: ClickHouse is never contacted. No named
collection, no database, nothing to undo. The connection is made directly
from the Superset container to your PostgreSQL server using Go's own driver.

Success:

```json
{"ok": true, "data": {"ok": true, "tableCount": 12}}
```

The browser shows a green *"Reachable — 12 table(s) visible."* and **Add**
becomes clickable.

### 4. The SSRF path, for contrast

Suppose you had typed `169.254.169.254` as the host. `validate.Host` resolves
it, finds it inside `169.254.0.0/16`, and returns:

```go
jsonio.NewError(jsonio.CodeHostNotAllowed,
    `host "169.254.169.254" resolves to 169.254.169.254, which is in a private/link-local/loopback range`,
    "add the host or address to the source-host allowlist if this is intentional")
```

That envelope reaches the shim, which maps `HOST_NOT_ALLOWED` → **400**, and
passes message and hint through unchanged. The browser shows both, in a red
alert. **Add stays disabled.**

No connection was attempted. The rejection happened before any socket opened.

### 5. Add

```
POST /extensions/acme/ch-federation/sources
     {"name":"sales","type":"postgres","params":{...}}
```

Shim: permission check → `fedctl source add --json` with the body on stdin.

`doAddSource`:

```go
validate.SourceName("sales")                         // ^[a-z][a-z0-9_]{2,40}$
conn.DatabaseExists(ctx, "fed_sales")                // → SOURCE_EXISTS (409) if it does
attachSource(ctx, conn, app, "sales", "postgres", params)
```

And inside `attachSource`, the five steps from
[chapter 4](04-fedctl-cli.md#sourceopsgo--where-it-all-comes-together):
validate → check host → build plan → **check host again** → execute.

The plan:

```sql
-- step 1
CREATE NAMED COLLECTION IF NOT EXISTS `fed_sales_creds` AS
    host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?
    -- args: "pg.internal", 5432, "svc", <secret>, "salesdb", "public"

-- step 2
CREATE DATABASE IF NOT EXISTS `fed_sales` ENGINE = PostgreSQL(`fed_sales_creds`) COMMENT ?
    -- args: "host=pg.internal"
```

`hub.Attach` executes them in order over the native protocol as `fed_admin`.
If step 2 fails, step 1's undo (`DROP NAMED COLLECTION IF EXISTS
fed_sales_creds`) runs, and the *original* error is returned — redacted of
the password first.

The password reached ClickHouse as a bound parameter. It is now inside a
named collection, and ClickHouse will not show it to anyone, ever — not even
to `fed_admin`.

### 6. The response and the audit entry

```json
{"ok": true, "data": {"name": "sales", "type": "postgres", "database": "fed_sales"}}
```

The shim writes an audit line — user, action, name, type, host, timestamp,
never credentials — and returns **201 Created**.

The modal closes, the panel refreshes, and a new row appears with a table
count read live from ClickHouse.

### 7. Total password exposure

Worth summarising, because it is the point of a lot of this design:

| Location | Present? |
|---|---|
| Browser memory while typing | Yes, unavoidable |
| HTTPS request body | Yes |
| Python variable, briefly | Yes |
| `fedctl`'s stdin | Yes |
| **`fedctl`'s argv** | **No** |
| **Any log line** | **No** |
| **Generated SQL text** | **No** |
| **API response** | **No** |
| **`fedctl plan` output** | **No** |
| ClickHouse named collection | Yes — encrypted at rest, unreadable via SQL |

---

## Walkthrough 3: running a federated join in SQL Lab

Now the payoff. In SQL Lab you select "ClickHouse Federation Hub" and run:

```sql
SELECT c.name, o.amount_cents
FROM fed_sales.customers AS c
JOIN fed_orders.orders   AS o ON o.customer_id = c.id
```

**1. Superset → ClickHouse.** Superset uses `clickhouse-connect` over HTTP on
port 8123, authenticating as `bi_ro`. Its `bi_readonly` profile applies:
`readonly=2`, the join algorithm list, the memory caps,
`max_execution_time=300`, `join_use_nulls=1`, and the query cache.

**2. ClickHouse plans the query.** It sees two databases with foreign
engines. For each, it must fetch rows from a remote server.

**3. ClickHouse → PostgreSQL.** It reads `fed_sales`'s named collection to
get host, port, user, password, database, and schema, opens a PostgreSQL
connection, and issues a query for `customers`. ClickHouse pushes down what
it can — column selection and simple filters — so it does not always fetch
whole tables.

**4. ClickHouse → MySQL.** The same for `fed_orders.orders`.

**5. The join happens in ClickHouse.** Using one of the algorithms from the
profile. `grace_hash` is listed first for good reason: it spills to disk
rather than failing when a join exceeds memory, and the size of a federated
join is not predictable in advance.

**6. Results stream back** to Superset over HTTP, and Superset renders them.

**No data was stored anywhere.** Change a row in PostgreSQL, re-run, and you
see the new value. That is the entire promise of the system, and it is
delivered by ClickHouse's engines plus two `CREATE DATABASE` statements.

### What "live" costs you

Honesty about trade-offs:

- Every query hits the source systems. A heavy dashboard puts real load on
  your production PostgreSQL.
- Join performance is bounded by network transfer, not by ClickHouse's
  column-store speed. ClickHouse is fast on its own data; on federated data
  it is bounded by how quickly PostgreSQL can hand rows over.
- If a source is down, queries touching it fail. There is no cached copy to
  fall back on.
- The 60-second query cache takes the edge off repeated dashboard loads.

For live cross-database joins over moderate data, this is an excellent trade.
For a hundred-million-row aggregation over data that changes hourly, ETL into
ClickHouse's own tables is still the right answer — and nothing here stops
you doing both.

---

## Walkthrough 4: uploading a SQLite file

SQLite is a file, so it must physically reach the volume. Two paths do that.

### Path A — the CLI, from your host

```bash
fedctl seed sqlite --from ./regions.sqlite --as regions.sqlite
```

1. `os.Stat` — does it exist, and is it a regular file?
2. `seedNameRe` — is the destination name safe (no separators, no `..`)?
3. `resolveComposeVolume(ctx, "ch-user-files")` — ask Docker for the *real*
   volume name via Compose's labels.
4. Run a throwaway container:

   ```
   docker run --rm \
       -v /host/dir:/src:ro,z \
       -v superset-federation_ch-user-files:/dest \
       clickhouse/clickhouse-server:26.8.1.2041@sha256:... \
       cp /src/regions.sqlite /dest/regions.sqlite
   ```

The running stack never gained a bind mount; a temporary container did the
crossing and deleted itself.

### Path B — the browser

You are in the Add Source dialog with type `sqlite`, and click **Upload new
file…**.

[SqliteFileField.tsx](../extension/frontend/src/SqliteFileField.tsx) builds a
`FormData`, deliberately sets no `Content-Type`, and POSTs to
`/sources/sqlite-files`.

The shim reads the file part and pipes its raw bytes to:

```
fedctl source sqlite-files put --name regions.sqlite --json
```

`putSQLiteFile` in [sqlitefiles.go](../cmd/fedctl/sqlitefiles.go):

1. Validate the name with the same `seedNameRe`.
2. Refuse if the file exists and `--replace` was not given.
3. Create a temp file in the target directory.
4. `io.Copy` through an `io.LimitReader` capped at 200 MiB + 1.
5. If more than 200 MiB arrived, fail.
6. `chmod 0644`, then `os.Rename` into place — atomic.

This runs **inside the Superset container**, writing into `ch-user-files`,
which is mounted there read-write and group-writable thanks to
`group_add: ["101"]` and the Dockerfile's `chown 101:101` / `chmod 0775`.

### Then attaching it

Pick the file in the dropdown and click Test, then Add:

```go
sqliteType.Validate(userFilesDir, params)
  → validate.SQLitePath("/var/lib/clickhouse/user_files", "regions.sqlite")
      → clean, join, check containment
      → EvalSymlinks, check containment again
      → Stat, must be a regular file
      → returns "/var/lib/clickhouse/user_files/regions.sqlite"
```

One step of DDL, no named collection:

```sql
CREATE DATABASE IF NOT EXISTS `fed_regions` ENGINE = SQLite(?)
    -- args: "/var/lib/clickhouse/user_files/regions.sqlite"
```

That absolute path is valid in the ClickHouse container because the *same
volume* is mounted at the same path there. The two containers agree on the
path because the volume makes them agree.

### Replace

Later the data is stale. SQLite has no live connection to re-read, so you
select the file and click **Replace selected file…**:

```tsx
const result = await api.uploadSqliteFile(file, value, true);
//                                              ^^^^^  ^^^^
//                             the CURRENT name ──┘      └── overwrite = true
```

The new bytes land under the *existing* name via an atomic rename. The
ClickHouse database still points at that path, so it needs no changes, and
the next query reads the new data.

---

## Walkthrough 5: a health check cycle

Every 15 seconds, forever, inside the ClickHouse container:

```
Docker → /usr/local/bin/fedctl check clickhouse
           ↓
main(): panic guard, credential-flag scan, loadDotEnv(".env")  [no .env in the container — fine]
           ↓
check.go → loadApp(false)
           CLICKHOUSE_HOST=clickhouse         (from compose.yaml's environment:)
           FEDCTL_ADMIN_PASSWORD=<secret>     (from .env via env_file:)
           ↓
health.Check(ctx, "clickhouse", app)
           ↓
hub.Open({Host: "clickhouse", Port: 9000, User: "fed_admin", ...})
           ↓
db.PingContext(ctx)  — 5 second timeout
           ↓
exit 0 → Docker marks the container healthy
```

The same binary, the same configuration loading, the same connection code as
every other command. A health check that shares its implementation with the
real code path cannot pass while the real path is broken.

If the ping fails, `fedctl` exits non-zero. After 5 consecutive failures
Docker marks the container `unhealthy`, `docker compose ps` shows it, and
`fedctl up` reports it in the timeout message.

---

## Walkthrough 6: the declarative path

For infrastructure-as-code, skip the UI entirely.

`config/sources.yaml`:

```yaml
sources:
  - name: sales
    type: postgres
    host: pg.example.internal
    port: 5432
    user: svc_readonly
    password_env: FED_SALES_PASSWORD
    database: salesdb
    schema: public
```

Commit that file — there is no password in it.

```bash
export FED_SALES_PASSWORD='...'
fedctl plan  -f config/sources.yaml    # preview
fedctl apply -f config/sources.yaml    # execute
```

`plan` prints:

```sql
-- source: sales (postgres)
CREATE NAMED COLLECTION IF NOT EXISTS `fed_sales_creds` AS host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?;
CREATE DATABASE IF NOT EXISTS `fed_sales` ENGINE = PostgreSQL(`fed_sales_creds`) COMMENT ?;
```

The `?` placeholders are not redaction — they are what the statement text
actually contains. Values are bound separately at execution.

`apply` calls the **same** `attachSource` the browser path calls, per source,
collecting results rather than stopping at the first failure. Run it twice
and nothing happens the second time, because every statement is
`IF NOT EXISTS`.

---

## Walkthrough 7: removing a source

```
DELETE /extensions/acme/ch-federation/sources/sales
```

Shim: permission check → `fedctl source remove --json` with `{"name":"sales"}`.

`doRemoveSource`:

```go
validate.SourceName("sales")
engine, err := conn.DatabaseEngine(ctx, "fed_sales")   // → "PostgreSQL", or SOURCE_NOT_FOUND (404)
hasCollection := engine != "SQLite"
sourceType := hub.EngineToType(engine)                  // "postgres"
host := conn.SourceHost(ctx, "sales")                   // read the COMMENT — BEFORE dropping
plan, _ := ddl.Detach("sales", hasCollection)
conn.Detach(ctx, plan)
```

```sql
DROP DATABASE IF EXISTS `fed_sales`;
DROP NAMED COLLECTION IF EXISTS `fed_sales_creds`;
```

The host is read before anything is dropped, because the audit log needs it
and it is about to cease to exist.

In the browser, this required typing `sales` exactly into the confirmation
box first.

**What is destroyed:** the ClickHouse database definition and the stored
credentials. **What is not:** your PostgreSQL server and every row in it.
Detaching is not deleting — nothing was ever copied, so there is nothing of
yours to lose.

---

## Putting it together

Every path you have followed passes through the same narrow places:

```
   CLI ─────────┐
                ├──▶ cmd/fedctl/sourceops.go ──▶ sources.Validate
   Browser ─────┤                            ──▶ validate.Host  (twice)
                │                            ──▶ ddl.AttachX
   sources.yaml ┘                            ──▶ hub.Attach (with rollback)
```

Three entry points, one implementation. That is why a security rule added in
`validate.go` immediately applies to the web UI, the CLI, and the config
file, with nothing else to update — and it is the single best idea in this
codebase to carry into your own work.

---

Next: [Glossary](10-glossary.md).
