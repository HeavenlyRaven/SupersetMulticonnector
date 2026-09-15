# 1. What this project actually does

## The problem

Imagine a company where the data lives in three different places:

- Customer records are in a **PostgreSQL** database.
- Orders are in a **MySQL** database (maybe it came with a legacy app).
- A small reference table — region codes, product notes — is a **SQLite**
  file someone keeps on a shared drive.

Now someone asks a normal business question:

> "Show me every customer, their orders, the note for their region, and who
> manages that region."

Answering it means joining data across three separate database systems that
have no idea the others exist. Plain SQL cannot join across servers. Each
database speaks its own dialect and lives behind its own connection.

## The two usual answers, and why this project picks a third

**Answer 1: ETL.** Extract the data from all three, transform it, and load it
into one central data warehouse on a schedule. This is the industry standard.
It works, but it costs you: pipelines to build and babysit, and data that is
always a little bit stale — as stale as the time since the last sync.

**Answer 2: do it in the application.** Query three databases separately and
join the results in Python. Fine for small data, terrible past that, and you
lose SQL entirely.

**Answer 3, the one this project implements: query federation.** Put one
database in the middle that can *reach into* the other three live, and let it
do the join. Nothing is copied. Nothing is scheduled. When you run the query,
the middle database opens connections to PostgreSQL and MySQL, reads the rows
it needs right now, reads the SQLite file, and joins them in memory.

The middle database here is **ClickHouse**, and this project calls it the
**federation hub**.

```
                                  ┌──────────────────────┐
   You, in a browser  ───────────▶│   Apache Superset    │
                                  │   (charts, SQL Lab)  │
                                  └──────────┬───────────┘
                                             │ one SQL query
                                             ▼
                                  ┌──────────────────────┐
                                  │      ClickHouse      │
                                  │  the federation hub  │
                                  └──┬────────┬───────┬──┘
                       live reads ┌──┘        │       └──┐
                                  ▼           ▼          ▼
                          ┌────────────┐ ┌─────────┐ ┌─────────────┐
                          │ PostgreSQL │ │  MySQL  │ │ SQLite file │
                          └────────────┘ └─────────┘ └─────────────┘
```

The key sentence, worth memorising:

> **ClickHouse does not copy the data. It attaches the other databases and
> reads them live, at query time.**

If someone changes a row in PostgreSQL, the next query through Superset sees
the new value within seconds. There is no sync job, because there is nothing
to sync.

## What ClickHouse is doing under the hood

ClickHouse has a feature called **database engines**. Normally when you
create a database in ClickHouse, ClickHouse stores the tables itself. But you
can also say:

```sql
CREATE DATABASE fed_sales ENGINE = PostgreSQL(...connection details...);
```

From that moment, `fed_sales` is not really a ClickHouse database — it is a
*window* onto a PostgreSQL server. Listing tables in `fed_sales` asks
PostgreSQL what tables it has. Selecting from `fed_sales.customers` opens a
connection to PostgreSQL and streams rows back. ClickHouse stores nothing.

The same trick exists for MySQL (`ENGINE = MySQL(...)`) and for SQLite files
(`ENGINE = SQLite('/path/to/file.sqlite')`).

Once three such databases exist side by side inside ClickHouse, a plain SQL
join across them just works:

```sql
SELECT c.name, o.amount_cents, rn.note, rm.manager
FROM fed_dev_postgres.customers      AS c                            -- PostgreSQL
JOIN fed_dev_mysql.orders            AS o  ON o.customer_id = c.id   -- MySQL
JOIN fed_dev_sqlite_ref.region_notes AS rn ON rn.region = c.region   -- SQLite
JOIN default.region_managers         AS rm ON rm.region = c.region   -- ClickHouse itself
```

That exact query is what the integration test in
[federation_test.go](../test/integration/federation_test.go) runs to prove
the whole system works: four different storage backends, one query.

## So what is in this repository?

Everything needed to **run and operate** that idea:

1. **Two containers.** One running Superset, one running ClickHouse. That is
   the entire production stack — see [compose.yaml](../compose.yaml).
2. **A Go command-line tool called `fedctl`.** It installs the stack,
   attaches and detaches sources, validates input, generates the SQL, and
   answers health checks. It lives in [cmd/fedctl/](../cmd/fedctl/) and
   [internal/](../internal/).
3. **A Superset extension.** It adds a "Federated sources" panel inside SQL
   Lab, so a person can add a source by filling in a form instead of using
   the CLI. It lives in [extension/](../extension/).
4. **Configuration for both containers** — how ClickHouse should behave, what
   users it should have, how Superset should be set up. In
   [clickhouse/](../clickhouse/) and [superset/](../superset/).

## The single most important design idea in the whole repo

Look at this picture carefully:

