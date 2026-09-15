# Installation guide

How to get the Superset + ClickHouse federation stack running on your
machine, whether you installed it from the Windows installer or want to
build it from source.

If you are studying the codebase rather than running it, start with the
[guided tour](README.md) instead.

---

## Before you start: two things you must have

### 1. Docker Desktop (required, installed separately)

This stack runs as **two Docker containers**. Docker is not bundled with
the installer — Docker Inc. ships its own installer and it cannot be
redistributed inside someone else's. You install it once, yourself.

| Your OS | Get Docker from | Notes |
|---|---|---|
| **Windows 10/11** | <https://docs.docker.com/desktop/install/windows-install/> | Uses the WSL2 backend. The installer will offer to enable WSL2 for you; accept it. A reboot is usually required. |
| **macOS** | <https://docs.docker.com/desktop/install/mac-install/> | Pick the build matching your chip — Apple silicon (M1/M2/M3/M4) or Intel. |
| **Linux** | <https://docs.docker.com/engine/install/> | Also install the `docker-compose-plugin` and `docker-buildx-plugin` packages — this stack needs both. |

**After installing, confirm it actually works.** Open a terminal and run:

```
docker compose version
docker buildx version
```

Both must print a version. Two common stumbles:

- `docker compose version` fails but `docker-compose --version` works — you
  have the obsolete Compose v1. This stack requires v2 (note the space, not
  a hyphen). Upgrade Docker Desktop, or install the `docker-compose-plugin`
  package on Linux.
- Both commands fail with "cannot connect to the Docker daemon" — Docker is
  installed but not *running*. Start Docker Desktop and wait for its whale
  icon to stop animating.

**Docker Desktop must be running** every time you start the stack, not just
during installation. On Windows and macOS it does not start automatically
unless you enable that in Docker Desktop's own settings.

### 2. A PostgreSQL database for Superset's own bookkeeping

This is the requirement people are most often surprised by, so read this
part carefully.

Superset stores *its own* data — your user accounts, dashboards, saved
queries, permissions — in a database it calls its **metadata database**.
This stack deliberately does **not** run a PostgreSQL container for that.
You must point it at an existing PostgreSQL instance you control.

> **Why not just include one?** Because the obvious fallback — a SQLite file
> inside the container — is destroyed the moment that container is replaced,
> taking every dashboard you ever made with it. The code refuses to start
> rather than let that happen quietly. This is a deliberate reliability
> decision, not an oversight.

This is **completely separate** from the PostgreSQL/MySQL/SQLite databases
you want to *analyse*. Those get attached later, through the app itself.

Your options, roughly easiest first:

- **A free hosted PostgreSQL.** Neon, Supabase, Render, and Railway all have
  free tiers that work fine for this. You get a connection URI immediately.
- **A PostgreSQL you already run** at work or at home.
- **A local PostgreSQL** installed on your machine. Note that if you go this
  route on Windows/macOS, the containers reach it at
  `host.docker.internal`, not `localhost` — and you will need to add that
  host to `FEDCTL_ALLOWED_SOURCE_HOSTS` (see Troubleshooting below).

Whatever you choose, you need a connection URI in this shape, and you will
be asked for it during setup:

```
postgresql://username:password@hostname:5432/databasename
```

