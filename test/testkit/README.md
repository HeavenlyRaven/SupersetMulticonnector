# Testkit

Three disposable databases for exercising a fresh install of this stack —
never part of the real product, never something an end user touches.

## Why this exists

Testing a *release* — the Windows installer, the Linux packages, the
published container images — means testing on a machine that genuinely has
nothing else set up: no Postgres for Superset's metadata, no source
databases to attach and join across. Before this existed, that meant either
signing up for a free hosted database somewhere, or hand-building sample
data on every machine you wanted to test on. This is the alternative:
everything runs in Docker, entirely disposable, identical on every machine.

## Quickest path

```
fedctl testkit up
```

One command. It starts three containers, waits for them to be ready, and
prints every host, port, username, and path you need — ready to paste
straight into `fedctl install` and `fedctl source add`. Nothing to look up
anywhere else.

On Windows, this is also a Start Menu shortcut: **Superset Federation →
Testing → Start test databases**. It opens a terminal and stays open, so the
printed information stays visible.

Forgot the details after closing that terminal? `fedctl testkit info`
reprints the exact same block without touching Docker at all — every value
is fixed in advance, nothing is discovered at runtime.

When you're done:

```
fedctl testkit down
```

(or the matching **Stop test databases** shortcut). This deletes the
containers and all their data. Nothing here is meant to persist.

## What actually starts

| Container | Host port | Purpose |
|---|---|---|
| `metadata-postgres` | `5433` | Superset's own metadata database — what `SQLALCHEMY_DATABASE_URI` needs to point at |
| `source-postgres` | `5434` | A federated PostgreSQL source, seeded with the same sample `customers` table as `compose.dev.yaml`'s `dev-postgres` |
| `source-mysql` | `3307` | A federated MySQL source, seeded with the same sample `orders` table as `dev-mysql` |

Plus `region_notes.sqlite`, a small pre-built file sitting right in this
directory — SQLite needs no hosting at all, just a file to upload.

All three regions match up across every source (`us-east`, `eu-west`,
`us-west`), so a three-way join actually returns real, non-empty rows —
exactly the same shape `test/integration/federation_test.go` verifies for
the real stack.

## Why `host.docker.internal`, and the one platform gap

Every value `fedctl testkit up` prints uses `host.docker.internal`, not
`localhost`. That's deliberate, not a copy-paste default: the metadata URI
has to resolve **twice over** — once when `fedctl install` checks it from
your bare host during preflight, and continuously afterward from *inside*
the running Superset container. `localhost` means something different in
each of those two places (the container's own loopback vs. yours), so it
can't be the answer; `host.docker.internal` is the one name recognized in
both contexts.

On Windows and macOS Docker Desktop, this resolves automatically — Desktop's
own DNS provides it, from the host and from any container. On plain Linux
Docker Engine (no Desktop layer), the *container* side is handled for you —
`compose.yaml` sets `extra_hosts: host.docker.internal:host-gateway` on both
real services specifically for this — but the **host's own** resolution of
that name isn't something Docker adds on bare Linux. If `fedctl testkit up`
or `fedctl install` can't reach the metadata database on such a machine, add
this once:

```
echo "127.0.0.1 host.docker.internal" | sudo tee -a /etc/hosts
```

That makes the name resolve consistently everywhere it's used: as the host
itself when the host asks, and as the real host machine (via
`host-gateway`) when a container asks.

## Manual equivalent, if you'd rather not use `fedctl testkit`

```
docker compose -f test/testkit/compose.yaml up -d
docker compose -f test/testkit/compose.yaml down -v   # when finished
```

`fedctl testkit up`/`down` are exactly these two commands, plus the
health-wait and the printed summary — there's no hidden behavior.

## Regenerating `region_notes.sqlite`

It was built once with `modernc.org/sqlite` (the same pure-Go driver
`fedctl` itself uses) and committed as a binary fixture — not regenerated on
demand, the same way `dev/postgres-init/001-sample.sql` is committed rather
than generated. If its contents ever need to change, recreate the file with
a `region_notes(region TEXT PRIMARY KEY, note TEXT NOT NULL)` table holding
rows for `us-east`, `eu-west`, and `us-west`, matching whatever regions
`init/postgres/001-sample.sql`'s `customers` table uses, and commit the
result in place of this one.
