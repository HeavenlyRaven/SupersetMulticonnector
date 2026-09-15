# 3. Repository map

Every file in the repository, grouped by purpose, with a one-line job
description. Use this as an index — the detailed explanations are in chapters
4 through 8.

There are **82 tracked files**. Anything else you see in the working
directory (`fedctl.exe`, `dist/`, `extension/dist/`,
`extension/frontend/node_modules/`, `*.supx`, `.env`) is build output or
local secrets, ignored by git — see [.gitignore](../.gitignore).

---

## The shape of the tree

```
SupersetMulticonnector/
├── README.md                  ← operator documentation
├── superset-clickhouse-two-container-spec.md   ← the original build spec
├── compose.yaml               ← the two-container production stack
├── compose.dev.yaml           ← optional sample databases for testing
├── .env / .env.example        ← all configuration and secrets
├── Makefile                   ← build, test, cross-compile
│
├── cmd/fedctl/                ← THE CLI: one file per command  (chapter 4)
├── internal/                  ← THE LOGIC: validation, DDL, ClickHouse  (chapter 5)
│
├── images/                    ← Dockerfiles for both containers  (chapter 6)
├── clickhouse/                ← ClickHouse server + user configuration
├── superset/                  ← Superset's Python configuration
├── config/sources.yaml        ← declarative source definitions
├── dev/                       ← sample data for the dev databases
│
├── extension/                 ← the Superset UI extension  (chapter 7)
│   ├── backend/               ← thin Python shim
│   └── frontend/              ← React/TypeScript panel
│
├── test/integration/          ← the full-stack test  (chapter 8)
└── docs/                      ← this guide
```

---

## Root files

| File | Job |
|---|---|
| [README.md](../README.md) | Operator-facing documentation: quickstart, architecture, tuning knobs, known limitations. Written for someone deploying this, not studying it. |
| [superset-clickhouse-two-container-spec.md](../superset-clickhouse-two-container-spec.md) | **The original specification the whole project was built from.** Fifteen numbered sections of requirements. Code comments refer to it constantly ("spec section 9"). Read it after chapter 1 — it explains the *why* behind nearly every constraint. |
| [compose.yaml](../compose.yaml) | Defines the two production services, three volumes, one network, both health checks. |
| [compose.dev.yaml](../compose.dev.yaml) | Adds a sample PostgreSQL and MySQL under the `dev` profile, so you can try the system without real databases. Never started unless asked for. |
| [.env.example](../.env.example) | The documented template for every configuration key. Copied to `.env` by `fedctl install`. |
| `.env` | Your real configuration and secrets. Git-ignored. Created by `fedctl install`. |
| [Makefile](../Makefile) | Build targets: `build`, `test`, `fmt`, `vet`, `cross-build` (all five OS/CPU targets plus checksums), `clean`. |
| [go.mod](../go.mod) | Go module name and dependency list. |
| [go.sum](../go.sum) | Cryptographic hashes for every dependency. |
| [.dockerignore](../.dockerignore) | Files excluded from the Docker build context — notably `.env`, so secrets never enter an image. |
| [.gitignore](../.gitignore) | Build outputs and secrets that must not be committed. |
| [.gitattributes](../.gitattributes) | `* text=auto eol=lf` — forces Unix line endings, so files written on Windows still work inside Linux containers. |

---

## `cmd/fedctl/` — the command-line tool (chapter 4)

The pattern: one file per command, each exporting a `newXxxCmd()` function
that builds a Cobra command.

| File | Job |
|---|---|
| [main.go](../cmd/fedctl/main.go) | Entry point. Panic guard, credential-flag rejection, `.env` loading, command tree assembly, CLI error printing. |
| [common.go](../cmd/fedctl/common.go) | Shared helpers every command uses: load config, open a ClickHouse connection as `fed_admin` or `bi_ro`, read a JSON request, write a JSON response. |
| [install.go](../cmd/fedctl/install.go) | `fedctl install` — the one-command first-time setup. Checks Docker, generates secrets, writes `.env`, runs preflight, brings the stack up. The largest file here. |
| [preflight.go](../cmd/fedctl/preflight.go) | `fedctl preflight` — runs the host environment checks and prints them as a table. |
| [up.go](../cmd/fedctl/up.go) | `fedctl up` — starts the stack, waits for health, bootstraps Superset, registers the ClickHouse connection. |
| [down.go](../cmd/fedctl/down.go) | `fedctl down` — stops the stack, optionally deleting volumes. |
| [compose.go](../cmd/fedctl/compose.go) | Helpers for invoking `docker compose` safely, and for resolving real Docker volume names via Compose's own labels. |
| [source.go](../cmd/fedctl/source.go) | The `fedctl source ...` command tree: `types`, `list`, `add`, `remove`, `test`, `tables`, `probe`, `sqlite-files`. Flag parsing and JSON-mode plumbing only. |
| [sourceops.go](../cmd/fedctl/sourceops.go) | The actual implementations behind those subcommands. This is where validation, DDL building, and execution are wired together. |
| [sqlitefiles.go](../cmd/fedctl/sqlitefiles.go) | `fedctl source sqlite-files list|put` — listing and safely writing uploaded SQLite files. Powers the browser upload. |
| [apply.go](../cmd/fedctl/apply.go) | `fedctl apply` and `fedctl plan` — the declarative, config-file-driven path. |
| [seed.go](../cmd/fedctl/seed.go) | `fedctl seed sqlite` — copies a SQLite file from your host into the shared volume using a throwaway container. |
| [doctor.go](../cmd/fedctl/doctor.go) | `fedctl doctor` — probes every source and prints a status table. |
| [check.go](../cmd/fedctl/check.go) | `fedctl check <target>` — the health check the containers run. |
| [version.go](../cmd/fedctl/version.go) | `fedctl version` — prints version, commit, build date, platform. |
| [images.go](../cmd/fedctl/images.go) | The two pinned base image references, as constants, so preflight can verify them. |
| [dotenv.go](../cmd/fedctl/dotenv.go) | A minimal `.env` file parser, so `fedctl` running on your host sees the same values the containers do. |
| [main_test.go](../cmd/fedctl/main_test.go) | Tests that CLI error printing does not mangle real error messages. |
| [dotenv_test.go](../cmd/fedctl/dotenv_test.go) | Tests `.env` parsing, including that a real shell variable beats the file. |