The database itself must already exist and be empty (or already contain a
previous Superset installation's tables). Superset creates its own tables
inside it on first run.

---

## Installing on Windows

1. Download `superset-federation-<version>-windows-x64-setup.exe` from the
   [releases page](https://github.com/HeavenlyRaven/SupersetMulticonnector/releases).
2. Run it. It installs **for your user only** — no administrator prompt —
   into `%LOCALAPPDATA%\Programs\SupersetFederation`.
3. Leave **"Add fedctl to my PATH"** ticked unless you have a reason not to.
   It lets you type `fedctl` in any terminal.
4. If Docker isn't installed yet, the installer will say so and let you
   continue anyway. Install Docker before moving on to first-time setup.

Windows may show a **"Windows protected your PC"** SmartScreen warning,
because the installer isn't signed with a paid code-signing certificate.
Click **More info → Run anyway**. (If you'd rather verify the download
first, compare its SHA-256 against the checksum published on the release
page.)

### First-time setup

Open the Start Menu, find the **Superset Federation** folder, and run
**"1. First-time setup"**. A terminal opens and walks you through it.

You'll be asked for a handful of things:

| Prompt | What to enter |
|---|---|
| External PostgreSQL URI | The metadata database URI from above. It's checked immediately and re-asked if malformed. |
| Superset admin username | Press Enter for `admin`. |
| Superset admin password | Type one, or **leave blank to have one generated** — it gets printed once, so copy it somewhere safe. |
| Port to publish Superset on | Press Enter for `8088`, or pick another if something already uses it. |

Everything else — the ClickHouse role passwords, Superset's session signing
key — is generated for you with a cryptographic random source. You are never
asked to invent a password that only two containers will ever see.

Then it runs preflight checks, builds both container images, and starts the
stack.

**The first run takes a few minutes** — it downloads the two container
images (over a gigabyte between them) from the GitHub Container Registry.
Nothing is compiled on your machine; the images are prebuilt and published
with each release, which is why Docker is the only thing you had to install.
Later runs reuse the downloaded images and start in seconds.

When it finishes, it prints the URL. Open **"3. Open Superset"** from the
Start Menu, or browse to `http://localhost:8088`, and log in with the admin
account from above.

### Everyday use

All from the Start Menu's Superset Federation folder:

| Shortcut | What it does |
|---|---|
| **2. Start the stack** | Starts both containers and waits until they report healthy |
| **3. Open Superset** | Opens `http://localhost:8088` in your browser |
| **Stop the stack** | Stops both containers. Your data is kept. |
| **Check the stack** | Probes every attached source and prints a status table |
| **Terminal here** | A command prompt in the install directory, for any other `fedctl` command |

If you ticked "Add fedctl to my PATH", you can also just open any terminal,
`cd` into `%LOCALAPPDATA%\Programs\SupersetFederation`, and run `fedctl`
commands directly.

> **Why does it matter which directory I'm in?** `fedctl` looks for
> `compose.yaml` and `.env` in the *current* directory. Run it somewhere
> else and it will tell you it can't find them. Every Start Menu shortcut
> handles this for you by opening in the right place.

### Uninstalling

Use **Settings → Apps → Installed apps → Superset Federation**, or the
Uninstall shortcut in the Start Menu folder.

The uninstaller removes the program files and your generated `.env`. It does
**not** remove the Docker containers, images, or volumes — those belong to
Docker, not to this installer. To clear them out too, run this *before*
uninstalling:

```
fedctl down --volumes
```

That deletes the `ch-data`, `ch-user-files`, and `superset-home` volumes and
everything in them. Your external PostgreSQL metadata database and your real
source databases are untouched — nothing here was ever a copy of them.

---

## Installing on Linux

Native packages are published for each release. Pick the one for your distro
— all of them install `fedctl` to `/usr/bin` and the stack template to
`/usr/share/superset-federation`.

```bash
# Debian / Ubuntu
sudo dpkg -i superset-federation_<version>_amd64.deb

# Fedora / RHEL / openSUSE
sudo rpm -i superset-federation-<version>.x86_64.rpm

# Alpine
sudo apk add --allow-untrusted superset-federation_<version>_x86_64.apk
```

Replace `amd64`/`x86_64` with `arm64`/`aarch64` on ARM machines. Every
release also ships `checksums.txt`; verify before installing:

```bash
sha256sum -c checksums.txt --ignore-missing
```

### After installing: make your own working copy

`fedctl` writes `.env` next to `compose.yaml`, and `/usr/share` isn't
writable by normal users — so copy the template somewhere that is. Once:

```bash
mkdir -p ~/superset-federation
cp -r /usr/share/superset-federation/. ~/superset-federation/
cd ~/superset-federation
fedctl install
```

From then on, run `fedctl` commands from `~/superset-federation`. (It looks
for `compose.yaml` and `.env` in the *current* directory — there's no
global config location.)

### Without a package manager

Any Linux or macOS machine can use the bare binary instead:

1. Download `fedctl-linux-amd64`, `fedctl-linux-arm64`, `fedctl-darwin-amd64`
   or `fedctl-darwin-arm64` from the
   [releases page](https://github.com/HeavenlyRaven/SupersetMulticonnector/releases).
2. Verify it against `checksums.txt`.
3. Install it and fetch the template files:
   ```bash
   chmod +x fedctl-linux-amd64
   sudo mv fedctl-linux-amd64 /usr/local/bin/fedctl

   git clone https://github.com/HeavenlyRaven/SupersetMulticonnector.git
   cd SupersetMulticonnector
   fedctl install
   ```

On macOS, Gatekeeper blocks the unsigned binary the first time. Either
right-click it in Finder and choose **Open**, or clear the quarantine flag:

```bash
xattr -d com.apple.quarantine /usr/local/bin/fedctl
```

---

## Building from source instead

The installer and the prebuilt binaries are conveniences. Everything is
buildable yourself, and this path is always supported.

You need [Go 1.25+](https://go.dev/dl/) and Docker.

```
git clone https://github.com/HeavenlyRaven/SupersetMulticonnector.git
cd SupersetMulticonnector

go build -o fedctl.exe ./cmd/fedctl     # Windows
go build -o fedctl ./cmd/fedctl         # macOS / Linux

./fedctl install
```

Or via the Makefile: `make build` for your own platform, `make cross-build`
for all five targets plus checksums, `make test` to run the test suite,
`make packages-linux` for the `.deb`/`.rpm`/`.apk` set.

To build the Windows installer itself, see
[installer/windows/README.md](../installer/windows/README.md).

### Which images get used, and when

`fedctl up` decides for itself whether to build the container images or pull
the published ones, by checking whether `images/*/Dockerfile` exists:

| Where you're running it | What happens |
|---|---|
| A **git clone** (Dockerfiles present) | Builds locally, so your source changes are actually in the running containers |
| An **installed copy** (installer, `.deb`) | Pulls the published images from GHCR — there's no source to build |

Override it explicitly when you need to:

```bash
fedctl up --build      # always compile locally (needs a source checkout)
fedctl up --no-build   # always use the published images
```

To run images from somewhere else entirely — your own registry, or a local
tag you built by hand — set either of these before `fedctl up`:

```bash
FEDERATION_SUPERSET_IMAGE=myrepo/superset:dev
FEDERATION_CLICKHOUSE_IMAGE=myrepo/clickhouse:dev
```

---

## Trying it out with sample data

Two different ways to get sample data, for two different situations.

**Testing an installed copy on a machine with nothing else set up** —
including needing a metadata database in the first place, which is normally
the one thing you have to bring yourself: `fedctl testkit up`. One command,
three disposable databases in Docker, and it prints every connection detail
you need — see [test/testkit/README.md](../test/testkit/README.md). This is
the fast path if you're validating an installer or package rather than
developing the project itself.

**Working from a source checkout, with a real metadata database already
configured** — the repo ships two sample source databases (a PostgreSQL and
a MySQL, with a few rows each) behind a `dev` profile, joined to the same
Docker network as the real stack:

```
fedctl up --dev
```

They only start when you ask for them explicitly. Before you can attach
them, add them to the SSRF allowlist in your `.env` — they live on Docker's
internal network, which is blocked by default:

```
FEDCTL_ALLOWED_SOURCE_HOSTS=dev-postgres,dev-mysql
```

Then attach them from SQL Lab's **Federated sources** panel, or from the
command line:

```
fedctl source add --name devpg --type postgres --host dev-postgres --port 5432 --user sample --database sampledb
fedctl source add --name devmy --type mysql    --host dev-mysql    --port 3306 --user sample --database sampledb
```

Both use `sample` as the password. Then join across them in SQL Lab:

```sql
SELECT c.name, o.amount_cents
FROM fed_devpg.customers AS c
JOIN fed_devmy.orders    AS o ON o.customer_id = c.id
```

---

## Troubleshooting

**"Docker was not found on PATH"**
Docker Desktop isn't installed, or isn't running. Start it and wait for the
whale icon to settle before retrying.

**"compose.yaml not found in the current directory"**
You're running `fedctl` from the wrong place. Use the Start Menu shortcuts,
or `cd` into the install directory first.

**Preflight fails on `compose-v2`**
You have the obsolete Compose v1. See the Docker section at the top.

**Preflight fails on `postgres-reachable`**
`fedctl` could not connect to the metadata database URI you gave it. Check
the credentials, that the host is reachable from this machine, and that the
database accepts connections from your IP (hosted providers often need your
address allowlisted). Re-run setup with `fedctl install --force` to enter a
different URI.

**Adding a source fails with "host resolves to ... private/link-local range"**
Working as designed — this is the SSRF guard. Any source host on a private
network is denied unless you explicitly permit it. Add the hostname to
`FEDCTL_ALLOWED_SOURCE_HOSTS` in `.env` and restart the stack:

```
FEDCTL_ALLOWED_SOURCE_HOSTS=dev-postgres,dev-mysql,host.docker.internal
```

**Port 8088 is already in use**
Re-run `fedctl install --force` and pick a different port, or edit
`SUPERSET_PORT` in `.env` and run `fedctl up` again.

**The stack is healthy but SQL Lab has no "Federated sources" panel**
The Superset extension isn't installed in the image. The panel is an
optional add-on; everything else, including `fedctl source add` from the
command line, works without it.

**Something else**
`fedctl doctor` probes every attached source and reports what's failing.
`fedctl preflight` re-runs the environment checks. Both print actionable
messages rather than stack traces.

---

## What's actually running

Once up, you have exactly two containers and three Docker volumes:

| Container | What it is |
|---|---|
| `superset` | Apache Superset, the web UI, on port 8088 |
| `clickhouse` | The federation hub — attaches your databases and joins across them live. Not published to your network at all. |

| Volume | Holds |
|---|---|
| `ch-data` | ClickHouse's own storage |
| `ch-user-files` | Uploaded SQLite files |
| `superset-home` | Superset's file cache |

For what each process does and how data moves between them, see the
[guided tour](README.md).