```
   ┌──────────── superset container ─────────────┐   ┌─── clickhouse container ───┐
   │                                             │   │                            │
   │  Superset (Python web app)                  │   │  ClickHouse server         │
   │      │                                      │   │       ▲                    │
   │      │ HTTP request to the extension        │   │       │ native protocol    │
   │      ▼                                      │   │       │ port 9000          │
   │  entrypoint.py  (thin Python shim)          │   │       │                    │
   │      │                                      │   │       │                    │
   │      │ runs a subprocess                    │   │       │                    │
   │      ▼                                      │   │       │                    │
   │  /usr/local/bin/fedctl  ────────────────────┼───┼───────┘                    │
   │       (the same Go binary you run by hand)  │   │                            │
   └─────────────────────────────────────────────┘   └────────────────────────────┘
```

`fedctl` is baked into **both** container images. It is:

- the CLI *you* type commands into (`fedctl up`, `fedctl source add`),
- the program the container health checks run (`fedctl check clickhouse`),
- **and** the program the Superset extension runs behind the scenes every
  time someone clicks a button in the browser.

Why does that matter? Because there is then exactly **one** implementation of
the rules. One place that validates a hostname. One place that generates SQL.
One place that talks to ClickHouse. The web UI cannot drift out of sync with
the CLI, because the web UI *is* the CLI, just invoked by a Python function
instead of by your shell.

The Python code between the browser and `fedctl` is deliberately about 200
lines and contains no business logic at all — it checks a permission, runs
`fedctl`, and passes the answer back. You will see that stated repeatedly in
the comments: *"if you find yourself writing logic here, move it to Go."*

## What was deliberately left out, and what that costs

A normal production Superset deployment runs five or six containers: Superset
itself, a Redis cache, a Celery worker, a Celery scheduler, a PostgreSQL
database for Superset's own bookkeeping, and your data warehouse.

This project runs **two**. The trade-offs were made on purpose and are
written down in the [README](../README.md):

| Removed | What it cost |
|---|---|
| **Redis** | The cache became files on disk instead of Redis. Fine at this scale. |
| **Celery** (background job runner) | SQL Lab queries run *synchronously*. A slow query occupies one web worker for its whole duration. Alerts, scheduled reports, and dashboard thumbnails stop working entirely — they all need Celery. |
| **A PostgreSQL container** | Superset still needs a PostgreSQL database to store its own dashboards, users, and saved queries. Here you must supply an **external** one. The code refuses to start without it, and refuses to fall back to SQLite. |

That last point confuses people, so be precise about it: there are **two
completely different roles** a database can play in this system.

1. **Superset's metadata database** — where Superset saves *its own* stuff:
   users, dashboards, saved queries, permissions. This is the
   `SQLALCHEMY_DATABASE_URI` setting. It must be an external PostgreSQL that
   you provide.
2. **Data sources** — the PostgreSQL/MySQL/SQLite databases holding the
   business data you want to *query*. These get attached to ClickHouse.

They have nothing to do with each other. If `fedctl` refuses to start because
`SQLALCHEMY_DATABASE_URI` is unset, that is about Superset's own bookkeeping
database, not about your sales data.

Why the hard refusal to use SQLite for metadata? Because Superset's default
behaviour is to quietly fall back to a SQLite file, and a SQLite file inside
a container is destroyed the moment the container is replaced — taking every
dashboard with it. The comment in
[superset_config.py](../superset/superset_config.py) calls this "a
reliability incident, not a style question", and the code exits rather than
let it happen.

## Security, stated up front

Two features of this system are dangerous if built naïvely, and the code
spends real effort on both.

**Someone can type a hostname into a web form, and a server inside your
network will connect to it.** That is the textbook setup for an attack called
**SSRF** (Server-Side Request Forgery). An attacker who can reach the form
could point it at `169.254.169.254` — the cloud metadata service, which hands
out credentials — or at an internal admin panel, and use your own server as a
proxy into your own network. The defence lives in
[validate.go](../internal/validate/validate.go): every hostname is resolved
to IP addresses, and every address is checked against a deny-list of private
and loopback ranges — **twice**, once when validating and again immediately
before connecting.

**Passwords for the source databases pass through the system.** The rules are
strict: a password never appears as a command-line argument (on Linux anyone
on the machine can read another process's arguments through `/proc`), never
appears in a log line, never appears in generated SQL, and never comes back
in an API response. It travels on standard input only, and is handed to
ClickHouse as a bound parameter that ends up inside a *named collection* —
ClickHouse's own secret store.

Both get a full treatment later. They are mentioned now so you know why so
much of the code looks paranoid: it is paranoid on purpose, and the paranoia
is documented at each site.

---

Next: [Background concepts](02-background-concepts.md) — the Docker, SQL,
Superset, and Go vocabulary you need before reading the code.