---

## `internal/` — the logic (chapter 5)

| Package | File | Job |
|---|---|---|
| **jsonio** | [jsonio.go](../internal/jsonio/jsonio.go) | The request/response contract shared with Python: error codes, the `{ok, data, error}` envelope, and a Go error type that carries a code. Read this package first — everything else returns its errors. |
| **validate** | [validate.go](../internal/validate/validate.go) | Every security-critical check: source names, SSRF host checking, ports, SQLite path traversal, credential-shaped flag names, secret redaction. |
| | [validate_test.go](../internal/validate/validate_test.go) | 17 tests covering all of the above. The most important test file in the repo. |
| **ddl** | [ddl.go](../internal/ddl/ddl.go) | Generates the SQL that attaches and detaches a source. Pure string building — executes nothing. |
| | [ddl_test.go](../internal/ddl/ddl_test.go) | Golden-file tests asserting exact SQL text, and that no password appears in it. |
| **hub** | [hub.go](../internal/hub/hub.go) | The only code that talks to ClickHouse: connect, execute a plan with rollback, list sources, list tables, probe. |
| | [databases.go](../internal/hub/databases.go) | One helper: look up a database's engine, or report "not found". |
| **sources** | [registry.go](../internal/sources/registry.go) | The `Type` interface and the registry that makes source types pluggable. |
| | [postgres.go](../internal/sources/postgres.go) | The PostgreSQL source type: its form fields, validation, and a direct test connection via `pgx`. |
| | [mysql.go](../internal/sources/mysql.go) | The same for MySQL. |
| | [sqlite.go](../internal/sources/sqlite.go) | The same for SQLite — no credentials, but a validated file path. |
| | [util.go](../internal/sources/util.go) | One tiny helper for parsing ports. |
| | [sources_test.go](../internal/sources/sources_test.go) | Registry and per-type validation tests. |
| | [testconn_test.go](../internal/sources/testconn_test.go) | Connection-testing tests, including the SSRF fallback-leak regression test. |
| **config** | [config.go](../internal/config/config.go) | Reads configuration from environment variables; parses `config/sources.yaml`; validates the metadata URI. |
| | [config_test.go](../internal/config/config_test.go) | Metadata URI and YAML parsing tests. |
| **health** | [health.go](../internal/health/health.go) | The two health check implementations (`clickhouse`, `superset`). |
| **preflight** | [preflight.go](../internal/preflight/preflight.go) | Host environment checks: Compose v2, buildx, image manifests, cgroups, SELinux, metadata database reachability. |
| | [resources_linux.go](../internal/preflight/resources_linux.go) | Disk/RAM checks using Linux syscalls. |
| | [resources_darwin.go](../internal/preflight/resources_darwin.go) | The same using macOS sysctl. |
| | [resources_windows.go](../internal/preflight/resources_windows.go) | The same, calling into `kernel32.dll`. |

---

## Container and configuration files (chapter 6)

| File | Job |
|---|---|
| [images/superset/Dockerfile](../images/superset/Dockerfile) | Builds `fedctl`, then derives from the official Superset image, adding the ClickHouse and PostgreSQL drivers, the config file, and `fedctl`. |
| [images/clickhouse/Dockerfile](../images/clickhouse/Dockerfile) | Builds `fedctl`, then derives from the official ClickHouse image, adding just `fedctl` and a correctly-permissioned `user_files` directory. |
| [clickhouse/config.d/federation.xml](../clickhouse/config.d/federation.xml) | Server settings: listen address, memory ratio, query log retention, query cache limits. |
| [clickhouse/users.d/roles.xml](../clickhouse/users.d/roles.xml) | The two users and their exact grants, plus the join-tuning profile. Removes the default no-password user. |
| [superset/superset_config.py](../superset/superset_config.py) | Superset's configuration: metadata URI (with a hard refusal to start if wrong), filesystem cache, feature flags, timeouts, extension paths. |
| [config/sources.yaml](../config/sources.yaml) | Example declarative source definitions for `fedctl apply`. Passwords are named environment variables, never literals. |
| [dev/postgres-init/001-sample.sql](../dev/postgres-init/001-sample.sql) | Three sample customers, loaded automatically into the dev PostgreSQL. |
| [dev/mysql-init/001-sample.sql](../dev/mysql-init/001-sample.sql) | Four sample orders, loaded automatically into the dev MySQL. |

---

## `extension/` — the Superset UI extension (chapter 7)

| File | Job |
|---|---|
| [extension/extension.json](../extension/extension.json) | The extension manifest: publisher, name, version, declared permissions. |
| [extension/backend/pyproject.toml](../extension/backend/pyproject.toml) | Python package metadata and which files belong in the built bundle. |
| [extension/backend/src/acme/ch_federation/entrypoint.py](../extension/backend/src/acme/ch_federation/entrypoint.py) | **The shim.** Nine REST endpoints, each one: check permission → run `fedctl --json` → map the result to an HTTP status → audit. No logic beyond that. |
| `extension/backend/src/acme/__init__.py` | Empty file marking `acme` as a Python package. |
| `extension/backend/src/acme/ch_federation/__init__.py` | Empty file marking `ch_federation` as a Python package. |
| [extension/backend/tests/conftest.py](../extension/backend/tests/conftest.py) | Fake Superset/Flask modules so the shim can be unit-tested without installing Superset. |
| [extension/backend/tests/test_entrypoint.py](../extension/backend/tests/test_entrypoint.py) | Seven tests: permission gating, subprocess safety, error mapping, no password leakage. |
| [extension/frontend/src/index.tsx](../extension/frontend/src/index.tsx) | Registers the panel with Superset's SQL Lab. The entry point. |
| [extension/frontend/src/FederationPanel.tsx](../extension/frontend/src/FederationPanel.tsx) | The main panel: the source table, health dots, Probe and Remove buttons. |
| [extension/frontend/src/AddSourceModal.tsx](../extension/frontend/src/AddSourceModal.tsx) | The Add Source dialog, whose fields are generated from the server's answer, not hardcoded. |
| [extension/frontend/src/SqliteFileField.tsx](../extension/frontend/src/SqliteFileField.tsx) | The SQLite file picker with Upload and Replace buttons. |
| [extension/frontend/src/RemoveSourceModal.tsx](../extension/frontend/src/RemoveSourceModal.tsx) | The type-the-name-to-confirm removal dialog. |
| [extension/frontend/src/Modal.tsx](../extension/frontend/src/Modal.tsx) | A hand-rolled modal, because Superset's component library does not export one yet. |
| [extension/frontend/src/api.ts](../extension/frontend/src/api.ts) | The typed HTTP client, including CSRF handling and the multipart upload. |
| [extension/frontend/src/types.ts](../extension/frontend/src/types.ts) | TypeScript interfaces mirroring the Go structs. |
| [extension/frontend/src/styles.ts](../extension/frontend/src/styles.ts) | Style helpers and a light/dark palette detected from the host page. |
| [extension/frontend/webpack.config.js](../extension/frontend/webpack.config.js) | Module Federation build configuration — how the panel gets loaded into a running Superset. |
| [extension/frontend/package.json](../extension/frontend/package.json) | Frontend dependencies and build scripts. |
| [extension/frontend/package-lock.json](../extension/frontend/package-lock.json) | Exact locked dependency versions. Machine-generated. |
| [extension/frontend/tsconfig.json](../extension/frontend/tsconfig.json) | TypeScript compiler settings (`strict: true`). |

---

## `test/` — the integration test (chapter 8)

| File | Job |
|---|---|
| [test/integration/federation_test.go](../test/integration/federation_test.go) | Starts the real stack, attaches all three source types, runs the four-way join, asserts real rows. Only runs with `-tags integration`. |
| [test/integration/compose.test.yaml](../test/integration/compose.test.yaml) | Test-only overlay that publishes ClickHouse's port so the test process can connect from outside Docker. |

---

## Files you will see but that are not source

| Path | What it is |
|---|---|
| `fedctl.exe` | Your locally-built Windows binary. Rebuild with `go build -o fedctl.exe ./cmd/fedctl`. |
| `dist/` | Cross-compiled binaries for all five targets plus `checksums.txt`, produced by `make cross-build`. |
| `extension/dist/`, `extension/frontend/dist/` | Built extension bundles. |
| `extension/ch-federation-0.1.0.supx` | The packaged extension, ready to install into Superset. |
| `extension/frontend/node_modules/` | Downloaded npm dependencies. |
| `__pycache__/`, `*.pyc` | Python bytecode caches. |
| `.env` | Your real secrets. |

---

Next: [`fedctl`, the Go CLI](04-fedctl-cli.md).
